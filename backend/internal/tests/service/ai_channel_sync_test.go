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
		"openai":           []byte(`{"apiVersion":1,"key":"openai","name":"OpenAI","version":"1.0.0","auth":{"type":"bearer"},"endpoints":{"text":{"mode":"sync"}},"channelSettings":{"api":{"type":"enum","label":"接口","options":["responses","chat_completions"],"default":"responses"}}}`),
		"openai-image":     []byte(`{"apiVersion":1,"key":"openai-image","name":"OpenAI Image","version":"1.0.0","auth":{"type":"bearer"},"endpoints":{"image":{"mode":"sync"}}}`),
		"tokendance-video": []byte(`{"apiVersion":1,"key":"tokendance-video","name":"TokenDance Video","version":"1.0.0","auth":{"type":"bearer"},"endpoints":{"video":{"mode":"async"}}}`),
		"tokendance-tts":   []byte(`{"apiVersion":1,"key":"tokendance-tts","name":"TokenDance TTS","version":"1.0.0","auth":{"type":"bearer"},"endpoints":{"audio":{"mode":"sync"}},"channelSettings":{"voice_id":{"type":"string","label":"音色","default":"male-qn-qingse"}}}`),
	}
}

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
			{"id":"qwen-text-embedding-v4","name":"Qwen: Text Embedding V4","context_length":8000,"supported_protocols":["openai:embeddings"]}
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
	if err := repo.CreateChannel(context.Background(), &model.AIChannel{Key: "main", Name: "主渠道", BaseURL: catalogURL, Enabled: true}); err != nil {
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
	if len(res.Created) != 4 || len(res.Skipped) != 1 {
		t.Fatalf("应新建 4 个、跳过 1 个：%+v", res)
	}
	if res.Skipped[0].Model != "qwen-text-embedding-v4" || !strings.Contains(res.Skipped[0].Reason, "openai:embeddings") {
		t.Fatalf("跳过原因不对：%+v", res.Skipped[0])
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
	// 视频 / 图像的插件与种类
	if byUpstream["seedance-2.0"].Kind != "video" || byUpstream["seedream-5.0-lite"].Kind != "image" {
		t.Fatalf("kind 映射不对：%+v", res.Created)
	}
	if len(notifier.Reasons) != 1 {
		t.Fatalf("同步后应刷新一次 Registry：%v", notifier.Reasons)
	}
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
	if len(res.Skipped) != 5 {
		t.Fatalf("5 个上游模型都应跳过：%+v", res.Skipped)
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

// 编译期确认 AIConfigService 仍实现渠道服务依赖的接口。
var _ AIChannelModels = (*AIConfigService)(nil)
