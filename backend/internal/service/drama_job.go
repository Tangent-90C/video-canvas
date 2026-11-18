package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"video-canvas/internal/model"
	"video-canvas/internal/pkg/errcode"
	"video-canvas/internal/pkg/logger"
	"video-canvas/internal/provider"
	"video-canvas/internal/provider/modelcfg"
	"video-canvas/internal/repository"

	"go.uber.org/zap"
)

// Estimate 使用现有模型定价及相同请求范围估价，不冻结积分。
func (s *DramaService) Estimate(ctx context.Context, userID, id uint64, req *model.DramaJobReq) (int, error) {
	if req.Step != "compose" {
		if err := s.ensureWallet(ctx, userID); err != nil {
			return 0, err
		}
	}
	if req.Step == "compose" {
		return s.estimateCompose(ctx, userID, id, req)
	}
	if err := checkStoryboardRequest(req); err != nil {
		return 0, err
	}
	if req.Step == "storyboard" {
		return s.estimateStoryboard(ctx, userID, id, req)
	}
	// 1. 估价执行与提交相同的前置检查。
	p, err := s.repo.Project(ctx, userID, id, false)
	if err != nil {
		return 0, dramaError(err)
	}
	eps, err := s.repo.Episodes(ctx, userID, id)
	if err != nil {
		return 0, err
	}
	selected, err := selectDramaEpisodes(p, eps, req)
	if err != nil {
		return 0, err
	}
	if req.Step == "asset_prompts" {
		jobs, e := s.repo.Jobs(ctx, userID, id, false)
		if e != nil {
			return 0, e
		}
		scope := dramaEpisodeNumbers(selected)
		if _, e = dramaRetryAssets(jobs, scope); e != nil {
			return 0, e
		}
	}
	snap, err := s.resolveModel(ctx, p.TextModel)
	if err != nil {
		return 0, err
	}
	return dramaQuote(p, req.Step, selected, snap), nil
}

// CreateJob 同项目任务串行提交，模型任务冻结积分，合成任务不访问模型或账户。
func (s *DramaService) CreateJob(ctx context.Context, userID, id uint64, req *model.DramaJobReq) (*model.DramaJobView, error) {
	return s.createDramaJob(ctx, userID, id, nil, req)
}

// createDramaJob 统一普通提交和审批提交，审批ID与任务在同一事务登记。
func (s *DramaService) createDramaJob(ctx context.Context, userID, id uint64, approvalID *uint64, req *model.DramaJobReq) (*model.DramaJobView, error) {
	if req.Step != "compose" {
		if err := s.ensureWallet(ctx, userID); err != nil {
			return nil, err
		}
	}
	if req.Step == "analyze" || req.Step == "outline" || req.Step == "plan" {
		return nil, errcode.ErrInvalidParams.WithMsg("研读与规划由系统自动发起")
	}
	if err := checkStoryboardRequest(req); err != nil {
		return nil, err
	}
	// 1. 审批重试先恢复原任务，即使模型配置后来失效也不能创建第二个任务。
	if approvalID != nil {
		previous, err := s.approvedDramaJob(ctx, userID, id, *approvalID)
		if err != nil || previous != nil {
			return previous, err
		}
	}
	p, err := s.repo.Project(ctx, userID, id, false)
	if err != nil {
		return nil, dramaError(err)
	}
	if req.Step == "storyboard" {
		seconds, e := s.storyboardSeconds(ctx, p, req)
		if e != nil {
			return nil, e
		}
		resolved := *req
		resolved.MaxSeconds = seconds
		req = &resolved
	}
	snap, err := s.jobFreeze(ctx, p, req)
	if err != nil {
		return nil, err
	}
	var job *model.DramaJob
	err = s.repo.WithTx(ctx, func(tx repository.DramaTx) error {
		var e error
		job, e = s.insertDramaJob(ctx, tx, userID, id, approvalID, req, snap)
		return e
	})
	if err != nil {
		return nil, dramaError(err)
	}
	s.publish(ctx, job)
	logDramaCreated(ctx, job, nil)
	v := jobView(*job)
	return &v, nil
}

