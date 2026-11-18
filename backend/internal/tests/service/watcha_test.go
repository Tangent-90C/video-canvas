package service_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"video-canvas/internal/cache"
	"video-canvas/internal/model"
	"video-canvas/internal/pkg/errcode"
	"video-canvas/internal/pkg/outbound"
	"video-canvas/internal/pkg/utils"
	. "video-canvas/internal/service"
)

type watchaTestEnv struct {
	svc    *WatchaService
	repo   *fakeUserRepo
	flows  *cache.MemoryWatchaFlowStore
	policy *fakePolicy
}

func newWatchaTestEnv(t *testing.T, tokenStatus int) *watchaTestEnv {
	t.Helper()
	token := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("token 表单解析失败：%v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if r.Form.Get("client_id") != "client-id" || r.Form.Get("client_secret") != "client-secret" || r.Form.Get("redirect_uri") != "https://app.example.com/auth/watcha/callback" || r.Form.Get("code_verifier") == "" {
			t.Errorf("token 请求参数不完整：%v", r.Form)
		}
		if tokenStatus != http.StatusOK {
			w.WriteHeader(tokenStatus)
			_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "watcha-token", "token_type": "Bearer", "expires_in": 1800})
	}))
	t.Cleanup(token.Close)

	info := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer watcha-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"statusCode": 200, "data": map[string]any{"user_id": 12345, "nickname": "观猹用户"}})
	}))
	t.Cleanup(info.Close)

	repo := newFakeUserRepo()
	policy := &fakePolicy{enabled: true}
	users := NewUserService(UserDeps{Repo: repo, Cache: noCache(), Policy: policy, JWTSecret: testJWTSecret, JWTIssuer: "test", JWTExpireHours: 1})
	flows := cache.NewMemoryWatchaFlowStore(time.Now)
	svc := NewWatchaService(WatchaDeps{
		Repo: repo, Users: users, Flows: flows, Policy: policy,
		ClientID: "client-id", ClientSecret: "client-secret",
		RedirectURI:  "https://app.example.com/auth/watcha/callback",
		AuthorizeURL: "https://watcha.example.com/oauth/authorize",
		TokenURL:     token.URL, UserInfoURL: info.URL,
		ExchangeClient: outbound.Client("test.watcha.exchange", token.Client()),
		UserInfoClient: outbound.SensitiveClient("test.watcha.info", info.Client()),
	})
	return &watchaTestEnv{svc: svc, repo: repo, flows: flows, policy: policy}
}

func TestWatchaService_Start(t *testing.T) {
	env := newWatchaTestEnv(t, http.StatusOK)
	view, err := env.svc.Start(context.Background(), "https://evil.example.com/canvas")
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(view.AuthURL)
	if err != nil {
		t.Fatal(err)
	}
	if u.Host != "watcha.example.com" || u.Query().Get("response_type") != "code" || u.Query().Get("client_id") != "client-id" || u.Query().Get("scope") != "read" || u.Query().Get("redirect_uri") != "https://app.example.com/auth/watcha/callback" || u.Query().Get("state") == "" || u.Query().Get("code_challenge") == "" || u.Query().Get("code_challenge_method") != "S256" {
		t.Fatalf("授权页参数不对：%s", view.AuthURL)
	}
	flow, ok := env.flows.Consume(context.Background(), u.Query().Get("state"))
	if !ok || flow.Next != "/" {
		t.Fatalf("外部 next 应被拒绝并落回首页：ok=%v flow=%+v", ok, flow)
	}
}

