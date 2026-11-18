package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	"go.uber.org/zap"
	"gorm.io/datatypes"

	"video-canvas/internal/model"
	"video-canvas/internal/pkg/errcode"
	"video-canvas/internal/pkg/logger"
	"video-canvas/internal/provider"
	"video-canvas/internal/provider/modelcfg"
	"video-canvas/internal/repository"
)

// GetTask 按 id 读取任务（队列 worker 用，不按用户过滤）。任务不存在返回 (nil, nil)。
func (s *GenerationTaskService) GetTask(ctx context.Context, id uint64) (*model.GenerationTask, error) {
	t, err := s.repo.GetByIDAny(ctx, id)
	if errors.Is(err, repository.ErrNotFound) {
		return nil, nil
	}
	return t, err
}

// AcquireSlot 为 pending 任务申请用户名额与渠道名额，拿到后生成截止时间从此刻起按模型超时重新计算。
//
//  1. 用户并发上限：单用户覆盖 > 系统默认（试跑任务不看用户上限，传 0 即可）；
//  2. 全局默认并发上限也要带上：渠道排队时要推算排在前面的等待者里谁真能拿到名额；
//  3. 新的截止时间 = 现在 + 模型配置的超时（缺省 30 分钟）；快照损坏时用默认值，任务照常处理（worker 稍后会因快照损坏直接失败退款）。
func (s *GenerationTaskService) AcquireSlot(ctx context.Context, t *model.GenerationTask) (provider.SlotOutcome, error) {
	userLimit := 0
	if !t.IsTest {
		n, err := s.maxActiveTasks(ctx, t.UserID)
		if err != nil {
			return "", err
		}
		userLimit = n
	}
	defLimit, err := s.defaultMaxActiveTasks(ctx)
	if err != nil {
		return "", err
	}
	now := s.now()
	deadline := defaultTaskDeadline
	var snap provider.Snapshot
	if err := json.Unmarshal(t.ConfigSnapshot, &snap); err == nil {
		deadline = taskDeadline(&snap)
	}
	res, err := s.repo.AcquireSlot(ctx, repository.SlotRequest{
		TaskID: t.ID, UserLimit: userLimit, DefaultUserLimit: defLimit, Now: now, Deadline: now.Add(deadline),
	})
	return provider.SlotOutcome(res), err
}

// SaveProviderState 保存插件的准备结果与私有状态，不改变任务状态。
func (s *GenerationTaskService) SaveProviderState(ctx context.Context, t *model.GenerationTask, st provider.ProviderState) error {
	raw, err := json.Marshal(st)
	if err != nil {
		return fmt.Errorf("编码 provider state 失败: %w", err)
	}
	updated, err := s.repo.UpdateIf(ctx, t.ID, model.ActiveTaskStatuses, map[string]any{
		"provider_state": datatypes.JSON(raw),
	}, false)
	if errors.Is(err, repository.ErrStateConflict) {
		return nil
	}
	if err != nil {
		return err
	}
	if updated == nil {
		return nil
	}
	return nil
}

// SaveTrace 保存试跑任务的追踪步骤，正式任务与空追踪直接忽略。
func (s *GenerationTaskService) SaveTrace(ctx context.Context, t *model.GenerationTask, steps []provider.TraceStep) error {
	if t == nil || !t.IsTest || len(steps) == 0 {
		return nil
	}
	raw, err := json.Marshal(steps)
	if err != nil {
		return fmt.Errorf("编码试跑追踪失败: %w", err)
	}
	return s.repo.SaveTrace(ctx, t.ID, datatypes.JSON(raw))
}

// restartDeadline 把任务的超时时间改成“从现在起一个完整的模型超时”。
// 创建任务时 deadline_at = 创建时间 + 超时，pending 阶段它就是排队的上限；任务真正交给上游后，
// 生成时长应该从这一刻重新算，不能让排队的时间吃掉生成的时间。快照解析失败时保持原值，不影响提交结果。
func (s *GenerationTaskService) restartDeadline(t *model.GenerationTask, now time.Time, fields map[string]any) {
	var snap provider.Snapshot
	if err := json.Unmarshal(t.ConfigSnapshot, &snap); err != nil {
		logger.Warn("解析任务快照失败，保持原有截止时间", logger.GenRun(t.ID), zap.Error(err))
		return
	}
	fields["deadline_at"] = now.Add(taskDeadline(&snap))
}