// jobFreeze 冻结模型配置；合成不调模型，返回空快照。
func (s *DramaService) jobFreeze(ctx context.Context, p *model.DramaProject, req *model.DramaJobReq) (*provider.Snapshot, error) {
	if req.Step == "compose" {
		return nil, nil
	}
	snap, err := s.resolveModel(ctx, p.TextModel)
	if err != nil {
		return nil, err
	}
	if !snap.Channel.Wallet {
		return nil, errcode.ErrWalletNotConnected.WithMsg("该模型未接入 TokenPay 钱包")
	}
	return snap, nil
}

// insertDramaJob 在项目锁内恢复审批已建任务，否则校验并入队。
func (s *DramaService) insertDramaJob(ctx context.Context, tx repository.DramaTx, userID, id uint64, approvalID *uint64, req *model.DramaJobReq, snap *provider.Snapshot) (*model.DramaJob, error) {
	current, err := tx.Project(ctx, userID, id, true)
	if err != nil {
		return nil, err
	}
	if approvalID != nil {
		job, err := tx.JobByApproval(ctx, userID, *approvalID)
		if err == nil {
			if job.DramaID != id {
				return nil, errcode.ErrDramaNotFound
			}
			return job, nil
		}
		if !errors.Is(err, repository.ErrNotFound) {
			return nil, err
		}
	}
	selected, err := s.prepareDramaJob(ctx, tx, current, req)
	if err != nil {
		return nil, err
	}
	if req.Step == "compose" {
		return enqueueCompose(ctx, tx, current, req, selected, approvalID)
	}
	return s.enqueue(ctx, tx, current, req, snap, selected, approvalID)
}

// prepareDramaJob 在项目锁内校验并发、前置条件与比例。
func (s *DramaService) prepareDramaJob(ctx context.Context, tx repository.DramaTx, p *model.DramaProject, req *model.DramaJobReq) ([]model.DramaEpisode, error) {
	jobs, e := tx.Jobs(ctx, p.UserID, p.ID, true)
	if e != nil {
		return nil, e
	}
	if dramaJobsConflict(jobs, req) {
		return nil, errcode.ErrDramaJobBusy
	}
	eps, e := tx.Episodes(ctx, p.UserID, p.ID)
	if e != nil {
		return nil, e
	}
	selected, e := selectDramaEpisodes(p, eps, req)
	if e != nil {
		return nil, e
	}
	if req.Step == "storyboard" {
		if e := checkStoryboardTakes(ctx, tx, p.UserID, selected); e != nil {
			return nil, e
		}
	}
	if req.Step == "storyboard" || req.Step == "compose" {
		if e := checkDramaMediaTasks(ctx, tx, p, selected, ""); e != nil {
			return nil, e
		}
	}
	if req.Step == "breakdown" && p.Ratio == "" {
		return nil, errcode.ErrDramaStepNotReady.WithMsg("项目缺少画面比例")
	}
	return selected, nil
}

// ListJobs 返回用户项目任务，active仅返回排队或执行中的任务。
func (s *DramaService) ListJobs(ctx context.Context, userID, id uint64, active bool) ([]model.DramaJobView, error) {
	// 1. 查询前确认项目归属，空结果序列化为[]。
	if _, e := s.repo.Project(ctx, userID, id, false); e != nil {
		return nil, dramaError(e)
	}
	jobs, e := s.repo.Jobs(ctx, userID, id, active)
	if e != nil {
		return nil, e
	}
	out := make([]model.DramaJobView, 0, len(jobs))
	for _, j := range jobs {
		out = append(out, jobView(j))
	}
	return out, nil
}

// CancelJob 幂等取消任务；费用以 TokenDance 实际账单为准。
func (s *DramaService) CancelJob(ctx context.Context, userID, dramaID, jobID uint64) (*model.DramaJobView, error) {
	// 1. 按project→job→credit顺序加锁，取消和完成只有一个能结算。
	var job *model.DramaJob
	canceled := false
	err := s.repo.WithTx(ctx, func(tx repository.DramaTx) error {
		if _, e := tx.Project(ctx, userID, dramaID, true); e != nil {
			return e
		}
		var e error
		job, e = tx.Job(ctx, userID, dramaID, jobID, true)
		if e != nil {
			return e
		}
		if !dramaActive(job.Status) {
			return nil
		}
		if pipelineJob(*job) {
			return errcode.ErrDramaPhase.WithMsg("自动规划请使用暂停接口")
		}
		// 已写入画布的批次保留，按检查点费用结算，其余退回
		if e := s.finishTx(ctx, tx, job, "canceled", 0, "", committedCost(job), nil); e != nil {
			return e
		}
		canceled = true
		return nil
	})
	if err != nil {
		return nil, dramaError(err)
	}
	s.publish(ctx, job)
	if canceled {
		_, run := dramaJobRun(ctx, job)
		run.End(logger.StatusCanceled, append(dramaFields(job), zap.String("by", "user"))...)
	}
	v := jobView(*job)
	return &v, nil
}

