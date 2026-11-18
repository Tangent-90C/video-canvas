package service_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"video-canvas/internal/model"
	. "video-canvas/internal/service"
	"video-canvas/internal/service/aiconfigfake"
)

// syncPluginMetas 是四个承接插件的 meta：声明 kind 端点与同步规则会用到的协议设置项。
func syncPluginMetas() map[string]json.RawMessage {
	return map[string]json.RawMessage{
		"openai":                   []byte(`{"apiVersion":1,"key":"openai","name":"OpenAI","version":"1.0.0","auth":{"type":"bearer"},"endpoints":{"text":{"mode":"sync"}},"channelSettings":{"api":{"type":"enum","label":"接口","options":["responses","chat_completions"],"default":"responses"}}}`),
		"openai-image":             []byte(`{"apiVersion":1,"key":"openai-image","name":"OpenAI Image","version":"1.0.0","auth":{"type":"bearer"},"endpoints":{"image":{"mode":"sync"}}}`),
		"tokendance-video":         []byte(`{"apiVersion":1,"key":"tokendance-video","name":"TokenDance Video","version":"1.0.0","auth":{"type":"bearer"},"endpoints":{"video":{"mode":"async"}}}`),
		"tokendance-kling-video":   []byte(`{"apiVersion":1,"key":"tokendance-kling-video","name":"TokenDance Kling Video","version":"1.0.0","auth":{"type":"bearer"},"endpoints":{"video":{"mode":"async"}},"channelSettings":{"api":{"type":"enum","label":"接口","options":["v1","v2","v1-omni","avatar","effects"],"default":"v2"}}}`),
		"tokendance-alibaba-video": []byte(`{"apiVersion":1,"key":"tokendance-alibaba-video","name":"TokenDance Alibaba Video","version":"1.0.0","auth":{"type":"bearer"},"endpoints":{"video":{"mode":"async"}},"channelSettings":{"family":{"type":"enum","label":"协议族","options":["wan3","happyhorse"],"default":"wan3"}}}`),
		"tokendance-minimax-video": []byte(`{"apiVersion":1,"key":"tokendance-minimax-video","name":"TokenDance MiniMax Video","version":"1.0.0","auth":{"type":"bearer"},"endpoints":{"video":{"mode":"async"}}}`),
		"tokendance-image":         []byte(`{"apiVersion":1,"key":"tokendance-image","name":"TokenDance Image","version":"1.0.0","auth":{"type":"bearer"},"endpoints":{"image":{"mode":"sync"}}}`),
		"tokendance-tts":           []byte(`{"apiVersion":1,"key":"tokendance-tts","name":"TokenDance TTS","version":"1.0.0","auth":{"type":"bearer"},"endpoints":{"audio":{"mode":"sync"}},"channelSettings":{"voice_id":{"type":"string","label":"音色","default":"male-qn-qingse"}}}`),
	}
}