// MarkSubmitting 记录任务开始调用上游：状态保持 pending，写入提交时间并推送。
// 拿到并发名额、真正去请求上游才算“生成中”；同步接口在上游返回前都是这个状态。
func (s *GenerationTaskService) MarkSubmitting(ctx context.Context, t *model.GenerationTask) (bool, error) {
	updated, applied, err := s.transit(ctx, t.ID, []string{model.TaskPending}, map[string]any{"submitted_at": s.now()}, true)
	if err != nil || !applied {
		return false, err
	}
	s.publish(ctx, updated)
	return true, nil
}

// MarkSubmitted 记录异步任务已被上游受理，并从受理时刻起重新计算生成超时。
func (s *GenerationTaskService) MarkSubmitted(ctx context.Context, t *model.GenerationTask, providerTaskID string, pluginState json.RawMessage, nextPollAt time.Time) (bool, error) {
	now := s.now()
	fields := map[string]any{
		"status":           model.TaskQueued,
		"provider_task_id": providerTaskID,
		"submitted_at":     now,
		"next_poll_at":     nextPollAt,
		"poll_attempts":    0,
		"lease_until":      nil,
	}
	s.restartDeadline(t, now, fields)
	if len(pluginState) > 0 {
		raw, err := mergeProviderState(t.ProviderState, provider.ProviderState{Plugin: pluginState})
		if err != nil {
			return false, err
		}
		fields["provider_state"] = datatypes.JSON(raw)
	}
	updated, applied, err := s.transit(ctx, t.ID, []string{model.TaskPending}, fields, true)
	if err != nil || !applied {
		return false, err
	}
	s.publish(ctx, updated)
	return true, nil
}

// MarkImmediate 记录同步接口的即时结果并进入转存阶段。
func (s *GenerationTaskService) MarkImmediate(ctx context.Context, t *model.GenerationTask, providerTaskID string, pluginState json.RawMessage, outputs json.RawMessage) (bool, error) {
	now := s.now()
	fields := map[string]any{
		"status":           model.TaskFinalizing,
		"provider_task_id": providerTaskID,
		"provider_result":  datatypes.JSON(outputs),
		"next_poll_at":     now,
		"poll_attempts":    0,
		"lease_until":      nil,
	}
	// 转存窗口是 deadline_at + TransferGrace：同样从上游出结果的时刻重新算，别让排队时间挤掉转存的余量
	s.restartDeadline(t, now, fields)
	if len(pluginState) > 0 {
		raw, err := mergeProviderState(t.ProviderState, provider.ProviderState{Plugin: pluginState})
		if err != nil {
			return false, err
		}
		fields["provider_state"] = datatypes.JSON(raw)
	}
	updated, applied, err := s.transit(ctx, t.ID, []string{model.TaskPending}, fields, true)
	if err != nil || !applied {
		return false, err
	}
	s.publish(ctx, updated)
	return true, nil
}

// MarkPolled 记录上游仍在处理的状态与插件状态。
func (s *GenerationTaskService) MarkPolled(ctx context.Context, t *model.GenerationTask, status string, progress *int, pluginState json.RawMessage, attempts int, nextPollAt time.Time) (bool, error) {
	if status != model.TaskQueued && status != model.TaskRunning {
		return false, fmt.Errorf("MarkPolled 不支持的状态：%s", status)
	}
	fields := map[string]any{
		"status":        status,
		"poll_attempts": attempts,
		"next_poll_at":  nextPollAt,
		"lease_until":   nil,
	}
	changed := status != t.Status
	if progress != nil {
		fields["progress"] = *progress
		changed = changed || t.Progress == nil || *t.Progress != *progress
	}
	if len(pluginState) > 0 {
		raw, err := mergeProviderState(t.ProviderState, provider.ProviderState{Plugin: pluginState})
		if err != nil {
			return false, err
		}
		fields["provider_state"] = datatypes.JSON(raw)
	}
	updated, applied, err := s.transit(ctx, t.ID, []string{model.TaskQueued, model.TaskRunning}, fields, changed)
	if err != nil || !applied {
		return false, err
	}
	if changed {
		s.publish(ctx, updated)
	}
	return true, nil
}

