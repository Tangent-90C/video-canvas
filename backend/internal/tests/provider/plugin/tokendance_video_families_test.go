package plugin_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"video-canvas/internal/provider"
	"video-canvas/internal/provider/modelcfg"
	"video-canvas/internal/provider/plugin"
	"video-canvas/internal/provider/pluginmeta"
	"video-canvas/internal/provider/pluginrunner"
	"video-canvas/plugins"
)

type tokenPayFamilyEnv struct {
	exec   *plugin.Executor
	meta   *pluginmeta.Meta
	sha    string
	assets *fakeAssets
}

func newTokenPayFamilyEnv(t *testing.T, key string) *tokenPayFamilyEnv {
	t.Helper()
	src, err := plugins.Source(key)
	if err != nil {
		t.Fatal(err)
	}
	code := string(src)
	runner := plugin.NewInProcessRunnerClient(pluginrunner.NewServer(pluginrunner.Options{}).Handler())
	pre, err := runner.Precheck(context.Background(), code)
	if err != nil {
		t.Fatal(err)
	}
	if !pre.OK {
		t.Fatalf("%s 预检没有通过：%+v", key, pre.Issues)
	}
	meta, err := pluginmeta.Parse(pre.Meta)
	if err != nil {
		t.Fatal(err)
	}
	resolver := &walletResolver{values: map[string]string{"wallet:9": "td-user-key", "channel:tp-main": "product-key"}}
	assets := &fakeAssets{userID: 9, files: map[uint64][]byte{1: []byte("image-1"), 2: []byte("image-2"), 3: []byte("video-1"), 4: []byte("audio-1")}}
	exec := plugin.New(plugin.Options{
		Runner: runner, Codes: codeStore{"code": code}, Secrets: resolver,
		Assets:    assets,
		IPAllowed: allowLoopback,
	})
	return &tokenPayFamilyEnv{exec: exec, meta: meta, sha: pre.SHA256, assets: assets}
}

func (e *tokenPayFamilyEnv) snapshot(baseURL, key, upstream string, settings map[string]any, caps modelcfg.Capabilities) *provider.Snapshot {
	return &provider.Snapshot{
		Model: provider.ModelSnapshot{Key: "m", Kind: "video", UpstreamModel: upstream, Capabilities: caps},
		Channel: provider.ChannelSnapshot{
			Key: "tp-main", PluginKey: key, PluginVersionID: 1, BaseURL: baseURL, Wallet: true, Settings: settings,
		},
		Plugin:          provider.PluginSnapshot{Key: key, Version: e.meta.Version, SHA256: e.sha, Meta: *e.meta},
		ModelRevisionID: 1,
	}
}

func tokenPayVideoCaps() modelcfg.Capabilities {
	ref := modelcfg.RefSpec{On: true, Max: 9, MaxMB: 30}
	return modelcfg.Capabilities{
		Ops:    []string{modelcfg.OpT2V, modelcfg.OpI2V, modelcfg.OpOmni},
		Refs:   modelcfg.Refs{Image: ref, Video: ref, Audio: modelcfg.RefSpec{On: true, Max: 3, MaxMB: 20}},
		Prompt: modelcfg.PromptSpec{MaxLength: 10000},
		Params: modelcfg.ParamSet{
			{Name: "aspect_ratio", ParamField: modelcfg.ParamField{Type: modelcfg.ParamEnum, Label: "比例", Open: true, Options: []any{"16:9", "9:16"}, Default: "16:9"}},
			{Name: "resolution", ParamField: modelcfg.ParamField{Type: modelcfg.ParamEnum, Label: "清晰度", Open: true, Options: []any{"720P", "1080P", "768P", "2K"}, Default: "720P"}},
			{Name: "duration", ParamField: modelcfg.ParamField{Type: modelcfg.ParamNumber, Label: "时长", Open: true, Min: intPtr(3), Max: intPtr(30), Default: 5}},
			{Name: "generate_audio", ParamField: modelcfg.ParamField{Type: modelcfg.ParamBoolean, Label: "音频", Open: true, Default: true}},
		},
	}
}

func intPtr(v int) *int { return &v }