// tokenPayMediaCatalog 是 2026-11-18 线上 /gateway/v1/models 里的图像与视频目录快照（只保留本次同步关心的字段）。
const tokenPayMediaCatalog = `{"data":[
  {"id":"seedream-5.5","name":"ByteDance Seed: Seedream 5.5","supported_protocols":["ark:image-generations"]},
  {"id":"seedream-5.0-pro","name":"ByteDance Seed: Seedream 5.0 Pro","supported_protocols":["ark:image-generations"]},
  {"id":"seedance-2.5-flash","name":"ByteDance: Seedance 2.5 Flash","supported_protocols":["seedance:generations"]},
  {"id":"seedance-2.5","name":"ByteDance: Seedance 2.5","supported_protocols":["seedance:generations"]},
  {"id":"seedance-2.0-mini","name":"ByteDance: Seedance 2.0 Mini","supported_protocols":["seedance:generations"]},
  {"id":"seedance-2.0","name":"ByteDance: Seedance 2.0","supported_protocols":["seedance:generations"]},
  {"id":"seedance-2.0-fast","name":"ByteDance: Seedance 2.0 Fast","supported_protocols":["seedance:generations"]},
  {"id":"kling-4.0-flash","name":"Kling: Video 4.0 Flash","supported_protocols":["kling:image2video-v2","kling:omni-video-v2","kling:text2video-v2"]},
  {"id":"kling-4.0","name":"Kling: Video 4.0","supported_protocols":["kling:image2video-v2","kling:omni-video-v2","kling:text2video-v2"]},
  {"id":"kling-3.0-turbo","name":"Kling: Video 3.0 Turbo","supported_protocols":["kling:image2video","kling:text2video"]},
  {"id":"kling-3.0-omni","name":"Kling: Video 3.0 Omni","supported_protocols":["kling:omni-video"]},
  {"id":"kling-3.0","name":"Kling: Video 3.0","supported_protocols":["kling:image2video","kling:motion-control","kling:text2video"]},
  {"id":"kling-avatar-image2video","name":"Kling: Kling Avatar","supported_protocols":["kling:avatar"]},
  {"id":"kling-effects","name":"Kling: Kling Video Effects","supported_protocols":["kling:video-effects"]},
  {"id":"wan3.0-video","name":"Qwen: Wan3.0","supported_protocols":["wan3:video-synthesis"]},
  {"id":"wan3.0-video-prime","name":"Qwen: Wan3.0 Prime","supported_protocols":["wan3:video-synthesis"]},
  {"id":"happyhorse-1.1-t2v","name":"Alibaba: HappyHorse 1.1 Text To Video","supported_protocols":["happyhorse:video-synthesis"]},
  {"id":"happyhorse-1.1-i2v","name":"Alibaba: HappyHorse 1.1 Image To Video","supported_protocols":["happyhorse:video-synthesis"]},
  {"id":"happyhorse-1.1-r2v","name":"Alibaba: HappyHorse 1.1 Reference To Video","supported_protocols":["happyhorse:video-synthesis"]},
  {"id":"happyhorse-1.0-t2v","name":"Alibaba: HappyHorse 1.0 Text To Video","supported_protocols":["happyhorse:video-synthesis"]},
  {"id":"happyhorse-1.0-i2v","name":"Alibaba: HappyHorse 1.0 Image To Video","supported_protocols":["happyhorse:video-synthesis"]},
  {"id":"happyhorse-1.0-r2v","name":"Alibaba: HappyHorse 1.0 Reference To Video","supported_protocols":["happyhorse:video-synthesis"]},
  {"id":"happyhorse-1.0-video-edit","name":"Alibaba: HappyHorse 1.0 Video Edit","supported_protocols":["happyhorse:video-synthesis"]},
  {"id":"minimax-h3-max","name":"MiniMax: MiniMax H3 Max","supported_protocols":["minimax:video_generation_v2"]},
  {"id":"minimax-h3","name":"MiniMax: MiniMax H3","supported_protocols":["minimax:video_generation_v2"]}
]}`

// syncCatalogServer 起一个假上游，返回 /v1/models 目录：覆盖四种可承接协议 + 一种不可承接协议。
func syncCatalogServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[
			{"id":"glm-5","name":"Z.ai: GLM 5","description":"旗舰开源模型","context_length":131072,"supported_protocols":["openai:chat-completions","anthropic:messages"]},
			{"id":"seedream-5.0-lite","name":"ByteDance: Seedream 5.0 lite","context_length":0,"supported_protocols":["ark:image-generations","openai:image-generations"]},
			{"id":"seedance-2.0","name":"ByteDance: Seedance 2.0","context_length":0,"supported_protocols":["seedance:generations"]},
			{"id":"minimax-speech-2.8-hd","name":"MiniMax: Speech-2.8-HD","context_length":0,"supported_protocols":["minimax:t2a_v2"]},
			{"id":"qwen-text-embedding-v4","name":"Qwen: Text Embedding V4","context_length":8000,"supported_protocols":["openai:embeddings"]},
			{"id":"mimo-v2.5-tts","name":"Xiaomi: MiMo-V2.5-TTS","context_length":8000,"supported_protocols":["openai:chat-completions"]}
		]}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// syncInvalidator 把 Notifier 适配成 RegistryInvalidator（AIConfigService 的热刷新依赖）。
