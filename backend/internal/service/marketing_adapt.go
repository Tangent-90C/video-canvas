// 本文件是出片前的提示词转写：H3 这类模型有自己的写法（<Picture N> 标签、六段式），
// 原稿（Seedance 写法，代码拼出）交给文本模型转写，输出不合格时带校验反馈重写一次。
// 转写稿按格式存在脚本上，原稿指纹变了就过期，出片时自动重新转写；转写稿可以手改。
// 扣费按文本模型实际用量，和转写稿写回在同一个事务里：转写失败不扣。
package service

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"

	"video-canvas/internal/llmgateway"
	"video-canvas/internal/marketing"
	"video-canvas/internal/model"
	"video-canvas/internal/pkg/errcode"
	"video-canvas/internal/pkg/logger"
	"video-canvas/internal/provider"
	"video-canvas/internal/provider/modelcfg"
	"video-canvas/internal/repository"
)

// marketingAdaptAttemptTimeout 是每次 H3 转写尝试的最长等待。推理模型可能先流式输出数分钟思考内容，
// 由服务端 write_timeout 和前端请求超时共同兜底；两次尝试各自计时，第一次校验失败后仍有一次完整机会。
const marketingAdaptAttemptTimeout = 3 * time.Minute

// adaptSource 返回脚本的转写输入和原稿：原稿是不删减的 Seedance 写法（手改稿优先），上限取目标视频模型的提示词上限。
// 原稿手改过时镜头表以原稿为准，不再给脚本的镜头表。
func (s *MarketingService) adaptSource(c *scriptCtx, st model.MarketingSettings, video modelcfg.Capabilities) (marketing.AdaptInput, sourcePrompt, error) {
	in, src, err := s.resolveSource(c, st, video)
	if err != nil {
		return marketing.AdaptInput{}, src, err
	}
	var shots []marketing.AdaptShot
	seconds := float64(in.Duration)
	for i, sh := range in.Script.Shots {
		if !src.Edited {
			shot := marketing.AdaptShot{No: i + 1, Narration: strings.TrimSpace(sh.Narration)}
			if i > 0 {
				shot.Cut = marketing.CutTime(sh.Start)
			}
			shots = append(shots, shot)
		}
		if in.Duration <= 0 {
			seconds = sh.End
		}
	}
	return marketing.AdaptInput{Source: src.Text, Seconds: seconds, ProductImages: in.ProductImages, HasModel: in.HasModel,
		HasRefVideo: in.HasRefVideo, Shots: shots, MaxLen: video.Prompt.MaxLength}, src, nil
}

// adaptationsOf 解出脚本上的全部转写稿；空列当作没有，返回非 nil 的空表。
func adaptationsOf(sc *model.MarketingScript) (map[string]model.MarketingAdaptation, error) {
	all := map[string]model.MarketingAdaptation{}
	if len(sc.Adaptations) == 0 {
		return all, nil
	}
	if err := json.Unmarshal(sc.Adaptations, &all); err != nil {
		return nil, err
	}
	if all == nil {
		all = map[string]model.MarketingAdaptation{}
	}
	return all, nil
}

// freshAdaptation 返回脚本在某个格式下、对当前原稿仍然有效的转写稿。
func freshAdaptation(sc *model.MarketingScript, promptFormat, hash string) (string, bool) {
	all, err := adaptationsOf(sc)
	if err != nil {
		return "", false
	}
	a, ok := all[promptFormat]
	if !ok || a.SourceHash != hash {
		return "", false
	}
	return a.Prompt, true
}

// ensureAdapted 返回出片用的转写稿：已有且未过期直接用；否则（或 force）调文本模型转写并保存。
func (s *MarketingService) ensureAdapted(ctx context.Context, userID uint64, c *scriptCtx, st model.MarketingSettings,
	video *provider.Snapshot, force bool) (string, error) {
	f := video.Model.Capabilities.PromptFormat
	in, _, err := s.adaptSource(c, st, video.Model.Capabilities)
	if err != nil {
		return "", err
	}
	hash := marketing.SourceHash(in.Source)
	if prompt, ok := freshAdaptation(c.sc, f, hash); ok && !force {
		return prompt, nil
	}
	key := strconv.FormatUint(c.sc.ID, 10) + ":" + f
	if _, busy := s.adapting.LoadOrStore(key, struct{}{}); busy {
		return "", errcode.ErrMarketingScriptBusy.WithMsg("正在转写这份脚本的提示词，请稍候再试")
	}
	defer s.adapting.Delete(key)
	out, err := s.callAdapt(ctx, userID, st.TextModel, in)
	if err != nil {
		return "", err
	}
	if err := s.saveAdaptation(ctx, userID, c.sc.ID, f, model.MarketingAdaptation{Prompt: out, SourceHash: hash, UpdatedAt: s.now()}); err != nil {
		return "", err
	}
	return out, nil
}

