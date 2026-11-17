package service_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"video-canvas/internal/cache"
	"video-canvas/internal/model"
	"video-canvas/internal/pkg/errcode"
	"video-canvas/internal/repository"
	. "video-canvas/internal/service"
)

// fakeWalletRepo 实现 service.UserWalletRepo：只认自己保存的 user_id。
type fakeWalletRepo struct {
	row      *model.UserWallet
	upserted *model.UserWallet
	deleted  uint64
}

func (r *fakeWalletRepo) GetByUser(_ context.Context, userID uint64) (*model.UserWallet, error) {
	if r.row == nil || r.row.UserID != userID {
		return nil, repository.ErrNotFound
	}
	return r.row, nil
}

func (r *fakeWalletRepo) Upsert(_ context.Context, w *model.UserWallet) error {
	cp := *w
	r.upserted = &cp
	r.row = &cp
	return nil
}

func (r *fakeWalletRepo) Delete(_ context.Context, userID uint64) error {
	if r.row == nil {
		return repository.ErrNotFound
	}
	r.deleted = userID
	r.row = nil
	return nil
}

// fakeWalletSecrets 实现 service.WalletSecrets，记录保存的名字与值。
type fakeWalletSecrets struct {
	names   []string
	saved   map[string]string
	forgots []string
}

func (s *fakeWalletSecrets) SetSecret(_ context.Context, name, value string, _ uint64) error {
	s.names = append(s.names, name)
	s.saved[name] = value
	return nil
}

func (s *fakeWalletSecrets) ForgetSecret(name string) { s.forgots = append(s.forgots, name) }

func (s *fakeWalletSecrets) Get(_ context.Context, name string) (string, error) {
	if v, ok := s.saved[name]; ok {
		return v, nil
	}
	return "", errors.New("secret not found: " + name)
}

// fakeFlows 实现 cache.OAuthFlowStore：固定一个发起用户（userID），行为同真实实现——state 匹配 + 发起人匹配才通过，消费即销毁。
type fakeFlows struct {
	userID   uint64
	verifier string
	state    string
	used     bool
}

func (f *fakeFlows) Issue(_ context.Context, verifier string, userID uint64, _ time.Duration) (string, error) {
	f.userID, f.verifier = userID, verifier
	f.state = "state-" + verifier[:8]
	f.used = false
	return f.state, nil
}

func (f *fakeFlows) Consume(_ context.Context, state string, uid uint64) (cache.OAuthFlow, bool) {
	if f.used || state != f.state || uid != f.userID {
		return cache.OAuthFlow{}, false
	}
	f.used = true
	return cache.OAuthFlow{Verifier: f.verifier, UserID: f.userID}, true
}

// newWalletService 组装一个完整的服务；httptest 上游返回固定 Key。
func newWalletService(t *testing.T, upstreamKey string) (*UserWalletService, *fakeWalletRepo, *fakeWalletSecrets, *fakeFlows) {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"key": upstreamKey})
	}))
	t.Cleanup(upstream.Close)
	repo := &fakeWalletRepo{}
	secrets := &fakeWalletSecrets{saved: map[string]string{}}
	flows := &fakeFlows{}
	svc := NewUserWalletService(UserWalletDeps{
		Repo:        repo,
		Secrets:     secrets,
		Flows:       flows,
		AppURL:      "https://app.example.com/",
		KeyName:     "连镜",
		ExchangeURL: upstream.URL,
	})
	return svc, repo, secrets, flows
}

// fakeWalletPricing 实现 WalletPricing：1 元 = N 积分。
type fakeWalletPricing struct{ creditsPerYuan *float64 }

func (p fakeWalletPricing) Get(_ context.Context) (*model.PricingSettingsView, error) {
	return &model.PricingSettingsView{CreditsPerYuan: p.creditsPerYuan}, nil
}