// ensureWallet 要求短剧用户已连接 TokenPay 钱包；未注入检查器时拒绝，不存在积分回退。
func (s *DramaService) ensureWallet(ctx context.Context, userID uint64) error {
	if s.wallets == nil {
		return errcode.ErrWalletNotConnected
	}
	connected, err := s.wallets.Connected(ctx, userID)
	if err != nil {
		return err
	}
	if !connected {
		return errcode.ErrWalletNotConnected
	}
	return nil
}

// resolveModel 跟随后台 Agent 钱包模型排序；失效的选择回落至第一个仍启用的钱包模型。
func (s *DramaService) resolveModel(ctx context.Context, key string) (*provider.Snapshot, error) {
	if s.registry == nil {
		return nil, errcode.ErrDramaNoTextModel
	}
	models, e := s.registry.ListModels(ctx, modelcfg.KindAgent)
	if e != nil {
		return nil, e
	}
	keys := make([]string, 0, len(models))
	for _, m := range models {
		if m.Wallet && m.Key == key {
			keys = append(keys, key)
		}
	}
	for _, m := range models {
		if m.Wallet && m.Key != key {
			keys = append(keys, m.Key)
		}
	}
	for _, k := range keys {
		snap, e := s.registry.Snapshot(ctx, k)
		if errors.Is(e, provider.ErrModelUnavailable) {
			continue
		}
		if e != nil {
			return nil, e
		}
		if snap != nil && snap.Model.Kind == modelcfg.KindAgent && snap.Channel.Wallet {
			return snap, nil
		}
	}
	return nil, errcode.ErrDramaNoTextModel
}

// enqueue 同事务创建业务任务与 River job；费用由用户 TokenPay 钱包直接承担。
func (s *DramaService) enqueue(ctx context.Context, tx repository.DramaTx, p *model.DramaProject, req *model.DramaJobReq, snap *provider.Snapshot, eps []model.DramaEpisode, approvalID *uint64) (*model.DramaJob, error) {
	scope := []int{}
	for _, ep := range eps {
		scope = append(scope, ep.No)
	}
	j := &model.DramaJob{UserID: p.UserID, DramaID: p.ID, Step: req.Step, Episodes: scope, Status: "queued", Credits: 0, FrozenCredits: 0, Lane: "user", InputJSON: []byte(`{}`), Output: []byte(`{}`), ModelID: snap.ModelRevisionID}
	j.ApprovalID = approvalID
	if req.Step == "analyze" || req.Step == "plan" || req.Step == "outline" {
		j.Lane = "pipeline"
	}
	if e := s.snapshotDramaJob(ctx, tx, p, j, snap); e != nil {
		return nil, e
	}
	if req.Step == "storyboard" {
		if e := enqueueStoryboard(ctx, tx, j, req, eps); e != nil {
			return nil, e
		}
	}
	if req.Step == "asset_prompts" {
		if e := prepareDramaAssetInput(ctx, tx, j); e != nil {
			return nil, e
		}
	}
	if e := tx.InsertJob(ctx, j); e != nil {
		return nil, e
	}
	status := p.Status
	switch req.Step {
	case "analyze":
		status = "analyzing"
	case "scripts":
		status = "scripting"
	}
	if e := tx.UpdateProject(ctx, p.UserID, p.ID, p.Revision, map[string]any{"status": status, "text_model_id": snap.ModelRevisionID}); e != nil {
		return nil, e
	}
	return j, nil
}

// prepareDramaAssetInput 在任务创建前恢复上次同范围的新资产集合。
func prepareDramaAssetInput(ctx context.Context, tx repository.DramaTx, j *model.DramaJob) error {
	jobs, e := tx.Jobs(ctx, j.UserID, j.DramaID, false)
	if e != nil {
		return e
	}
	assets, e := dramaRetryAssets(jobs, j.Episodes)
	if e != nil {
		return e
	}
	raw, e := json.Marshal(assets)
	if e != nil {
		return e
	}
	j.InputJSON = raw
	return nil
}

