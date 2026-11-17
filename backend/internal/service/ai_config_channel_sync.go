package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"go.uber.org/zap"

	"video-canvas/internal/model"
	"video-canvas/internal/pkg/errcode"
	"video-canvas/internal/pkg/logger"
	"video-canvas/internal/pkg/outbound"
	"video-canvas/internal/repository"
)

// 渠道一键同步模型：拉上游 /v1/models 目录，按每个模型声明的 supported_protocols 自动选协议插件、
// 填协议设置、套用每类模型的默认能力与定价（镜像 web/src/utils/admin/model-template.ts 的新建模板），
// 创建并直接启用。为 TokenDance 这类「一个网关、四种能力」的渠道设计：运营者配好渠道后点一次同步
// 就能获得全部可用模型，不需要逐个绑插件、填设置、逐个发布。目录里没有可承接协议的模型（向量、搜索、
// OCR、只有其它厂商协议的）跳过并在结果里说明原因。

// syncModelRule 是一种上游协议 → 本站模型的映射规则。
type syncModelRule struct {
	protocol    string         // 上游 supported_protocols 里的取值
	plugin      string         // 承接的协议插件 key
	version     string         // 承接插件的固定版本（同步时解析成该插件的最新版本）
	kind        string         // 模型种类
	settings    map[string]any // 协议设置（跟随模型，钩子经 ctx.channel.settings 读取）
	deadline    string         // 任务时限（Go duration 写法）
	caps        string         // 能力默认值 JSON（与前端 defaultCapabilities 同源）
	pricing     string         // 定价默认值 JSON（与前端 defaultPricing 同源）
	contextCaps bool           // 是否把目录里的 context_length 填进能力的上下文窗口（仅文本）
}

// syncContextWindowMax 是同步时上下文窗口的上限：目录里个别模型标到百万级，超过一律截到 1M。
const syncContextWindowMax = 1_000_000

