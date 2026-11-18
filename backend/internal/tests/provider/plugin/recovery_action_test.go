package plugin_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"video-canvas/internal/provider"
	"video-canvas/internal/provider/modelcfg"
	"video-canvas/internal/provider/plugin"
	"video-canvas/internal/provider/pluginmeta"
	"video-canvas/internal/provider/pluginrunner"
	"video-canvas/plugins"
)

// TokenDance 用 TokenDance-Recovery-Action 头声明确切的恢复路径（官方 OAuth 文档的硬性要求），
// 宿主按它直接定分类，优先于插件的 classifyError 与文本启发式：
//   - top_up_balance → provider_balance（钱包任务文案「余额不足，请充值」）
//   - reauthorize_api_key → secret_unavailable（钱包任务文案「请重新连接钱包」）
//   - api_key_quota → key_quota（等待额度刷新或重新授权）
//
// 现在所有真实任务都走钱包；宿主仍按响应头优先分类。
func TestTokendanceRecoveryActionHeader(t *testing.T) {
	cases := []struct {
		action string
		status int
		want   provider.ErrorClass
	}{
		{"top_up_balance", http.StatusPaymentRequired, provider.ClassProviderBalance},
		{"reauthorize_api_key", http.StatusUnauthorized, provider.ClassSecretUnavailable},
		{"api_key_quota", http.StatusBadRequest, provider.ClassKeyQuota},
	}
	for _, tc := range cases {
		t.Run(tc.action, func(t *testing.T) {
			up := &fakeNewAPI{handler: func(w http.ResponseWriter, _ *http.Request, _ map[string]any) {
				w.Header().Set("TokenDance-Recovery-Action", tc.action)
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(`{"error":{"message":"upstream says no"}}`))
			}}
			srv := httptest.NewServer(up)
			defer srv.Close()

			resolver := &walletResolver{values: map[string]string{
				"channel:tp-main": "channel-key",
				"wallet:7":        "wallet-key",
			}}
			env := newTokendanceEnv(t, resolver)
			_, err := env.exec.Submit(context.Background(), env.snapshot(srv.URL, true), provider.SubmitInput{
				Task:  provider.TaskRef{ID: 1, UserID: 7, Wallet: true},
				Input: map[string]any{"prompt": "一只猫在跳舞"},
			})
			if err == nil {
				t.Fatalf("%s 应该提交失败", tc.action)
			}
			var pe *provider.Error
			if !errors.As(err, &pe) {
				t.Fatalf("应是 *provider.Error：%v", err)
			}
			if pe.Class != tc.want {
				t.Fatalf("恢复动作 %s 应分类为 %s，实际 %s（upstream=%s）", tc.action, tc.want, pe.Class, pe.Upstream)
			}
			if pe.Upstream == "" {
				t.Fatal("应保留上游原文给用户看")
			}
		})
	}
}

// tokendance-image.js：TokenDance 图像接口的尺寸约束——至少 3686400 像素（1920x1920）。
// 面板给的比例要换算成合法尺寸；显式尺寸太小直接在本地报错，不发出去让上游 400。
func TestTokendanceImageSizeRules(t *testing.T) {
	up := &fakeNewAPI{handler: func(w http.ResponseWriter, _ *http.Request, _ map[string]any) {
		_, _ = w.Write([]byte(`{"data":[{"url":"https://cdn.example.com/a.png"}]}`))
	}}
	srv := httptest.NewServer(up)
	defer srv.Close()

	src, err := plugins.Source("tokendance-image")
	if err != nil {
		t.Fatal(err)
	}
	runner := plugin.NewInProcessRunnerClient(pluginrunner.NewServer(pluginrunner.Options{}).Handler())
	pre, err := runner.Precheck(context.Background(), string(src))
	if err != nil || !pre.OK {
		t.Fatalf("内置 tokendance-image.js 预检没有通过：%v %+v", err, pre.Issues)
	}
	meta, err := pluginmeta.Parse(pre.Meta)
	if err != nil {
		t.Fatal(err)
	}
	exec := plugin.New(plugin.Options{
		Runner:    runner,
		Codes:     codeStore{"code": string(src)},
		Secrets:   &walletResolver{values: map[string]string{"wallet:7": "wallet-key", "channel:tp-main": "product-key"}},
		Assets:    &fakeAssets{userID: 7, files: map[uint64][]byte{1: []byte("a")}},
		IPAllowed: allowLoopback,
	})
	// 能力放开常用比例，让宿主输入校验放行（对齐同步模板的 options）
	ref := modelcfg.RefSpec{On: true, Max: 9, MaxMB: 10}
	caps := modelcfg.Capabilities{Prompt: modelcfg.PromptSpec{MaxLength: 10000}}
	caps.Ops = []string{modelcfg.OpT2I, modelcfg.OpI2I}
	caps.Refs = modelcfg.Refs{Image: ref, Video: ref, Audio: ref}
	enum := modelcfg.ParamField{Type: modelcfg.ParamEnum, Label: "比例", Open: true,
		Options: []any{"Auto", "1:1", "16:9", "9:16", "4:3", "3:4"}, Default: "Auto"}
	caps.Params = modelcfg.ParamSet{{Name: "aspect_ratio", ParamField: enum}}
	snap := &provider.Snapshot{
		Model:           provider.ModelSnapshot{Key: "m", Kind: "image", UpstreamModel: "seedream-5.0-lite", Capabilities: caps},
		Channel:         provider.ChannelSnapshot{Wallet: true, Key: "tp-main", PluginKey: "tokendance-image", PluginVersionID: 1, BaseURL: srv.URL},
		Plugin:          provider.PluginSnapshot{Key: "tokendance-image", Version: meta.Version, SHA256: pre.SHA256, Meta: *meta},
		ModelRevisionID: 1,
	}

	cases := []struct {
		ratio string
		want  string
	}{
		{"16:9", "2560x1440"},
		{"9:16", "1440x2560"},
		{"1:1", "2048x2048"},
		{"Auto", "2048x2048"},
		{"4:3", "2240x1680"},
	}
	for _, tc := range cases {
		if _, err := exec.Submit(context.Background(), snap, provider.SubmitInput{
			Task:  provider.TaskRef{ID: 1, UserID: 7},
			Input: map[string]any{"prompt": "一只猫", "aspect_ratio": tc.ratio},
		}); err != nil {
			t.Fatalf("%s 提交失败：%v", tc.ratio, err)
		}
		got := up.last()
		size, _ := got.Body["size"].(string)
		if size != tc.want {
			t.Fatalf("比例 %s 应换算成 %s，实际 %s", tc.ratio, tc.want, size)
		}
		if got.Path != "/ark/v3/images/generations" {
			t.Fatalf("应走 ark 图像路径：%+v", got)
		}
	}

	// 显式尺寸低于最小像素：本地就报错，不发请求
	_, err = exec.Submit(context.Background(), snap, provider.SubmitInput{
		Task:  provider.TaskRef{ID: 1, UserID: 7},
		Input: map[string]any{"prompt": "一只猫", "size": "1024x1024"},
	})
	if err == nil {
		t.Fatal("1024x1024 小于 3686400 像素，应直接报错")
	}
}