// dramaEpisodeNumbers 提取集号供范围比较，保持自然顺序。
func dramaEpisodeNumbers(eps []model.DramaEpisode) []int {
	scope := make([]int, 0, len(eps))
	for _, ep := range eps {
		scope = append(scope, ep.No)
	}
	return scope
}

// selectDramaEpisodes 统一估价与提交的步骤前置条件。
func selectDramaEpisodes(p *model.DramaProject, eps []model.DramaEpisode, req *model.DramaJobReq) ([]model.DramaEpisode, error) {
	if err := checkStoryboardRequest(req); err != nil {
		return nil, err
	}
	if req.Step == "compose" && len(req.Episodes) != 1 {
		return nil, errcode.ErrInvalidParams.WithMsg("合成须且只能选择一集")
	}
	switch req.Step {
	case "analyze":
		return nil, nil
	case "outline":
		if p.SourceSummary == "" {
			return nil, errcode.ErrDramaStepNotReady.WithMsg("请先完成原文研读")
		}
		return nil, nil
	case "scripts", "breakdown", "asset_prompts", "storyboard", "compose":
	default:
		return nil, errcode.ErrInvalidParams.WithMsg("不支持的流水线步骤")
	}
	if req.Step == "scripts" && p.SourceKind == "script" {
		return nil, errcode.ErrDramaStepNotReady.WithMsg("剧本型输入不能生成分集剧本")
	}
	if len(req.Episodes) > 200 {
		return nil, errcode.ErrInvalidParams.WithMsg("单次最多选择200集")
	}
	if len(eps) == 0 {
		return nil, errcode.ErrDramaStepNotReady.WithMsg("请先生成分集大纲")
	}
	return filterDramaEpisodes(eps, req)
}

// filterDramaEpisodes 校验集号和剧本状态，保持集号自然顺序。
func filterDramaEpisodes(eps []model.DramaEpisode, req *model.DramaJobReq) ([]model.DramaEpisode, error) {
	seen := map[int]bool{}
	selected := []model.DramaEpisode{}
	for _, no := range req.Episodes {
		if no < 1 || no > 100000 || seen[no] {
			return nil, errcode.ErrInvalidParams.WithMsg("集号须为1至100000且不能重复")
		}
		seen[no] = true
	}
	for _, ep := range eps {
		if len(seen) > 0 && !seen[ep.No] {
			continue
		}
		if e := checkDramaEpisodeStep(ep, req.Step); e != nil {
			return nil, e
		}
		selected = append(selected, ep)
	}
	if len(selected) > 200 {
		return nil, errcode.ErrInvalidParams.WithMsg("单次最多选择200集")
	}
	if len(selected) == 0 || len(seen) > 0 && len(selected) != len(seen) {
		return nil, errcode.ErrDramaStepNotReady.WithMsg("所选集不存在")
	}
	return selected, nil
}

// checkDramaEpisodeStep 锁定集不能重新生成正文或重新拆解。
func checkDramaEpisodeStep(ep model.DramaEpisode, step string) error {
	if step == "scripts" && strings.TrimSpace(ep.Title) == "" {
		return errcode.ErrDramaStepNotReady.WithMsg("只能选择已规划的集")
	}
	if step == "compose" && ep.LockedAt == nil {
		return errcode.ErrDramaStepNotReady.WithMsg("所选集须已拆解锁定")
	}
	if step == "scripts" && ep.LockedAt != nil {
		return errcode.ErrDramaEpisodeLocked
	}
	if step == "breakdown" && (ep.ScriptStatus != "ready" || ep.LockedAt != nil) {
		return errcode.ErrDramaStepNotReady.WithMsg("所选集须已有剧本且尚未拆解")
	}
	if step == "storyboard" && (ep.ScriptStatus != "ready" || strings.TrimSpace(ep.ScriptMD) == "" || ep.LockedAt == nil) {
		return errcode.ErrDramaStepNotReady.WithMsg("所选集须已拆解资产且剧本已就绪")
	}
	return nil
}

// dramaQuote 只保留兼容返回 0；费用由用户自己的 TokenPay 钱包在上游按实际用量结算。
func dramaQuote(_ *model.DramaProject, _ string, _ []model.DramaEpisode, _ *provider.Snapshot) int {
	return 0
}