// MarkFinalizing 把已成功的上游任务转入产物转存阶段。
func (s *GenerationTaskService) MarkFinalizing(ctx context.Context, t *model.GenerationTask) (bool, error) {
	updated, applied, err := s.transit(ctx, t.ID, []string{model.TaskQueued, model.TaskRunning}, map[string]any{
		"status":        model.TaskFinalizing,
		"progress":      100,
		"poll_attempts": 0,
		"next_poll_at":  s.now(),
		"lease_until":   nil,
	}, true)
	if err != nil || !applied {
		return false, err
	}
	s.publish(ctx, updated)
	return true, nil
}

// Retry 安排一次可重试的任务处理。
func (s *GenerationTaskService) Retry(ctx context.Context, t *model.GenerationTask, attempts int, nextPollAt time.Time) (bool, error) {
	_, applied, err := s.transit(ctx, t.ID, []string{t.Status}, map[string]any{
		"poll_attempts": attempts,
		"next_poll_at":  nextPollAt,
		"lease_until":   nil,
	}, false)
	return applied, err
}

// Complete 完成转存。费用已在用户自己的 TokenPay 账户中结算，平台只记录任务状态和用量。
func (s *GenerationTaskService) Complete(ctx context.Context, t *model.GenerationTask, outputs []model.TaskOutput, usage *modelcfg.Usage) (bool, error) {
	if outputs == nil {
		outputs = []model.TaskOutput{}
	}
	raw, err := json.Marshal(outputs)
	if err != nil {
		return false, err
	}
	_ = usage // 保留参数以兼容 worker 契约；平台积分不再结算。
	updated, applied, err := s.finish(ctx, t.ID, []string{model.TaskFinalizing}, model.TaskSucceeded, map[string]any{
		"output_json":     datatypes.JSON(raw),
		"progress":        100,
		"error_code":      "",
		"error_message":   "",
		"charged_credits": 0,
	})
	if err != nil || !applied {
		return false, err
	}
	s.publish(ctx, updated)
	s.writeCanvasCover(ctx, updated, outputs)
	return true, nil
}

// writeCanvasCover 任务成功后，把产出里第一张图写成所属画布的封面（画布已有封面则不动，所以封面是画布里第一张图）。
// 封面只是列表页的装饰：试跑任务、不属于画布、没有图片产出都跳过；写失败只记日志，不能让已经完成的任务算失败。
func (s *GenerationTaskService) writeCanvasCover(ctx context.Context, t *model.GenerationTask, outputs []model.TaskOutput) {
	if s.covers == nil || t.IsTest || t.CanvasProjectID == nil {
		return
	}
	for _, o := range outputs {
		if o.MediaType != "image" || o.AssetID == 0 {
			continue
		}
		if err := s.covers.SetCoverIfEmpty(ctx, t.UserID, *t.CanvasProjectID, o.AssetID); err != nil {
			logger.Warn("写入画布封面失败", logger.GenRun(t.ID), zap.Error(err))
		}
		return
	}
}

// Fail 把非终态任务置为失败。平台不冻结、不退款，上游实际费用以 TokenDance 账单为准。
func (s *GenerationTaskService) Fail(ctx context.Context, t *model.GenerationTask, errorCode, message string) (bool, error) {
	return s.FailWithUpstream(ctx, t, errorCode, message, "")
}

// FailWithUpstream 与 Fail 相同，并把上游错误原文写进这次迁移产生的 failed 事件，供后台「使用记录」排查。
// 原文由调用方（worker）从插件宿主拿到，宿主构造时已经遮掉渠道 Key；这里只按 upstreamLimit 截断（保护存储，不是脱敏）。
// 它只进事件详情，绝不写进任务的 error_message：用户看到的永远是统一文案。
func (s *GenerationTaskService) FailWithUpstream(ctx context.Context, t *model.GenerationTask, errorCode, message, upstream string) (bool, error) {
	extra := map[string]any{
		"error_code":    errorCode,
		"error_message": message,
	}
	if upstream != "" {
		extra[repository.EventDetailKey] = map[string]any{"upstream": clipUTF8(upstream, upstreamLimit)}
	}
	updated, applied, err := s.finish(ctx, t.ID, model.ActiveTaskStatuses, model.TaskFailed, extra)
	if err != nil || !applied {
		return false, err
	}
	s.publish(ctx, updated)
	return true, nil
}