// callAdapt 调文本模型转写，最多两次：第一次不合格时把校验原因交给模型修正。
func (s *MarketingService) callAdapt(ctx context.Context, userID uint64, textModel string, in marketing.AdaptInput) (string, error) {
	snap, err := s.pickTextModel(ctx, textModel, needAny)
	if err != nil {
		return "", err
	}
	for attempt := 1; attempt <= 2; attempt++ {
		prompt, err := marketing.AdaptPrompt(in)
		if err != nil {
			return "", err
		}
		callCtx, cancel := context.WithTimeout(ctx, marketingAdaptAttemptTimeout)
		res, err := s.caller.Complete(callCtx, snap, []llmgateway.Message{{Role: "user", Text: prompt}})
		cancel()
		if err != nil {
			// 上游返回了错误就带出原文；超时和网络断开给出可行动的说明。
			logger.Warn("营销出片转写调用失败", zap.String("model", snap.Model.Key), zap.Int("attempt", attempt), zap.Error(err))
			msg := "文本模型没有返回结果，请稍后重试"
			if errors.Is(err, context.DeadlineExceeded) {
				msg = "文本模型响应超时，请稍后重试"
			} else if up := upstreamText(err); up != "" {
				msg = up
			}
			return "", errcode.ErrMarketingAdaptFailed.WithMsg("转写提示词失败：" + msg)
		}
		out, err := marketing.DecodeAdapt(res.Text)
		if err == nil {
			if err = marketing.ValidateAdapt(in, out); err == nil {
				_ = snap
				_ = res
				return out, nil
			}
		}
		in.ValidationError = err.Error()
	}
	return "", errcode.ErrMarketingAdaptFailed
}

// adaptEstimate 兼容旧视图字段；平台积分已停用，恒为 0。
func adaptEstimate(snap *provider.Snapshot, in marketing.AdaptInput) int {
	_ = snap
	_ = in
	return 0
}

// saveAdaptation 在锁住脚本的事务里存下一份转写稿。
func (s *MarketingService) saveAdaptation(ctx context.Context, userID, scriptID uint64, promptFormat string, a model.MarketingAdaptation) error {
	return s.repo.WithTx(ctx, func(tx repository.MarketingTx) error {
		sc, err := tx.Script(ctx, userID, scriptID, true)
		if err != nil {
			return err
		}
		all, err := adaptationsOf(sc)
		if err != nil {
			return err
		}
		all[promptFormat] = a
		raw, err := json.Marshal(all)
		if err != nil {
			return err
		}
		if err := tx.UpdateScript(ctx, scriptID, map[string]any{"adaptations": raw}); err != nil {
			return err
		}
		return nil
	})
}

// adaptPromptView 组装需要转写的模型的提示词预览；兼容字段 AdaptCredits 恒为 0。
func (s *MarketingService) adaptPromptView(ctx context.Context, c *scriptCtx, video *provider.Snapshot) (*model.MarketingPrompt, error) {
	f := video.Model.Capabilities.PromptFormat
	in, src, err := s.adaptSource(c, c.st, video.Model.Capabilities)
	if err != nil {
		return nil, err
	}
	v := &model.MarketingPrompt{Format: f, Prompt: in.Source, Edited: src.Edited, Stale: src.Stale}
	all, err := adaptationsOf(c.sc)
	if err != nil {
		return nil, err
	}
	if a, ok := all[f]; ok {
		v.Adaptation = &model.MarketingAdaptationView{Prompt: a.Prompt, Stale: a.SourceHash != marketing.SourceHash(in.Source), Edited: a.Edited, UpdatedAt: a.UpdatedAt}
	}
	v.AdaptCredits = 0
	return v, nil
}