func TestTokendanceKlingV2OmniRequestAndQuery(t *testing.T) {
	env := newTokenPayFamilyEnv(t, "tokendance-kling-video")
	env.assets.width, env.assets.height = 256, 256
	small := imagePNG(t, 256, 256)
	env.assets.files[1], env.assets.files[2] = small, small
	var submit map[string]any
	var paths []string
	up := &fakeNewAPI{handler: func(w http.ResponseWriter, r *http.Request, body map[string]any) {
		paths = append(paths, r.URL.Path)
		if r.Method == http.MethodPost {
			submit = body
			_, _ = w.Write([]byte(`{"code":0,"data":{"id":"kling-1"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"code":0,"data":{"status":"succeeded","outputs":[{"type":"video","url":"https://cdn.example.com/out.mp4"}]}}`))
	}}
	srv := httptest.NewServer(up)
	defer srv.Close()
	snap := env.snapshot(srv.URL, "tokendance-kling-video", "kling-4.0", map[string]any{"api": "v2"}, tokenPayVideoCaps())
	sub, err := env.exec.Submit(context.Background(), snap, provider.SubmitInput{
		Task: provider.TaskRef{ID: 1, UserID: 9, Wallet: true},
		Input: map[string]any{
			"prompt": "参考人物和动作生成视频", "op": "omni", "images": []uint64{1, 2}, "videos": []uint64{3},
			"aspect_ratio": "16:9", "resolution": "1080P", "duration": 8, "generate_audio": true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if sub.ProviderTaskID != "kling-1" || sub.State == nil {
		t.Fatalf("提交结果不对：%+v", sub)
	}
	if submit["model_name"] != "kling-4.0" {
		t.Fatalf("model_name 不对：%v", submit)
	}
	contents := submit["contents"].([]any)
	if len(contents) != 4 || contents[1].(map[string]any)["type"] != "refer_image" || contents[3].(map[string]any)["type"] != "feature_video" {
		t.Fatalf("Kling omni contents 不对：%v", contents)
	}
	if imageURL, _ := contents[1].(map[string]any)["url"].(string); imageURL == "" || strings.HasPrefix(imageURL, "https://") {
		t.Fatalf("256px 参考图应由宿主放大并以 base64 传入：%v", contents[1])
	}
	settings := submit["settings"].(map[string]any)
	if settings["resolution"] != "1080p" || settings["audio"] != "native" || settings["duration"] != float64(8) {
		t.Fatalf("Kling settings 不对：%v", settings)
	}
	q, err := env.exec.Query(context.Background(), snap, provider.TaskRef{ID: 1, UserID: 9, Wallet: true, ProviderTaskID: sub.ProviderTaskID, State: sub.State})
	if err != nil {
		t.Fatal(err)
	}
	if q.Status != provider.StatusSucceeded || len(q.Outputs) != 1 || q.Outputs[0].URL != "https://cdn.example.com/out.mp4" {
		t.Fatalf("查询结果不对：%+v", q)
	}
	if len(paths) != 2 || paths[0] != "/kling/v2/omni-video" || paths[1] != "/kling/v2/omni-video/kling-1" {
		t.Fatalf("Kling 请求路径不对：%v", paths)
	}
}

func TestTokendanceAlibabaWan3AndHappyHorse(t *testing.T) {
	env := newTokenPayFamilyEnv(t, "tokendance-alibaba-video")
	for _, tc := range []struct {
		name, model, family, wantPath, wantTask string
	}{
		{"Wan3", "wan3.0-video", "wan3", "/alibaba/wan3/v1/video-synthesis", "w-1"},
		{"HappyHorse", "happyhorse-1.1-r2v", "happyhorse", "/alibaba/happyhorse/v1/video-synthesis", "h-1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var submit map[string]any
			var paths []string
			up := &fakeNewAPI{handler: func(w http.ResponseWriter, r *http.Request, body map[string]any) {
				paths = append(paths, r.URL.Path)
				if r.Method == http.MethodPost {
					if tc.family == "happyhorse" && r.Header.Get("X-DashScope-Async") != "enable" {
						t.Errorf("HappyHorse 提交缺少 X-DashScope-Async: enable")
					}
					submit = body
					_, _ = w.Write([]byte(`{"output":{"task_id":"` + tc.wantTask + `"}}`))
					return
				}
				_, _ = w.Write([]byte(`{"output":{"task_status":"SUCCEEDED","video_url":"https://cdn.example.com/out.mp4"}}`))
			}}
			srv := httptest.NewServer(up)
			defer srv.Close()
			snap := env.snapshot(srv.URL, "tokendance-alibaba-video", tc.model, map[string]any{"family": tc.family}, tokenPayVideoCaps())
			sub, err := env.exec.Submit(context.Background(), snap, provider.SubmitInput{
				Task:  provider.TaskRef{ID: 1, UserID: 9, Wallet: true},
				Input: map[string]any{"prompt": "商品视频", "op": "omni", "images": []uint64{1, 2}, "videos": []uint64{3}, "resolution": "720P", "duration": 5, "generate_audio": true},
			})
			if err != nil {
				t.Fatal(err)
			}
			if paths[0] != tc.wantPath || submit["model"] != tc.model {
				t.Fatalf("提交路径或模型不对：paths=%v body=%v", paths, submit)
			}
			media := submit["input"].(map[string]any)["media"].([]any)
			if len(media) == 0 || media[0].(map[string]any)["type"] != "reference_image" {
				t.Fatalf("参考素材不对：%v", media)
			}
			q, err := env.exec.Query(context.Background(), snap, provider.TaskRef{ID: 1, UserID: 9, Wallet: true, ProviderTaskID: sub.ProviderTaskID, State: sub.State})
			if err != nil || q.Status != provider.StatusSucceeded {
				t.Fatalf("查询失败：%+v %v", q, err)
			}
		})
	}
}

func TestTokendanceMiniMaxV2RequestAndQuery(t *testing.T) {
	env := newTokenPayFamilyEnv(t, "tokendance-minimax-video")
	var submit map[string]any
	var paths []string
	up := &fakeNewAPI{handler: func(w http.ResponseWriter, r *http.Request, body map[string]any) {
		paths = append(paths, r.URL.Path)
		if r.Method == http.MethodPost {
			submit = body
			_, _ = w.Write([]byte(`{"task_id":"mm-1"}`))
			return
		}
		_, _ = w.Write([]byte(`{"task":{"status":"succeeded","content":{"url":"https://cdn.example.com/mm.mp4"}}}`))
	}}
	srv := httptest.NewServer(up)
	defer srv.Close()
	snap := env.snapshot(srv.URL, "tokendance-minimax-video", "minimax-h3", nil, tokenPayVideoCaps())
	sub, err := env.exec.Submit(context.Background(), snap, provider.SubmitInput{
		Task:  provider.TaskRef{ID: 1, UserID: 9, Wallet: true},
		Input: map[string]any{"prompt": "参考人物和动作", "op": "omni", "images": []uint64{1}, "videos": []uint64{3}, "audios": []uint64{4}, "resolution": "768P", "duration": 6},
	})
	if err != nil {
		t.Fatal(err)
	}
	content := submit["content"].([]any)
	if len(content) != 4 || content[1].(map[string]any)["role"] != "reference_image" || content[2].(map[string]any)["role"] != "reference_video" {
		t.Fatalf("MiniMax content 不对：%v", content)
	}
	q, err := env.exec.Query(context.Background(), snap, provider.TaskRef{ID: 1, UserID: 9, Wallet: true, ProviderTaskID: sub.ProviderTaskID, State: sub.State})
	if err != nil || q.Status != provider.StatusSucceeded || q.Outputs[0].URL != "https://cdn.example.com/mm.mp4" {
		t.Fatalf("MiniMax 查询不对：%+v %v", q, err)
	}
	if paths[0] != "/minimax/v2/video_generation" || paths[1] != "/minimax/v2/query/video_generation/mm-1" {
		t.Fatalf("MiniMax 请求路径不对：%v", paths)
	}
}

func TestTokendanceKlingEffectsUsesStringTemplate(t *testing.T) {
	env := newTokenPayFamilyEnv(t, "tokendance-kling-video")
	var submit map[string]any
	up := &fakeNewAPI{handler: func(w http.ResponseWriter, _ *http.Request, body map[string]any) {
		submit = body
		_, _ = w.Write([]byte(`{"code":0,"data":{"task_id":"effect-1"}}`))
	}}
	srv := httptest.NewServer(up)
	defer srv.Close()
	caps := tokenPayVideoCaps()
	caps.Ops = []string{modelcfg.OpI2V}
	caps.Params = modelcfg.ParamSet{{Name: "effect_scene", ParamField: modelcfg.ParamField{Type: modelcfg.ParamString, Label: "模板", Open: true, Default: "color_mixing"}}}
	snap := env.snapshot(srv.URL, "tokendance-kling-video", "kling-effects", map[string]any{"api": "effects"}, caps)
	_, err := env.exec.Submit(context.Background(), snap, provider.SubmitInput{
		Task:  provider.TaskRef{ID: 1, UserID: 9, Wallet: true},
		Input: map[string]any{"prompt": "特效", "op": "i2v", "images": []uint64{1}, "effect_scene": "hug_pro"},
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(submit)
	if submit["effect_scene"] != "hug_pro" || submit["input"].(map[string]any)["image"] == nil {
		t.Fatalf("视频特效请求不对：%s", raw)
	}
}
