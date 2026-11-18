package service

import (
	"context"
	"time"
	"unicode/utf8"

	"go.uber.org/zap"

	"video-canvas/internal/drama"
	"video-canvas/internal/llmgateway"
	"video-canvas/internal/model"
	"video-canvas/internal/pkg/errcode"
	"video-canvas/internal/pkg/logger"
	"video-canvas/internal/provider"
)

// dramaAdaptTimeout 转写一个片段（含重写一次）的最长等待；须小于服务端 write_timeout，否则响应会被连接超时截断。
// 超时按失败处理，用户可以重试。
const dramaAdaptTimeout = 50 * time.Second

// AdaptSegment 用项目文本模型把片段原稿转写成 format 写法，输出不合格时带校验反馈重写一次。
// 费用由用户 TokenPay 钱包在上游直接结算，平台返回的积分兼容字段恒为 0。
func (s *DramaService) AdaptSegment(ctx context.Context, p *model.DramaProject, format string, in drama.AdaptInput) (string, int, error) {
	if err := s.ensureWallet(ctx, p.UserID); err != nil {
		return "", 0, err
	}
	// 1. 跟随项目文本模型；选的模型停用时回落到第一个可用的 Agent 模型（与流水线一致）
	snap, err := s.resolveModel(ctx, p.TextModel)
	if err != nil {
		return "", 0, err
	}
	in.Style = s.dramaVariables(p)["style"].(string)
	callCtx, cancel := context.WithTimeout(ctx, dramaAdaptTimeout)
	defer cancel()
	// 3. 最多两次：第一次不合格时把校验原因交给模型修正，仍不合格才报错；不合格的那次不收费
	for try := 1; try <= 2; try++ {
		prompt, err := drama.AdaptPrompt(format, in)
		if err != nil {
			return "", 0, errcode.ErrDramaAdaptFormat
		}
		chars := utf8.RuneCountInString(prompt)
		if chars > dramaContextLimit(snap) {
			return "", 0, errcode.ErrDramaContext
		}
		tryCtx, callID := logger.WithCall(callCtx, "drama.adapt", zap.Int("try", try), zap.String("format", format), zap.String("model_key", snap.Model.Key))
		logger.RunOf(ctx).NoteCall(callID)
		res, err := s.streamText(tryCtx, p.UserID, snap, prompt, func(c context.Context) error { return c.Err() })
		if err != nil {
			// 上游返回了错误就带出原文；网络断开、超时这类不把内部错误透给前端，用统一文案。原始错误留给日志（运行收尾行）
			msg := "文本模型没有返回结果，请稍后重试"
			if up := upstreamText(err); up != "" {
				msg = up
			}
			cause := &dramaCallFailure{err: err}
			return "", 0, withCause(errcode.ErrDramaAdaptFailed.WithMsg("转写失败："+msg), cause, dramaErrorType(cause))
		}
		decoded, err := drama.DecodeOutput("adapt", []byte(res.Text))
		if err == nil {
			out := decoded.(drama.AdaptOutput).Prompt
			if err = drama.ValidateAdapt(format, in.Source, out, in.Seconds); err == nil {
				return out, adaptCost(snap, chars, res), nil
			}
		}
		logger.Ctx(ctx).Warn("片段转写输出不合格", logger.Event(logger.EventRunStep), zap.String("call_id", callID), zap.Int("try", try),
			zap.String("error_type", "invalid_output"), zap.String("validation_error", err.Error()))
		in.ValidationError = err.Error()
	}
	return "", 0, errcode.ErrDramaAdaptFailed
}

// adaptCost 只保留兼容返回 0；费用已由用户 TokenPay 钱包在上游结算。
func adaptCost(_ *provider.Snapshot, _ int, _ *llmgateway.Result) int {
	return 0
}