// syncModelRules 的顺序就是优先级：一个模型声明多种协议时取第一个能承接的。
var syncModelRules = []syncModelRule{
	{
		protocol: "openai:chat-completions", plugin: "openai", kind: "text",
		settings: map[string]any{"api": "chat_completions"}, // TokenDance 目录的文本模型只实现了 Chat Completions
		deadline: "5m", contextCaps: true,
		caps: `{"refs":{"image":{"on":false,"max":0,"max_mb":0},"audio":{"on":false,"max":0,"max_mb":0},"video":{"on":false,"max":0,"max_mb":0}},` +
			`"prompt":{"max_length":20000},"params":{},"context":{"window":%d,"output":8192}}`,
		pricing: `{"billing":"token","token":{"in":2,"out":8},"pending":false}`,
	},
	{
		protocol: "openai:image-generations", plugin: "openai-image", kind: "image",
		deadline: "10m",
		caps: `{"ops":["t2i","i2i"],"refs":{"image":{"on":true,"max":4,"max_mb":10},"audio":{"on":false,"max":0,"max_mb":0},"video":{"on":false,"max":0,"max_mb":0}},` +
			`"prompt":{"max_length":20000},"params":{"aspect_ratio":{"type":"enum","label":"比例","open":true,"options":["Auto","1:1","16:9","9:16","4:3","3:4"],"default":"1:1"},` +
			`"resolution":{"type":"enum","label":"清晰度","open":true,"options":["1K","2K","4K"],"default":"1K","spec":true},` +
			`"count":{"type":"enum","label":"生成数量","open":true,"options":[1,2,4],"default":1,"fanout":true,"unit":"张"}}}`,
		pricing: `{"billing":"per_call","unit":4,"pending":false,"tiers":[{"on":true,"when":{"resolution":"2K"},"unit":8},{"on":true,"when":{"resolution":"4K"},"unit":16}]}`,
	},
	{
		protocol: "seedance:generations", plugin: "tokendance-video", kind: "video",
		deadline: "30m",
		caps: `{"ops":["t2v","i2v","omni"],"refs":{"image":{"on":true,"max":4,"max_mb":10},"audio":{"on":true,"max":1,"max_mb":15},"video":{"on":true,"max":1,"max_mb":100}},` +
			`"prompt":{"max_length":20000},"params":{"aspect_ratio":{"type":"enum","label":"比例","open":true,"options":["Auto","16:9","4:3","1:1","3:4","9:16","21:9"],"default":"Auto"},` +
			`"resolution":{"type":"enum","label":"清晰度","open":true,"options":["720P","1080P","4K"],"default":"720P","spec":true},` +
			`"duration":{"type":"number","label":"视频时长","open":true,"min":5,"max":10,"step":1,"default":5,"unit":"秒"},` +
			`"generate_audio":{"type":"boolean","label":"生成音频","open":true,"default":true,"spec":true},` +
			`"count":{"type":"enum","label":"生成数量","open":true,"options":[1,2,4],"default":1,"fanout":true,"unit":"个"}}}`,
		pricing: `{"billing":"per_second","per_second":2,"pending":false,"tiers":[{"on":true,"when":{"generate_audio":true},"unit":3},` +
			`{"on":true,"when":{"resolution":"1080P"},"unit":4},{"on":true,"when":{"resolution":"1080P","generate_audio":true},"unit":5},` +
			`{"on":true,"when":{"resolution":"4K"},"unit":8}]}`,
	},
	{
		protocol: "minimax:t2a_v2", plugin: "tokendance-tts", kind: "audio",
		settings: map[string]any{"voice_id": "male-qn-qingse"}, // 插件声明的默认音色，运营可改
		deadline: "30m",
		caps: `{"refs":{"image":{"on":false,"max":0,"max_mb":0},"audio":{"on":false,"max":0,"max_mb":0},"video":{"on":false,"max":0,"max_mb":0}},` +
			`"prompt":{"max_length":20000},"params":{}}`,
		pricing: `{"billing":"per_call","unit":2,"pending":false}`,
	},
}

// SyncedModel 是同步新建（并尝试启用）的一个模型。
type SyncedModel struct {
	Key           string `json:"key"`
	UpstreamModel string `json:"upstream_model"`
	Kind          string `json:"kind"`
	Label         string `json:"label"`
	Enabled       bool   `json:"enabled"`
	EnableIssue   string `json:"enable_issue,omitempty"` // 启用检查没过时的原因（模型已创建为未启用）
}

// SkippedSyncModel 是同步时跳过的一个上游模型与原因。
type SkippedSyncModel struct {
	Model  string `json:"model"`
	Reason string `json:"reason"`
}

// SyncChannelModelsResult 是一键同步的结果：新建的模型与跳过的上游模型。
type SyncChannelModelsResult struct {
	Created []SyncedModel      `json:"created"`
	Skipped []SkippedSyncModel `json:"skipped"`
}

// syncCatalogHTTP 拉上游模型目录的出站客户端（架构约束：业务代码不自己构造 http.Client）。
var syncCatalogHTTP = outbound.Client("ai.model_catalog", &http.Client{Timeout: 20 * time.Second})

// syncCatalogEntry 是目录里的一个模型。
type syncCatalogEntry struct {
	ID                 string   `json:"id"`
	Name               string   `json:"name"`
	Description        string   `json:"description"`
	ContextLength      int      `json:"context_length"`
	SupportedProtocols []string `json:"supported_protocols"`
}