// newBalanceService 组装带余额上游的服务：balance 上游返回固定余额，pricing 可注入。
func newBalanceService(t *testing.T, credits, used int64, creditsPerYuan *float64) (*UserWalletService, *fakeWalletRepo) {
	t.Helper()
	balance := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tk-live" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"balance": map[string]any{"credits": credits + used, "credits_used": used, "balance": credits},
		})
	}))
	t.Cleanup(balance.Close)
	repo := &fakeWalletRepo{}
	secrets := &fakeWalletSecrets{saved: map[string]string{model.WalletSecretName(7): "tk-live"}}
	svc := NewUserWalletService(UserWalletDeps{
		Repo:       repo,
		Secrets:    secrets,
		Flows:      &fakeFlows{},
		AppURL:     "https://app.example.com",
		KeyName:    "连镜",
		BalanceURL: balance.URL,
		Pricing:    fakeWalletPricing{creditsPerYuan: creditsPerYuan},
	})
	repo.row = &model.UserWallet{UserID: 7, Provider: "tokendance", AppURL: "https://app.example.com", KeyMasked: "tk-l…"}
	return svc, repo
}

// Balance：已连接时查上游余额（微元→元），汇率来自定价设置。
func TestUserWalletBalance(t *testing.T) {
	ctx := context.Background()
	rate := 200.0
	svc, _ := newBalanceService(t, 12_500_000, 3_000_000, &rate) // 12.5 元剩、3 元已用

	view, err := svc.Balance(ctx, 7)
	if err != nil {
		t.Fatal(err)
	}
	if !view.Enabled || !view.Connected {
		t.Fatalf("应已连接：%+v", view)
	}
	if view.BalanceYuan != 12.5 || view.UsedYuan != 3 {
		t.Fatalf("余额换算不对：%+v", view)
	}
	if view.YuanPerCredit != 1.0/200 || view.Stale {
		t.Fatalf("汇率 / stale 不对：%+v", view)
	}

	// 60 秒内第二次走缓存：上游计数不再增长（换个余额也不变）
	view2, err := svc.Balance(ctx, 7)
	if err != nil {
		t.Fatal(err)
	}
	if view2.BalanceYuan != view.BalanceYuan {
		t.Fatalf("应命中缓存：%+v", view2)
	}
}

// Balance 的降级：未启用、未连接、上游挂、Key 读不到——都不报错，页面照常渲染。
func TestUserWalletBalanceFallbacks(t *testing.T) {
	ctx := context.Background()
	rate := 100.0

	// 未连接（仓储无行）
	svc, _ := newBalanceService(t, 1, 0, &rate)
	view, err := svc.Balance(ctx, 9)
	if err != nil || !view.Enabled || view.Connected {
		t.Fatalf("未连接应降级：%+v err=%v", view, err)
	}

	// 上游 500：stale + 零值，不报错
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer broken.Close()
	repo := &fakeWalletRepo{row: &model.UserWallet{UserID: 7, Provider: "tokendance"}}
	secrets := &fakeWalletSecrets{saved: map[string]string{model.WalletSecretName(7): "tk-live"}}
	svc2 := NewUserWalletService(UserWalletDeps{
		Repo: repo, Secrets: secrets, Flows: &fakeFlows{}, AppURL: "https://app.example.com",
		BalanceURL: broken.URL, Pricing: fakeWalletPricing{creditsPerYuan: &rate},
	})
	view, err = svc2.Balance(ctx, 7)
	if err != nil || !view.Connected || !view.Stale {
		t.Fatalf("上游失败应 stale 降级：%+v err=%v", view, err)
	}

	// Key 读不到（secrets 里没有）：已连接 + stale
	noKey := &fakeWalletSecrets{saved: map[string]string{}}
	svc3 := NewUserWalletService(UserWalletDeps{
		Repo: repo, Secrets: noKey, Flows: &fakeFlows{}, AppURL: "https://app.example.com",
		BalanceURL: broken.URL, Pricing: fakeWalletPricing{creditsPerYuan: &rate},
	})
	view, err = svc3.Balance(ctx, 7)
	if err != nil || !view.Connected || !view.Stale {
		t.Fatalf("Key 读不到应 stale 降级：%+v err=%v", view, err)
	}
}