func TestWatchaService_CallbackCreatesUser(t *testing.T) {
	env := newWatchaTestEnv(t, http.StatusOK)
	start, err := env.svc.Start(context.Background(), "/canvas/123?x=1")
	if err != nil {
		t.Fatal(err)
	}
	state := mustQuery(t, start.AuthURL).Get("state")
	view, err := env.svc.Callback(context.Background(), state, "code-1", ClientMeta{IP: "1.2.3.4", UserAgent: "ua"})
	if err != nil {
		t.Fatal(err)
	}
	if view.Next != "/canvas/123?x=1" || view.Role != model.RoleUser || view.Token == "" {
		t.Fatalf("登录响应不对：%+v", view)
	}
	claims, err := utils.ParseToken(view.Token, testJWTSecret)
	if err != nil || claims.UserID != uint(env.repo.users[0].ID) {
		t.Fatalf("JWT 不对：%+v %v", claims, err)
	}
	user := env.repo.users[0]
	if user.Username != "watcha_12345" || user.Nickname != "观猹用户" || user.Password == "" || user.LastLoginAt == nil {
		t.Fatalf("创建的用户不对：%+v", user)
	}
	if env.repo.oauth[oauthKey(model.OAuthProviderWatcha, "12345")] == nil {
		t.Fatal("应写入观猹身份绑定")
	}
	if len(env.repo.logs) != 1 || env.repo.logs[0].Kind != model.LoginKindOAuth || env.repo.logs[0].Result != model.LoginResultOK {
		t.Fatalf("登录记录不对：%+v", env.repo.logs)
	}
}

func TestWatchaService_CallbackRejectsClosedRegistrationAndBadState(t *testing.T) {
	env := newWatchaTestEnv(t, http.StatusOK)
	start, err := env.svc.Start(context.Background(), "/")
	if err != nil {
		t.Fatal(err)
	}
	state := mustQuery(t, start.AuthURL).Get("state")
	env.policy.enabled = false
	if _, err := env.svc.Callback(context.Background(), state, "code-1", ClientMeta{}); errorCode(err) != errcode.ErrRegisterClosed.Code {
		t.Fatalf("注册关闭时应拒绝新用户：%v", err)
	}
	if _, err := env.svc.Callback(context.Background(), "not-a-state", "code-2", ClientMeta{}); errorCode(err) != errcode.ErrWatchaState.Code {
		t.Fatalf("非法 state 应拒绝：%v", err)
	}
}

func TestWatchaService_CallbackUpstreamFailure(t *testing.T) {
	env := newWatchaTestEnv(t, http.StatusUnauthorized)
	start, err := env.svc.Start(context.Background(), "/")
	if err != nil {
		t.Fatal(err)
	}
	state := mustQuery(t, start.AuthURL).Get("state")
	if _, err := env.svc.Callback(context.Background(), state, "code-1", ClientMeta{}); errorCode(err) != errcode.ErrWatchaOAuthFailed.Code {
		t.Fatalf("上游失败应返回登录失败：%v", err)
	}
	if len(env.repo.users) != 0 {
		t.Fatal("上游失败不应创建用户")
	}
}

func mustQuery(t *testing.T, raw string) url.Values {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u.Query()
}

func errorCode(err error) int {
	var e *errcode.Error
	if errors.As(err, &e) {
		return e.Code
	}
	return -1
}

// 已绑定用户不受注册关闭影响；账号停用仍然拒绝登录。
func TestWatchaService_CallbackExistingUserAndDisabled(t *testing.T) {
	env := newWatchaTestEnv(t, http.StatusOK)
	user := env.repo.seed(model.User{Username: "watcha_12345", Role: model.RoleUser, Status: model.UserStatusActive})
	env.repo.oauth[oauthKey(model.OAuthProviderWatcha, "12345")] = user
	env.policy.enabled = false
	start, err := env.svc.Start(context.Background(), "/profile")
	if err != nil {
		t.Fatal(err)
	}
	view, err := env.svc.Callback(context.Background(), mustQuery(t, start.AuthURL).Get("state"), "code", ClientMeta{})
	if err != nil || view.Next != "/profile" || len(env.repo.users) != 1 {
		t.Fatalf("已绑定用户应能登录：%+v %v users=%d", view, err, len(env.repo.users))
	}

	user.Status = model.UserStatusDisabled
	start, err = env.svc.Start(context.Background(), "/")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.svc.Callback(context.Background(), mustQuery(t, start.AuthURL).Get("state"), "code", ClientMeta{}); errorCode(err) != errcode.ErrAccountDisabled.Code {
		t.Fatalf("停用账号应被拒绝：%v", err)
	}
}