// Adapt 手动转写一份脚本的出片提示词（不传视频模型用本轮的），已有转写稿也重新转写。
// 模型不需要转写 10001；脚本重写中 68006；转写失败 68018。
func (s *MarketingService) Adapt(ctx context.Context, userID, id uint64, req *model.AdaptMarketingScriptReq) (*model.MarketingPrompt, error) {
	if err := s.ensureWallet(ctx, userID); err != nil {
		return nil, err
	}
	c, err := s.loadScript(ctx, userID, id)
	if err != nil {
		return nil, err
	}
	video, err := s.promptVideo(ctx, c, req.VideoModel)
	if err != nil {
		return nil, err
	}
	if !marketing.NeedsAdapt(video.Model.Capabilities.PromptFormat) {
		return nil, errcode.ErrInvalidParams.WithMsg("这个视频模型直接用原稿出片，不需要转写")
	}
	if c.sc.Status == model.MarketingScriptRewriting {
		return nil, errcode.ErrMarketingScriptBusy
	}
	if _, err := s.ensureAdapted(ctx, userID, c, c.st, video, true); err != nil {
		return nil, err
	}
	return s.Prompt(ctx, userID, id, req.VideoModel)
}

// EditAdaptation 手改一份已有的转写稿：标记 edited，指纹不变（过期与否仍按原稿判断，过期后出片会重新转写）。
func (s *MarketingService) EditAdaptation(ctx context.Context, userID, id uint64, req *model.EditMarketingAdaptationReq) (*model.MarketingPrompt, error) {
	err := s.repo.WithTx(ctx, func(tx repository.MarketingTx) error {
		sc, err := tx.Script(ctx, userID, id, true)
		if mktNotFound(err) {
			return errcode.ErrMarketingNotFound
		}
		if err != nil {
			return err
		}
		all, err := adaptationsOf(sc)
		if err != nil {
			return err
		}
		a, ok := all[req.Format]
		if !ok {
			return errcode.ErrInvalidParams.WithMsg("还没有转写稿，不能修改")
		}
		a.Prompt, a.Edited, a.UpdatedAt = strings.TrimSpace(req.Prompt), true, time.Now()
		all[req.Format] = a
		raw, err := json.Marshal(all)
		if err != nil {
			return err
		}
		return tx.UpdateScript(ctx, id, map[string]any{"adaptations": raw})
	})
	if err != nil {
		return nil, err
	}
	return s.Prompt(ctx, userID, id, "")
}

// promptVideo 取预览 / 转写用的视频模型快照：传了就用传的，否则用本轮的。
func (s *MarketingService) promptVideo(ctx context.Context, c *scriptCtx, videoModel string) (*provider.Snapshot, error) {
	key := videoModel
	if key == "" {
		key = c.st.VideoModel
	}
	return s.videoSnapshot(ctx, key)
}

// adaptQuote 是视图里预估出片前转写费用用的：本轮视频模型需要转写时才有，带上文本模型和视频模型能力。
type adaptQuote struct {
	text  *provider.Snapshot
	video modelcfg.Capabilities
}

// adaptQuoteFor 返回本轮的转写报价依据；不需要转写、模型不可用或没有文本模型时为 nil（报价不含转写）。
func (s *MarketingService) adaptQuoteFor(ctx context.Context, st model.MarketingSettings) *adaptQuote {
	video, err := s.models.Snapshot(ctx, st.VideoModel)
	if err != nil || video == nil || !marketing.NeedsAdapt(video.Model.Capabilities.PromptFormat) {
		return nil
	}
	text, err := s.pickTextModel(ctx, st.TextModel, needAny)
	if err != nil {
		return nil
	}
	return &adaptQuote{text: text, video: video.Model.Capabilities}
}

// adaptExtra 是一份脚本出片前还要付的转写费：已有未过期的转写稿为 0。
func (s *MarketingService) adaptExtra(q *adaptQuote, c *scriptCtx) int {
	if q == nil {
		return 0
	}
	in, _, err := s.adaptSource(c, c.st, q.video)
	if err != nil {
		return 0
	}
	if _, ok := freshAdaptation(c.sc, q.video.PromptFormat, marketing.SourceHash(in.Source)); ok {
		return 0
	}
	return adaptEstimate(q.text, in)
}