// Start 生成的授权地址带齐 PKCE 与归因参数：challenge = base64url(sha256(verifier))，回调落到 <app_url>/wallet/tokenpay/callback。
func TestUserWalletStartBuildsAuthURL(t *testing.T) {
	svc, _, _, flows := newWalletService(t, "tk-x")
	authURL, err := svc.Start(context.Background(), 1, "/profile?tab=wallet")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(authURL, "https://tokendance.space/auth?") {
		t.Fatalf("授权地址不对：%s", authURL)
	}
	for _, want := range []string{
		"code_challenge_method=S256",
		"app_url=https%3A%2F%2Fapp.example.com",
		"key_name=%E8%BF%9E%E9%95%9C", // 连镜
		// next 在 callback_url 内编码一次后，随 callback_url 作为外层参数再编码一次（正确：上游原样回跳，浏览器解码一层即是单层）
		// next 在 callback_url 值里，连 "=" 一起被编码成 %3D
		"next%3D%252Fprofile%253Ftab%253Dwallet",
	} {
		if !strings.Contains(authURL, want) {
			t.Fatalf("授权地址缺少 %s：%s", want, authURL)
		}
	}
	// 回调地址：flow 作为已有查询参数保留，code 由上游追加
	if !strings.Contains(authURL, "callback_url=https%3A%2F%2Fapp.example.com%2Fwallet%2Ftokenpay%2Fcallback%3Fflow%3D") {
		t.Fatalf("回调地址不对：%s", authURL)
	}
	// challenge 与 fakeFlows 记下的 verifier 对得上（S256）
	sum := sha256.Sum256([]byte(flows.verifier))
	if !strings.Contains(authURL, "code_challenge="+base64.RawURLEncoding.EncodeToString(sum[:])) {
		t.Fatalf("challenge 与 verifier 不匹配：%s", authURL)
	}
}

// Connect 的完整链路：换 Key → 加密入库（wallet:<uid>）→ 元数据落库（掩码 + app_url）→ 返回视图。
func TestUserWalletConnect(t *testing.T) {
	key := "tk-test-123456789012"
	svc, repo, secrets, flows := newWalletService(t, key)
	ctx := context.Background()
	if _, err := svc.Start(ctx, 1, ""); err != nil {
		t.Fatal(err)
	}
	view, err := svc.Connect(ctx, 1, flows.state, "the-code")
	if err != nil {
		t.Fatal(err)
	}
	if !view.Connected || view.AppURL != "https://app.example.com" {
		t.Fatalf("视图不对：%+v", view)
	}
	if view.KeyMasked != "tk-tes…9012" {
		t.Fatalf("掩码不对：%q", view.KeyMasked)
	}
	if secrets.saved[model.WalletSecretName(1)] != key {
		t.Fatalf("Key 没有按 wallet:<uid> 保存：%v", secrets.names)
	}
	if repo.upserted == nil || repo.upserted.KeyMasked != view.KeyMasked || repo.upserted.Provider != "tokendance" {
		t.Fatalf("元数据没落库：%+v", repo.upserted)
	}
}