type syncInvalidator struct{ n *aiconfigfake.Notifier }

func (s syncInvalidator) Invalidate(_ context.Context, reason string) {
	s.n.NotifyChanged(context.Background(), reason)
}

// newSyncEnv 组装同步测试环境：四个插件版本 + 指向假上游的渠道 + 渠道 Key。
func newSyncEnv(t *testing.T, catalogURL string) (*AIConfigService, *aiconfigfake.MemRepo, *aiconfigfake.Notifier) {
	t.Helper()
	repo := aiconfigfake.NewMemRepo()
	notifier := &aiconfigfake.Notifier{}
	for _, meta := range syncPluginMetas() {
		_ = adminSeedVersion(t, repo, meta, "builtin", true)
	}
	cfg := NewAIConfigService(repo, repo, repo, "test-master-key")
	cfg.SetInvalidator(syncInvalidator{notifier})
	if err := repo.CreateChannel(context.Background(), &model.AIChannel{Key: "main", Name: "主渠道", BaseURL: catalogURL, Enabled: true, WalletEnabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := cfg.SetSecret(context.Background(), model.ChannelSecretName("main"), "sk-live-123", 1); err != nil {
		t.Fatal(err)
	}
	return cfg, repo, notifier
}

// TestSyncChannelModels 一键同步：按协议映射建模型并直接启用，不支持的跳过并说明原因。
func TestSyncChannelModels(t *testing.T) {
	ctx := context.Background()
	srv := syncCatalogServer(t)
	cfg, repo, notifier := newSyncEnv(t, srv.URL)

	res, err := cfg.SyncChannelModels(ctx, 5, "main")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Created) != 4 || len(res.Skipped) != 2 {
		t.Fatalf("应新建 4 个、跳过 2 个：%+v", res)
	}
	if res.Skipped[0].Model != "qwen-text-embedding-v4" || !strings.Contains(res.Skipped[0].Reason, "openai:embeddings") {
		t.Fatalf("跳过原因不对：%+v", res.Skipped[0])
	}
	// TTS 模型在目录里标的是 chat-completions，但不是对话模型：按名字跳过
	if res.Skipped[1].Model != "mimo-v2.5-tts" || !strings.Contains(res.Skipped[1].Reason, "语音") {
		t.Fatalf("TTS 模型应被跳过：%+v", res.Skipped[1])
	}
	byUpstream := map[string]SyncedModel{}
	for _, c := range res.Created {
		if !c.Enabled || c.EnableIssue != "" {
			t.Fatalf("%s 应直接启用（issue=%s）：%+v", c.UpstreamModel, c.EnableIssue, c)
		}
		byUpstream[c.UpstreamModel] = c
	}
	// 文本：openai 插件 + api=chat_completions + 上下文窗口取目录值
	var textBody struct {
		Channels []struct {
			Protocol struct {
				Plugin   string         `json:"plugin"`
				Version  string         `json:"version"`
				Settings map[string]any `json:"settings"`
			} `json:"protocol"`
		} `json:"channels"`
		Capabilities struct {
			Context struct {
				Window int `json:"window"`
			} `json:"context"`
		} `json:"capabilities"`
	}
	body, err := repo.GetModelConfig(ctx, byUpstream["glm-5"].Key)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(body.BodyJSON, &textBody); err != nil {
		t.Fatal(err)
	}
	if textBody.Channels[0].Protocol.Plugin != "openai" ||
		textBody.Channels[0].Protocol.Settings["api"] != "chat_completions" {
		t.Fatalf("文本模型的协议映射不对：%+v", textBody.Channels[0].Protocol)
	}
	if textBody.Capabilities.Context.Window != 131072 {
		t.Fatalf("上下文窗口应取目录值 131072：%d", textBody.Capabilities.Context.Window)
	}
	if byUpstream["glm-5"].Label != "Z.ai: GLM 5" {
		t.Fatalf("展示名应取目录 name：%q", byUpstream["glm-5"].Label)
	}
	// 音频：tokendance-tts + 默认音色
	var audioBody struct {
		Channels []struct {
			Protocol struct {
				Plugin   string         `json:"plugin"`
				Settings map[string]any `json:"settings"`
			} `json:"protocol"`
		} `json:"channels"`
	}
	body, err = repo.GetModelConfig(ctx, byUpstream["minimax-speech-2.8-hd"].Key)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(body.BodyJSON, &audioBody); err != nil {
		t.Fatal(err)
	}
	if audioBody.Channels[0].Protocol.Plugin != "tokendance-tts" ||
		audioBody.Channels[0].Protocol.Settings["voice_id"] != "male-qn-qingse" {
		t.Fatalf("音频模型的协议映射不对：%+v", audioBody.Channels[0].Protocol)
	}
	// 视频 / 图像的插件与种类：图像走 tokendance-image（ark 协议，2K 起步），不是 openai-image
	if byUpstream["seedance-2.0"].Kind != "video" || byUpstream["seedream-5.0-lite"].Kind != "image" {
		t.Fatalf("kind 映射不对：%+v", res.Created)
	}
	var imageBody struct {
		Channels []struct {
			Protocol struct {
				Plugin   string         `json:"plugin"`
				Settings map[string]any `json:"settings"`
			} `json:"protocol"`
		} `json:"channels"`
		Capabilities struct {
			Params json.RawMessage `json:"params"`
		} `json:"capabilities"`
		Pricing json.RawMessage `json:"pricing"`
	}
	body, err = repo.GetModelConfig(ctx, byUpstream["seedream-5.0-lite"].Key)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(body.BodyJSON, &imageBody); err != nil {
		t.Fatal(err)
	}
	if imageBody.Channels[0].Protocol.Plugin != "tokendance-image" {
		t.Fatalf("图像模型应走 tokendance-image 插件：%+v", imageBody.Channels[0].Protocol)
	}
	// 画布面板上不该出现 resolution（ark 协议没有这个概念，给了也会被插件忽略）
	if strings.Contains(string(imageBody.Capabilities.Params), "resolution") {
		t.Fatalf("图像能力不该带 resolution 参数：%s", imageBody.Capabilities.Params)
	}
	if len(imageBody.Pricing) != 0 {
		t.Fatalf("同步正文不应再写平台积分 pricing：%s", imageBody.Pricing)
	}
	if len(notifier.Reasons) != 1 {
		t.Fatalf("同步后应刷新一次 Registry：%v", notifier.Reasons)
	}
}

// TokenPay 图像和视频目录必须全部有承接协议：Seedream 新模型不能再漏，23 个视频模型也不能只剩 Seedance。
func TestSyncChannelModels_TokenPayAllMediaFamilies(t *testing.T) {
	ctx := context.Background()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(tokenPayMediaCatalog))
	}))
	defer srv.Close()
	cfg, repo, _ := newSyncEnv(t, srv.URL)

	res, err := cfg.SyncChannelModels(ctx, 5, "main")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Created) != 25 || len(res.Skipped) != 0 {
		t.Fatalf("应新建 2 个图像 + 23 个视频模型，实际：created=%d skipped=%+v", len(res.Created), res.Skipped)
	}
	var videoCount, imageCount int
	for _, c := range res.Created {
		switch c.Kind {
		case "video":
			videoCount++
		case "image":
			imageCount++
		}
		if !c.Enabled || c.EnableIssue != "" {
			t.Fatalf("%s 应直接启用（issue=%s）：%+v", c.UpstreamModel, c.EnableIssue, c)
		}
	}
	if videoCount != 23 || imageCount != 2 {
		t.Fatalf("模型类型数量不符：video=%d image=%d", videoCount, imageCount)
	}

	type modelBody struct {
		Kind     string   `json:"kind"`
		Tags     []string `json:"tags"`
		Channels []struct {
			Protocol struct {
				Plugin   string         `json:"plugin"`
				Settings map[string]any `json:"settings"`
			} `json:"protocol"`
		} `json:"channels"`
		Capabilities struct {
			Ops          []string `json:"ops"`
			PromptFormat string   `json:"prompt_format"`
			Refs         struct {
				Image struct {
					On  bool `json:"on"`
					Max int  `json:"max"`
				} `json:"image"`
				Video struct {
					On bool `json:"on"`
				} `json:"video"`
			} `json:"refs"`
			Params map[string]struct {
				Type string `json:"type"`
			} `json:"params"`
		} `json:"capabilities"`
	}
	bodyOf := func(key string) modelBody {
		t.Helper()
		cur, err := repo.GetModelConfig(ctx, key)
		if err != nil {
			t.Fatal(err)
		}
		var body modelBody
		if err := json.Unmarshal(cur.BodyJSON, &body); err != nil {
			t.Fatal(err)
		}
		return body
	}
	keyOf := func(upstream string) string {
		for _, c := range res.Created {
			if c.UpstreamModel == upstream {
				return c.Key
			}
		}
		t.Fatalf("没有同步 %s", upstream)
		return ""
	}

	for _, upstream := range []string{"seedream-5.5", "seedream-5.0-pro"} {
		body := bodyOf(keyOf(upstream))
		if body.Kind != "image" || body.Channels[0].Protocol.Plugin != "tokendance-image" {
			t.Fatalf("%s 应由 tokendance-image 承接：%+v", upstream, body)
		}
	}

	wantPlugin := map[string]string{
		"seedance-2.5":             "tokendance-video",
		"kling-4.0":                "tokendance-kling-video",
		"kling-3.0":                "tokendance-kling-video",
		"kling-3.0-omni":           "tokendance-kling-video",
		"kling-avatar-image2video": "tokendance-kling-video",
		"kling-effects":            "tokendance-kling-video",
		"wan3.0-video":             "tokendance-alibaba-video",
		"happyhorse-1.1-r2v":       "tokendance-alibaba-video",
		"minimax-h3":               "tokendance-minimax-video",
		"minimax-h3-max":           "tokendance-minimax-video",
	}
	for upstream, plugin := range wantPlugin {
		body := bodyOf(keyOf(upstream))
		if body.Kind != "video" || body.Channels[0].Protocol.Plugin != plugin {
			t.Fatalf("%s 应由 %s 承接：%+v", upstream, plugin, body)
		}
	}

	kling4 := bodyOf(keyOf("kling-4.0"))
	if kling4.Channels[0].Protocol.Settings["api"] != "v2" || !stringListContains(kling4.Capabilities.Ops, "omni") || !kling4.Capabilities.Refs.Video.On {
		t.Fatalf("Kling 4.0 的协议设置或能力不对：%+v", kling4)
	}
	wan := bodyOf(keyOf("wan3.0-video"))
	if wan.Channels[0].Protocol.Settings["family"] != "wan3" || !stringListContains(wan.Capabilities.Ops, "omni") || wan.Capabilities.Refs.Image.Max != 10 {
		t.Fatalf("Wan3.0 的协议设置或能力不对：%+v", wan)
	}
	h3max := bodyOf(keyOf("minimax-h3-max"))
	if h3max.Capabilities.PromptFormat != "h3" || stringListContains(h3max.Capabilities.Ops, "omni") {
		t.Fatalf("MiniMax H3 Max 只支持文生和首尾帧：%+v", h3max)
	}
	effects := bodyOf(keyOf("kling-effects"))
	if effects.Capabilities.Params["effect_scene"].Type != "string" || !stringListContains(effects.Tags, "effect") {
		t.Fatalf("视频特效需要字符串模板参数并带 effect 标签：%+v", effects)
	}
	avatar := bodyOf(keyOf("kling-avatar-image2video"))
	if !stringListContains(avatar.Tags, "avatar") {
		t.Fatalf("数字人模型应带 avatar 标签：%+v", avatar)
	}
}