// clipUTF8 把 s 按字节截到不超过 n，且不切断 UTF-8 字符；没超过原样返回。
func clipUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

// Expire 把超时任务置为 expired；平台不结算或退款，费用以 TokenDance 实际账单为准。
func (s *GenerationTaskService) Expire(ctx context.Context, t *model.GenerationTask) (bool, error) {
	// 从没调用过上游（pending 且没有过提交重试）就超时，说明是在排队：文案要让用户知道不是生成失败
	msg := taskMsgTimeout
	if t.Status == model.TaskPending && t.PollAttempts == 0 {
		msg = taskMsgQueueTimeout
	}
	updated, applied, err := s.finish(ctx, t.ID, model.ActiveTaskStatuses, model.TaskExpired, map[string]any{
		"error_code":              TaskErrTimeout,
		"error_message":           msg,
		repository.EventDetailKey: map[string]any{"reason": "deadline"},
	})
	if err != nil || !applied {
		return false, err
	}
	s.publish(ctx, updated)
	return true, nil
}

// taskMsgCancelFinalizing 是任务结果已经拿到、正在转存时用户再取消的文案。
const taskMsgCancelFinalizing = "结果已生成，正在保存，无法取消"

// Cancel 用户取消任务。平台只改任务状态，并尽力通知上游取消；用户 TokenDance 账户实际产生的费用以账单为准。
// 转存中结果已经到手，不允许取消，等任务自己出结果。
func (s *GenerationTaskService) Cancel(ctx context.Context, userID, id uint64) (*model.GenerationTaskView, error) {
	return s.cancel(ctx, userID, id, false)
}

// CancelForced 不受「已提交给平台」的限制：后台封禁用户要取消他全部进行中的任务，走这里。
// 取消成功后尽力通知插件执行器取消上游任务。
func (s *GenerationTaskService) CancelForced(ctx context.Context, userID, id uint64) (*model.GenerationTaskView, error) {
	return s.cancel(ctx, userID, id, true)
}

func (s *GenerationTaskService) cancel(ctx context.Context, userID, id uint64, force bool) (*model.GenerationTaskView, error) {
	t, err := s.repo.GetByID(ctx, userID, id)
	if errors.Is(err, repository.ErrNotFound) || (err == nil && t.IsTest) {
		return nil, errcode.ErrTaskNotFound
	}
	if err != nil {
		return nil, err
	}
	if model.IsTerminalStatus(t.Status) {
		return nil, errcode.ErrTaskNotCancelable
	}
	// 用户取消：转存中不放行；平台积分已停用，所有阶段都只改任务状态。
	// 状态迁移的前置条件按读到的情况收紧：读到之后任务状态又变了（如进入转存），这次取消不会生效
	from := model.ActiveTaskStatuses
	reason := "user_cancel"
	if !force {
		if t.Status == model.TaskFinalizing {
			return nil, errcode.ErrTaskNotCancelable.WithMsg(taskMsgCancelFinalizing)
		}
		from = []string{model.TaskPending}
		if t.Status != model.TaskPending || t.SubmittedAt != nil {
			from = []string{model.TaskPending, model.TaskQueued, model.TaskRunning}
		}
	} else {
		// 后台封禁并取消：不是用户自己点的，事件里写明，运营排查时不会误以为是用户取消
		reason = "admin_cancel"
	}
	detail := map[string]any{"reason": reason}
	extra := map[string]any{
		"error_code":              TaskErrCanceled,
		"error_message":           taskMsgCanceled,
		repository.EventDetailKey: detail,
	}
	if t.Status != model.TaskPending || t.SubmittedAt != nil {
		extra["error_message"] = taskMsgCanceledCharged
	}
	updated, applied, err := s.finish(ctx, id, from, model.TaskCanceled, extra)
	if err != nil {
		return nil, err
	}
	if !applied {
		return nil, errcode.ErrTaskNotCancelable
	}
	s.cancelProvider(ctx, updated)
	s.publish(ctx, updated)
	return taskView(updated), nil
}