// Connect 的失败路径：flow 错误、上游没有给 Key、发起人不匹配。
func TestUserWalletConnectFailures(t *testing.T) {
	key := "tk-y"
	svc, _, _, flows := newWalletService(t, key)
	ctx := context.Background()
	if _, err := svc.Start(ctx, 1, ""); err != nil {
		t.Fatal(err)
	}

	// 错误的 state
	if _, err := svc.Connect(ctx, 1, "wrong-state", "c"); err == nil {
		t.Fatal("错误的 state 不应成功")
	}
	// 正确 state、错误的发起人：fakeFlows 固定 UserID=1，换用户 2 消费失败
	if _, err := svc.Connect(ctx, 2, flows.state, "c"); err == nil {
		t.Fatal("别人的 flow 不应成功")
	}
	// 上游没给 Key：换一个返回空 key 的上游
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "bad code"})
	}))
	defer broken.Close()
	flows2 := &fakeFlows{verifier: "v2", userID: 1}
	svc2 := NewUserWalletService(UserWalletDeps{
		Repo: &fakeWalletRepo{}, Secrets: &fakeWalletSecrets{saved: map[string]string{}},
		Flows: flows2, AppURL: "https://app.example.com",
		ExchangeURL: broken.URL,
	})
	if _, err := svc2.Start(ctx, 1, ""); err != nil {
		t.Fatal(err)
	}
	// 连接时用的 flow 是 fakeFlows.Issue 生成的 state（真实链路里由回调地址带回），不是整个授权地址
	_, err := svc2.Connect(ctx, 1, flows2.state, "c")
	if err == nil {
		t.Fatal("上游没给 Key 不应成功")
	}
	var ec *errcode.Error
	if !errors.As(err, &ec) || !strings.Contains(err.Error(), "bad code") {
		t.Fatalf("错误应带上游原文：%v", err)
	}
}

// Disconnect 删除元数据与密钥缓存。
func TestUserWalletDisconnect(t *testing.T) {
	svc, repo, secrets, flows := newWalletService(t, "tk-z")
	ctx := context.Background()
	if _, err := svc.Start(ctx, 1, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Connect(ctx, 1, flows.state, "c"); err != nil {
		t.Fatal(err)
	}
	if err := svc.Disconnect(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if repo.deleted != 1 || repo.row != nil {
		t.Fatalf("元数据没有删除：%+v", repo)
	}
	if len(secrets.forgots) != 1 || secrets.forgots[0] != model.WalletSecretName(1) {
		t.Fatalf("没有清密钥缓存：%v", secrets.forgots)
	}
	// 再断开：本来就没有连接，返回 ErrNotFound 语义错误
	if err := svc.Disconnect(ctx, 1); err == nil {
		t.Fatal("重复断开应报错")
	}
}

// Enabled / View 的未启用与未连接形态。
func TestUserWalletViewAndEnabled(t *testing.T) {
	off := NewUserWalletService(UserWalletDeps{Repo: &fakeWalletRepo{}, Secrets: &fakeWalletSecrets{saved: map[string]string{}}, Flows: &fakeFlows{}})
	if off.Enabled() {
		t.Fatal("没配 app_url 不应视为启用")
	}
	if _, err := off.Start(context.Background(), 1, ""); !errors.Is(err, errcode.ErrWalletDisabled) {
		t.Fatalf("未启用时应返回 ErrWalletDisabled：%v", err)
	}
	if _, err := off.Connect(context.Background(), 1, "f", "c"); !errors.Is(err, errcode.ErrWalletDisabled) {
		t.Fatalf("未启用时连接应返回 ErrWalletDisabled：%v", err)
	}

	svc, repo, _, _ := newWalletService(t, "tk-v")
	view, err := svc.View(context.Background(), 9)
	if err != nil {
		t.Fatal(err)
	}
	if view.Enabled && view.Connected {
		t.Fatalf("未连接时视图不对：%+v", view)
	}
	repo.row = &model.UserWallet{UserID: 9, AppURL: "https://app.example.com", KeyMasked: "tk-1…2", Provider: "tokendance"}
	view, err = svc.View(context.Background(), 9)
	if err != nil {
		t.Fatal(err)
	}
	if !view.Connected || view.KeyMasked != "tk-1…2" {
		t.Fatalf("已连接视图不对：%+v", view)
	}
	// Connected 实现 WalletChecker
	ok, err := svc.Connected(context.Background(), 9)
	if err != nil || !ok {
		t.Fatalf("Connected 不对：ok=%v err=%v", ok, err)
	}
	if ok, _ := svc.Connected(context.Background(), 10); ok {
		t.Fatal("别的用户不应显示已连接")
	}
}