func stringListContains(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}

// TestSyncChannelModelsIdempotent 重复同步：已有的上游模型全部跳过，不再新建。
func TestSyncChannelModelsIdempotent(t *testing.T) {
	ctx := context.Background()
	srv := syncCatalogServer(t)
	cfg, _, _ := newSyncEnv(t, srv.URL)

	if _, err := cfg.SyncChannelModels(ctx, 5, "main"); err != nil {
		t.Fatal(err)
	}
	res, err := cfg.SyncChannelModels(ctx, 5, "main")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Created) != 0 {
		t.Fatalf("重复同步不应新建：%+v", res.Created)
	}
	if len(res.Skipped) != 6 {
		t.Fatalf("6 个上游模型都应跳过：%+v", res.Skipped)
	}
}

// TestSyncChannelModelsPreconditions 前置失败：渠道不存在 / 停用 / Key 未设置。
func TestSyncChannelModelsPreconditions(t *testing.T) {
	ctx := context.Background()
	srv := syncCatalogServer(t)
	cfg, repo, _ := newSyncEnv(t, srv.URL)

	if _, err := cfg.SyncChannelModels(ctx, 5, "nope"); err == nil {
		t.Fatal("渠道不存在应报错")
	}
	if err := repo.UpdateChannel(ctx, &model.AIChannel{Key: "main", Name: "主渠道", BaseURL: srv.URL, Enabled: false}); err != nil {
		t.Fatal(err)
	}
	if _, err := cfg.SyncChannelModels(ctx, 5, "main"); err == nil {
		t.Fatal("渠道停用应报错")
	}
	if err := repo.UpdateChannel(ctx, &model.AIChannel{Key: "main", Name: "主渠道", BaseURL: srv.URL, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	// 清掉渠道 Key：换一个没 Key 的渠道
	if err := repo.CreateChannel(ctx, &model.AIChannel{Key: "nokey", Name: "无 Key", BaseURL: srv.URL, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := cfg.SyncChannelModels(ctx, 5, "nokey"); err == nil {
		t.Fatal("渠道 Key 未设置应报错")
	}
}

// TestSyncChannelModelsPluginDisabled 插件停用时对应协议整体跳过，其余照常。
func TestSyncChannelModelsPluginDisabled(t *testing.T) {
	ctx := context.Background()
	srv := syncCatalogServer(t)
	cfg, repo, _ := newSyncEnv(t, srv.URL)
	if err := repo.SetPluginEnabled(ctx, "tokendance-video", false); err != nil {
		t.Fatal(err)
	}

	res, err := cfg.SyncChannelModels(ctx, 5, "main")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Created) != 3 {
		t.Fatalf("视频插件停用时应新建 3 个：%+v", res.Created)
	}
	found := false
	for _, s := range res.Skipped {
		if s.Model == "seedance-2.0" && strings.Contains(s.Reason, "未启用") {
			found = true
		}
	}
	if !found {
		t.Fatalf("seedance 应因插件停用被跳过：%+v", res.Skipped)
	}
}

// 目录里的多模态文本模型（如 deepseek-v4.1-flash）同步成 text 时也要带上看图能力。
func TestSyncChannelModelsTextVisionFromModelMeta(t *testing.T) {
	ctx := context.Background()
	const catalog = `{"data":[{"id":"deepseek-v4.1-flash","name":"DeepSeek V4.1 Flash","description":"原生多模态","context_length":1000000,"supported_protocols":["openai:chat-completions"]}]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(catalog))
	}))
	defer srv.Close()
	cfg, repo, _ := newSyncEnv(t, srv.URL)

	res, err := cfg.SyncChannelModels(ctx, 5, "main")
	if err != nil || len(res.Created) != 1 {
		t.Fatalf("同步失败：res=%+v err=%v", res, err)
	}
	cur, err := repo.GetModelConfig(ctx, res.Created[0].Key)
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		Kind         string `json:"kind"`
		Capabilities struct {
			Vision bool `json:"vision"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal(cur.BodyJSON, &body); err != nil {
		t.Fatal(err)
	}
	if body.Kind != "text" || !body.Capabilities.Vision {
		t.Fatalf("deepseek-v4.1-flash 应同步为 text + vision：%+v", body)
	}
}

// 编译期确认 AIConfigService 仍实现渠道服务依赖的接口。
var _ AIChannelModels = (*AIConfigService)(nil)