// SyncChannelModels 为渠道执行一键同步：拉目录 → 按协议映射 → 创建并启用模型。
// 渠道下已有同名上游模型的跳过；协议没有启用中的插件可承接、或目录里声明了我们不支持的协议时跳过并说明原因。
func (s *AIConfigService) SyncChannelModels(ctx context.Context, actorID uint64, channelKey string) (*SyncChannelModelsResult, error) {
	// 1. 渠道必须存在且启用，Key 已设置（目录接口用渠道 Key 鉴权）
	ch, err := s.channels.GetChannel(ctx, channelKey)
	if err != nil {
		return nil, aiNotFound(err)
	}
	if !ch.Enabled {
		return nil, errcode.ErrChannelDisabled
	}
	key, err := s.Get(ctx, model.ChannelSecretName(channelKey))
	if err != nil {
		return nil, errcode.ErrConfigInvalid.WithMsg("渠道 Key 未设置，无法拉取模型目录")
	}
	// 2. 每个承接插件的最新版本（模型固定版本用）；插件停用则该协议整体跳过
	versions := map[string]string{}
	for _, rule := range syncModelRules {
		if _, done := versions[rule.plugin]; done {
			continue
		}
		p, err := s.plugins.GetPlugin(ctx, rule.plugin)
		if err != nil || !p.Enabled {
			versions[rule.plugin] = ""
			continue
		}
		vs, err := s.plugins.ListVersions(ctx, rule.plugin)
		if err != nil || len(vs) == 0 {
			versions[rule.plugin] = ""
			continue
		}
		latest := vs[0]
		for _, v := range vs[1:] {
			if v.ID > latest.ID {
				latest = v
			}
		}
		versions[rule.plugin] = latest.Version
	}
	// 3. 拉目录
	entries, err := syncFetchCatalog(ctx, ch.BaseURL, key)
	if err != nil {
		return nil, err
	}
	// 4. 逐个映射、建模型、启用
	have, maxSort, err := s.channelModels(ctx, channelKey)
	if err != nil {
		return nil, err
	}
	haveUp := make(map[string]bool, len(have))
	for _, up := range have {
		haveUp[up] = true
	}
	ptrs, err := s.repo.ListModelPointers(ctx)
	if err != nil {
		return nil, err
	}
	taken := make(map[string]bool, len(ptrs))
	for _, p := range ptrs {
		taken[p.Key] = true
	}
	res := &SyncChannelModelsResult{Created: []SyncedModel{}, Skipped: []SkippedSyncModel{}}
	for _, entry := range entries {
		if entry.ID == "" {
			continue
		}
		rule := pickSyncRule(entry.SupportedProtocols)
		if rule == nil {
			res.Skipped = append(res.Skipped, SkippedSyncModel{Model: entry.ID, Reason: "没有可承接的协议：" + strings.Join(entry.SupportedProtocols, "、")})
			continue
		}
		version := versions[rule.plugin]
		if version == "" {
			res.Skipped = append(res.Skipped, SkippedSyncModel{Model: entry.ID, Reason: "协议插件 " + rule.plugin + " 未启用"})
			continue
		}
		rule.version = version
		if haveUp[entry.ID] {
			res.Skipped = append(res.Skipped, SkippedSyncModel{Model: entry.ID, Reason: "该渠道下已有这个上游模型"})
			continue
		}
		modelKey := importModelKey(entry.ID, channelKey, taken)
		maxSort += 10
		body, err := syncModelBody(modelKey, channelKey, entry, *rule, maxSort)
		if err != nil {
			return nil, err
		}
		// 启用检查过了才直接上线；没过就先建成未启用，把原因带回给运营
		enabled := true
		enableIssue := ""
		if _, err := s.checkEnableable(ctx, modelKey, body); err != nil {
			enabled = false
			enableIssue = err.Error()
		}
		meta, err := aiParseMeta(body)
		if err != nil {
			res.Skipped = append(res.Skipped, SkippedSyncModel{Model: entry.ID, Reason: err.Error()})
			continue
		}
		if _, err := s.repo.SaveModel(ctx, repository.SaveModelInput{
			Pointer: aiPointer(meta), Body: body, CreatedBy: actorID, Note: "从渠道一键同步",
			InitialEnabled: enabled, InitialSort: maxSort,
		}); err != nil {
			return nil, err
		}
		taken[modelKey] = true
		haveUp[entry.ID] = true
		res.Created = append(res.Created, SyncedModel{
			Key: modelKey, UpstreamModel: entry.ID, Kind: rule.kind,
			Label: syncLabel(entry), Enabled: enabled, EnableIssue: enableIssue,
		})
	}
	if len(res.Created) > 0 {
		s.notifyChanged(ctx, "sync models "+channelKey)
	}
	logger.Info("渠道一键同步模型", zap.String("channel", channelKey), zap.Uint64("admin_id", actorID),
		zap.Int("created", len(res.Created)), zap.Int("skipped", len(res.Skipped)))
	return res, nil
}

