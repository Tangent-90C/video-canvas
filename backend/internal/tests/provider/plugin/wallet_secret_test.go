package plugin_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"video-canvas/internal/provider"
	"video-canvas/internal/provider/plugin"
	"video-canvas/internal/provider/pluginmeta"
	"video-canvas/internal/provider/pluginrunner"
	"video-canvas/plugins"
)

// walletResolver 按 secret 名字返回不同的 Key，并记录请求过的名字：钱包任务应请求 wallet:<uid>，普通任务请求 channel:<key>。
type walletResolver struct {
	mu       sync.Mutex
	values   map[string]string
	requests []string
}

func (r *walletResolver) Get(_ context.Context, name string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requests = append(r.requests, name)
	return r.values[name], nil
}

func (r *walletResolver) asked() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.requests...)
}

func (r *walletResolver) askedFor(name string) bool {
	for _, n := range r.asked() {
		if n == name {
			return true
		}
	}
	return false
}

// tokendanceEnv 用真实 runner 预检并装载内置 tokendance-video.js，凭证按名字分别注入。
type tokendanceEnv struct {
	exec *plugin.Executor
	meta *pluginmeta.Meta
	sha  string
}

func newTokendanceEnv(t *testing.T, resolver *walletResolver) *tokendanceEnv {
	t.Helper()
	src, err := plugins.Source("tokendance-video")
	if err != nil {
		t.Fatal(err)
	}
	runner := plugin.NewInProcessRunnerClient(pluginrunner.NewServer(pluginrunner.Options{}).Handler())
	pre, err := runner.Precheck(context.Background(), string(src))
	if err != nil {
		t.Fatal(err)
	}
	if !pre.OK {
		t.Fatalf("内置 tokendance-video.js 预检没有通过：%+v", pre.Issues)
	}
	meta, err := pluginmeta.Parse(pre.Meta)
	if err != nil {
		t.Fatal(err)
	}
	exec := plugin.New(plugin.Options{
		Runner:    runner,
		Codes:     codeStore{"code": string(src)},
		Secrets:   resolver,
		Assets:    &fakeAssets{userID: 7, files: map[uint64][]byte{1: []byte("a")}},
		IPAllowed: allowLoopback,
	})
	return &tokendanceEnv{exec: exec, meta: meta, sha: pre.SHA256}
}

// snapshot 组装钱包渠道快照（base_url 指向假上游）。
func (e *tokendanceEnv) snapshot(baseURL string, wallet bool) *provider.Snapshot {
	return &provider.Snapshot{
		Model: provider.ModelSnapshot{Key: "m", Kind: "video", UpstreamModel: "seedance-2.5-flash", Capabilities: doubaovideoCaps("video")},
		Channel: provider.ChannelSnapshot{
			Key: "tp-main", PluginKey: "tokendance-video", PluginVersionID: 1,
			BaseURL: baseURL, Wallet: wallet,
		},
		Plugin: provider.PluginSnapshot{
			Key: "tokendance-video", Version: e.meta.Version, SHA256: e.sha, Meta: *e.meta,
		},
		ModelRevisionID: 1,
	}
}

// 钱包任务：TaskRef.Wallet=true 时 Executor 取 wallet:<UserID> 的 Key 注入 Bearer，而不是渠道 Key；
// 凭证解析只找 wallet:<uid>，完全不碰渠道密钥。
func TestTokendanceVideoWalletUsesUserKey(t *testing.T) {
	resolver := &walletResolver{values: map[string]string{
		"channel:tp-main": "channel-key",
		"wallet:7":        "wallet-key-of-user-7",
	}}
	up := &fakeNewAPI{handler: func(w http.ResponseWriter, _ *http.Request, _ map[string]any) {
		_, _ = w.Write([]byte(`{"id":"td-1"}`))
	}}
	srv := httptest.NewServer(up)
	defer srv.Close()

	env := newTokendanceEnv(t, resolver)
	sub, err := env.exec.Submit(context.Background(), env.snapshot(srv.URL, true), provider.SubmitInput{
		Task:  provider.TaskRef{ID: 1, UserID: 7, Wallet: true},
		Input: map[string]any{"prompt": "一只猫在跳舞"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if sub.ProviderTaskID != "td-1" {
		t.Fatalf("提交结果不符：%+v", sub)
	}
	got := up.last()
	if got.Path != "/ark/v3/generations/tasks" || got.Auth != "Bearer wallet-key-of-user-7" {
		t.Fatalf("钱包任务应注入用户 Key 并走 /ark/v3 路径：%+v", got)
	}
	if resolver.askedFor("channel:tp-main") {
		t.Fatalf("钱包任务不应读渠道密钥，实际请求了 %v", resolver.asked())
	}
	if !resolver.askedFor("wallet:7") {
		t.Fatalf("应请求 wallet:7，实际：%v", resolver.asked())
	}
}

// 历史非钱包任务已经不允许再回退到产品方渠道 Key：执行前失败，且不读取任何凭证、不发上游请求。
func TestTokendanceVideoPlainFailsClosedWithoutWallet(t *testing.T) {
	resolver := &walletResolver{values: map[string]string{
		"channel:tp-main": "channel-key",
		"wallet:7":        "wallet-key-of-user-7",
	}}
	up := &fakeNewAPI{handler: func(w http.ResponseWriter, _ *http.Request, _ map[string]any) {
		_, _ = w.Write([]byte(`{"id":"td-2"}`))
	}}
	srv := httptest.NewServer(up)
	defer srv.Close()

	env := newTokendanceEnv(t, resolver)
	if _, err := env.exec.Submit(context.Background(), env.snapshot(srv.URL, false), provider.SubmitInput{
		Task:  provider.TaskRef{ID: 2, UserID: 7},
		Input: map[string]any{"prompt": "一只猫在跳舞"},
	}); err == nil {
		t.Fatal("非钱包历史任务必须失败，不能回退到渠道 Key")
	}
	if got := resolver.asked(); len(got) != 0 {
		t.Fatalf("非钱包历史任务不应读取任何凭证，实际请求了 %v", got)
	}
}