// CancelActiveByUser 取消用户全部进行中的正式任务（后台封禁并取消任务用）。
// 每个任务复用强制取消的完整流程（状态迁移、通知上游取消、推送）。
// 单个任务失败不影响其他任务：失败的 id 放进结果，已成功的不回滚；只有查询进行中任务失败才返回 error。
func (s *GenerationTaskService) CancelActiveByUser(ctx context.Context, userID uint64) (*CancelActiveResult, error) {
	// 1. 取该用户全部非终态的正式任务（试跑任务不占积分，不在其中）
	tasks, err := s.repo.ListActive(ctx, userID)
	if err != nil {
		return nil, err
	}
	// 2. 逐个取消。任务在查询之后被别的流程结束（返回“不可取消”）说明目标已达成，既不算取消也不算失败
	res := &CancelActiveResult{}
	for i := range tasks {
		_, err := s.CancelForced(ctx, userID, tasks[i].ID)
		var ec *errcode.Error
		switch {
		case err == nil:
			res.Canceled++
		case errors.As(err, &ec) && ec.Code == errcode.ErrTaskNotCancelable.Code:
		default:
			logger.Warn("取消进行中任务失败", zap.Error(err), logger.GenRun(tasks[i].ID), zap.Uint64("user_id", userID))
			res.Failed = append(res.Failed, tasks[i].ID)
		}
	}
	return res, nil
}

func (s *GenerationTaskService) cancelProvider(ctx context.Context, t *model.GenerationTask) {
	if t == nil || t.ProviderTaskID == "" || s.executor == nil {
		return
	}
	var snap provider.Snapshot
	if err := json.Unmarshal(t.ConfigSnapshot, &snap); err != nil {
		logger.Warn("解析任务快照失败，跳过上游取消", logger.GenRun(t.ID), zap.Error(err))
		return
	}
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), executorCancelTimeout)
	defer cancel()
	err := s.executor.Cancel(cctx, &snap, provider.TaskRef{
		ID: t.ID, UserID: t.UserID, ProviderTaskID: t.ProviderTaskID,
	})
	if err != nil && !errors.Is(err, provider.ErrCancelUnsupported) {
		logger.Warn("通知上游取消任务失败", logger.GenRun(t.ID), zap.String("channel", t.Provider), zap.Error(err))
	}
}

func (s *GenerationTaskService) transit(ctx context.Context, id uint64, from []string, fields map[string]any, bumpVersion bool) (*model.GenerationTask, bool, error) {
	t, err := s.repo.UpdateIf(ctx, id, from, fields, bumpVersion)
	if errors.Is(err, repository.ErrStateConflict) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return t, true, nil
}

func (s *GenerationTaskService) finish(ctx context.Context, id uint64, from []string, target string, extra map[string]any) (*model.GenerationTask, bool, error) {
	fields := map[string]any{"status": target, "finished_at": s.now(), "lease_until": nil}
	for k, v := range extra {
		fields[k] = v
	}
	var updated *model.GenerationTask
	err := s.repo.WithTx(ctx, func(tx repository.GenerationTaskTx) error {
		t, err := tx.UpdateIf(ctx, id, from, fields, true)
		if err != nil {
			return err
		}
		updated = t
		// Agent 发起的任务仍要把完成事件交给 Agent，否则审批后的等待流程会一直挂着。
		if !t.IsTest && t.AgentRunID != 0 {
			return tx.EnqueueAgentNotify(ctx, t)
		}
		return nil
	})
	if errors.Is(err, repository.ErrStateConflict) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	logGenFinished(ctx, updated, target, extra)
	return updated, true, nil
}

func mergeProviderState(raw []byte, next provider.ProviderState) ([]byte, error) {
	current, ok := provider.DecodeProviderState(raw)
	if !ok {
		current = provider.ProviderState{}
	}
	if len(next.Prepared) > 0 {
		current.Prepared = next.Prepared
	}
	if len(next.Plugin) > 0 {
		current.Plugin = next.Plugin
	}
	out, err := json.Marshal(current)
	if err != nil {
		return nil, fmt.Errorf("编码 provider state 失败: %w", err)
	}
	return out, nil
}