// pickSyncRule 按模型声明的协议选第一条能承接的规则；都承接不了返回 nil。
func pickSyncRule(protocols []string) *syncModelRule {
	for _, rule := range syncModelRules {
		for _, p := range protocols {
			if p == rule.protocol {
				copied := rule
				return &copied
			}
		}
	}
	return nil
}

// syncFetchCatalog 拉上游的 /v1/models 目录（渠道 Key 鉴权）。
func syncFetchCatalog(ctx context.Context, baseURL, key string) ([]syncCatalogEntry, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+"/v1/models", nil)
	if err != nil {
		return nil, errcode.ErrConfigInvalid.WithMsg("渠道地址不合法：" + err.Error())
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Accept", "application/json")
	resp, err := syncCatalogHTTP.Do(req)
	if err != nil {
		return nil, errcode.ErrConfigInvalid.WithMsg("拉取模型目录失败：" + err.Error())
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, errcode.ErrConfigInvalid.WithMsg("读取模型目录失败：" + err.Error())
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, errcode.ErrConfigInvalid.WithMsg(fmt.Sprintf("拉取模型目录失败：HTTP %d", resp.StatusCode))
	}
	var out struct {
		Data []syncCatalogEntry `json:"data"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, errcode.ErrConfigInvalid.WithMsg("模型目录不是合法的 JSON：" + err.Error())
	}
	return out.Data, nil
}

// syncModelBody 组装一个同步模型的配置正文：协议、每类默认能力与定价（能力里的上下文窗口取目录值）。
func syncModelBody(key, channelKey string, entry syncCatalogEntry, rule syncModelRule, sort int) ([]byte, error) {
	caps := rule.caps
	if rule.contextCaps {
		window := entry.ContextLength
		if window <= 0 {
			window = 128000
		}
		if window > syncContextWindowMax {
			window = syncContextWindowMax
		}
		caps = fmt.Sprintf(rule.caps, window)
	}
	body := map[string]any{
		"key": key, "kind": rule.kind, "label": syncLabel(entry), "hint": syncHint(entry),
		"enabled": true, "sort": sort, "deadline": rule.deadline,
		"channels": []map[string]any{{
			"channel": channelKey, "upstream_model": entry.ID,
			"protocol": map[string]any{"plugin": rule.plugin, "version": rule.version, "settings": rule.settings},
		}},
		"capabilities": json.RawMessage(caps),
		"pricing":      json.RawMessage(rule.pricing),
	}
	return json.Marshal(body)
}

// syncLabel 取展示名：目录的 name（如 "Z.ai: GLM 5"），没有就用模型 id。
func syncLabel(entry syncCatalogEntry) string {
	if name := strings.TrimSpace(entry.Name); name != "" {
		return name
	}
	return entry.ID
}

// syncHintMaxRunes 是同步模型描述的字数上限（hint 列约束之内，够一句定位）。
const syncHintMaxRunes = 200

// syncHint 取目录描述的前一段做模型说明。
func syncHint(entry syncCatalogEntry) string {
	hint := strings.TrimSpace(entry.Description)
	if r := []rune(hint); len(r) > syncHintMaxRunes {
		hint = string(r[:syncHintMaxRunes]) + "…"
	}
	return hint
}
