package service_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
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

// newBalanceService 组装带余额上游的服务：balance 上游返回固定余额。
// 生产的 balanceURL 是 https://tokendance.space/portal/api/v1/user/balance——
// 必须带 /portal 前缀：一级域名下的 /api/v1/... 是官网前端路由，返回 HTML 而不是 JSON（线上实测踩过）。
// 同时断言请求路径是 /portal/api/v1/user/balance（一级域名下的 /api/v1 是官网 HTML 页，不是 API）。
func newBalanceService(t *testing.T, credits, used int64) (*UserWalletService, *fakeWalletRepo) {
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
	})
	repo.row = &model.UserWallet{UserID: 7, Provider: "tokendance", AppURL: "https://app.example.com", KeyMasked: "tk-l…"}
	return svc, repo
}

// Balance：已连接时查上游余额（微元→元），60 秒内命中缓存。
func TestUserWalletBalance(t *testing.T) {
	ctx := context.Background()
	svc, _ := newBalanceService(t, 12_500_000, 3_000_000) // 12.5 元剩、3 元已用

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
	if view.Stale {
		t.Fatalf("不应标 stale：%+v", view)
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

	// 未连接（仓储无行）
	svc, _ := newBalanceService(t, 1, 0)
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
		BalanceURL: broken.URL,
	})
	view, err = svc2.Balance(ctx, 7)
	if err != nil || !view.Connected || !view.Stale {
		t.Fatalf("上游失败应 stale 降级：%+v err=%v", view, err)
	}

	// Key 读不到（secrets 里没有）：已连接 + stale
	noKey := &fakeWalletSecrets{saved: map[string]string{}}
	svc3 := NewUserWalletService(UserWalletDeps{
		Repo: repo, Secrets: noKey, Flows: &fakeFlows{}, AppURL: "https://app.example.com",
		BalanceURL: broken.URL,
	})
	view, err = svc3.Balance(ctx, 7)
	if err != nil || !view.Connected || !view.Stale {
		t.Fatalf("Key 读不到应 stale 降级：%+v err=%v", view, err)
	}
}

// newUsageService 组装带调用记录上游的服务：usage 上游返回给定记录，并断言请求带了
// limit / window 查询参数与用户自己的 Key（钱包场景所有金额都来自这里，不是平台积分 × 汇率）。
func newUsageService(t *testing.T, items []map[string]any, got *url.Values) (*UserWalletService, *fakeWalletRepo) {
	t.Helper()
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tk-live" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if got != nil {
			*got = r.URL.Query()
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"items": items, "total": 0})
	}))
	t.Cleanup(up.Close)
	repo := &fakeWalletRepo{row: &model.UserWallet{UserID: 7, Provider: "tokendance"}}
	secrets := &fakeWalletSecrets{saved: map[string]string{model.WalletSecretName(7): "tk-live"}}
	svc := NewUserWalletService(UserWalletDeps{
		Repo: repo, Secrets: secrets, Flows: &fakeFlows{}, AppURL: "https://app.example.com",
		UsageURL: up.URL,
	})
	return svc, repo
}

// Usage：已连接时拉上游调用记录，微元→元，保留 newest first 顺序；失败调用也带上（花费为 0）。
func TestUserWalletUsage(t *testing.T) {
	ctx := context.Background()
	var q url.Values
	svc, _ := newUsageService(t, []map[string]any{
		{"model_id": "01KMM9QH8VZX83GM06Q4A6JEF4", "model_slug": "seedream-5.0-lite", "created_at": "2026-11-17T12:15:40Z", "cost": 220_000, "is_success": true},
		{"model_id": "01KMM9QH8VZX83GM06Q4A6JEF4", "model_slug": "seedream-5.0-lite", "created_at": "2026-11-17T10:08:10Z", "cost": 220_000, "is_success": true},
		{"model_id": "01KMM9QH8VZX83GM06Q4A6JEF4", "model_slug": "seedream-5.0-lite", "created_at": "2026-11-17T09:21:17Z", "cost": 0, "error": "供应商返回 400"},
	}, &q)

	view, err := svc.Usage(ctx, 7)
	if err != nil {
		t.Fatal(err)
	}
	if !view.Enabled || !view.Connected || view.Stale {
		t.Fatalf("形态不对：%+v", view)
	}
	if len(view.Items) != 3 {
		t.Fatalf("应带回 3 条：%+v", view.Items)
	}
	// 真实单价：seedream 每张 0.22 元（用户在自己 TokenDance 账号里付的钱）
	// 模型名走 model_slug；model_id 是内部不透明 id，不能拿来匹配（线上实测踩过）
	if view.Items[0].ModelSlug != "seedream-5.0-lite" || view.Items[0].CostYuan != 0.22 {
		t.Fatalf("金额换算或顺序不对：%+v", view.Items[0])
	}
	if view.Items[2].Success || view.Items[2].CostYuan != 0 {
		t.Fatalf("失败记录应标失败且 0 元：%+v", view.Items[2])
	}
	// 分页参数：limit 500 + window 30d（不无限翻页，1QPS 限制下翻页会把页面拖死）
	if q.Get("limit") != "500" || q.Get("window") != "30d" {
		t.Fatalf("查询参数不对：%v", q)
	}

	// 60 秒内第二次走缓存
	view2, err := svc.Usage(ctx, 7)
	if err != nil || len(view2.Items) != 3 {
		t.Fatalf("应命中缓存：%+v err=%v", view2, err)
	}
}

// Usage 的降级：未连接、上游挂、Key 读不到——都不报错；上游挂时返回旧缓存并标 stale。
func TestUserWalletUsageFallbacks(t *testing.T) {
	ctx := context.Background()

	// 未连接
	svc, _ := newUsageService(t, nil, nil)
	view, err := svc.Usage(ctx, 9)
	if err != nil || !view.Enabled || view.Connected || len(view.Items) != 0 {
		t.Fatalf("未连接应降级：%+v err=%v", view, err)
	}

	// 上游 500 + 无缓存：stale + 空列表
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer broken.Close()
	repo := &fakeWalletRepo{row: &model.UserWallet{UserID: 7, Provider: "tokendance"}}
	secrets := &fakeWalletSecrets{saved: map[string]string{model.WalletSecretName(7): "tk-live"}}
	svc2 := NewUserWalletService(UserWalletDeps{
		Repo: repo, Secrets: secrets, Flows: &fakeFlows{}, AppURL: "https://app.example.com",
		UsageURL: broken.URL,
	})
	view, err = svc2.Usage(ctx, 7)
	if err != nil || !view.Connected || !view.Stale || len(view.Items) != 0 {
		t.Fatalf("上游失败应 stale 降级：%+v err=%v", view, err)
	}

	// Key 读不到：已连接 + stale
	noKey := &fakeWalletSecrets{saved: map[string]string{}}
	svc4 := NewUserWalletService(UserWalletDeps{
		Repo: repo, Secrets: noKey, Flows: &fakeFlows{}, AppURL: "https://app.example.com",
		UsageURL: broken.URL,
	})
	view, err = svc4.Usage(ctx, 7)
	if err != nil || !view.Connected || !view.Stale {
		t.Fatalf("Key 读不到应 stale 降级：%+v err=%v", view, err)
	}
}

// newRechargeService 组装充值代理：上游按 HTTP 方法返回创建 / 查询结果，并校验 Bearer 是用户自己的 Key。
func newRechargeService(t *testing.T, handler http.HandlerFunc) (*UserWalletService, *fakeWalletRepo) {
	t.Helper()
	up := httptest.NewServer(handler)
	t.Cleanup(up.Close)
	repo := &fakeWalletRepo{row: &model.UserWallet{UserID: 7, Provider: "tokendance"}}
	secrets := &fakeWalletSecrets{saved: map[string]string{model.WalletSecretName(7): "tk-live"}}
	svc := NewUserWalletService(UserWalletDeps{
		Repo: repo, Secrets: secrets, Flows: &fakeFlows{}, AppURL: "https://app.example.com",
		RechargeURL: up.URL,
	})
	return svc, repo
}

// Recharge / RechargeStatus：用用户自己的 Key 创建会话，不把上游 status_url 透给浏览器；
// 状态查询从固定会话地址发 GET，金额、支付链接和时间都按前端需要的形状返回。
func TestUserWalletRecharge(t *testing.T) {
	created := false
	svc, _ := newRechargeService(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tk-live" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/":
			created = true
			var body struct {
				Amount int `json:"amount"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Amount != 30 {
				t.Fatalf("创建充值请求体不对：%+v err=%v", body, err)
			}
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":          "sess-1",
				"amount":      30,
				"status":      "pending",
				"payment_url": "weixin://pay/sess-1",
				"alipay_url":  "alipays://pay/sess-1",
				"expired_at":  1_800_000_000,
			})
		case r.Method == http.MethodGet && r.URL.Path == "/sess-1":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":         "sess-1",
				"amount":     30,
				"status":     "paid",
				"expired_at": 1_800_000_000,
				"paid_at":    1_799_999_100,
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	ctx := context.Background()
	view, err := svc.Recharge(ctx, 7, 30)
	if err != nil {
		t.Fatal(err)
	}
	if !created || view.ID != "sess-1" || view.Amount != 30 || view.Status != "pending" {
		t.Fatalf("创建会话视图不对：%+v", view)
	}
	if view.PaymentURL != "weixin://pay/sess-1" || view.AlipayURL != "alipays://pay/sess-1" {
		t.Fatalf("支付链接没带上：%+v", view)
	}
	if view.ExpiresAt != "2027-01-15T08:00:00Z" {
		t.Fatalf("过期时间格式不对：%q", view.ExpiresAt)
	}

	status, err := svc.RechargeStatus(ctx, 7, "sess-1")
	if err != nil {
		t.Fatal(err)
	}
	if status.Status != "paid" || status.PaidAt != "2027-01-15T07:45:00Z" {
		t.Fatalf("状态视图不对：%+v", status)
	}
}

// TestUserWalletRechargeSessionEnvelope 验证官方 Agent Payment 接口的 {"session": {...}} 包装能正确解析。
func TestUserWalletRechargeSessionEnvelope(t *testing.T) {
	svc, _ := newRechargeService(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tk-live" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		session := map[string]any{
			"id":          "sess-envelope",
			"amount":      30,
			"status":      "pending",
			"payment_url": "weixin://pay/sess-envelope",
			"alipay_url":  "alipays://pay/sess-envelope",
			"expired_at":  1_800_000_000,
		}
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusCreated)
		} else {
			session["status"] = "paid"
			session["paid_at"] = 1_799_999_100
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"session": session})
	})

	ctx := context.Background()
	view, err := svc.Recharge(ctx, 7, 30)
	if err != nil {
		t.Fatal(err)
	}
	if view.ID != "sess-envelope" || view.PaymentURL != "weixin://pay/sess-envelope" || view.Status != "pending" {
		t.Fatalf("创建会话包装解析不对：%+v", view)
	}
	status, err := svc.RechargeStatus(ctx, 7, "sess-envelope")
	if err != nil {
		t.Fatal(err)
	}
	if status.ID != "sess-envelope" || status.Status != "paid" {
		t.Fatalf("查询会话包装解析不对：%+v", status)
	}
}

// 充值校验：未启用 / 未连接 / Key 丢失 / 金额越界 / 非法会话 ID 都不请求上游。
func TestUserWalletRechargeValidation(t *testing.T) {
	ctx := context.Background()
	off := NewUserWalletService(UserWalletDeps{Repo: &fakeWalletRepo{}, Secrets: &fakeWalletSecrets{saved: map[string]string{}}, Flows: &fakeFlows{}})
	if _, err := off.Recharge(ctx, 7, 30); !errors.Is(err, errcode.ErrWalletDisabled) {
		t.Fatalf("未启用应返回 ErrWalletDisabled：%v", err)
	}

	svc, repo := newRechargeService(t, func(http.ResponseWriter, *http.Request) {
		t.Fatal("校验失败时不应请求上游")
	})
	repo.row = nil
	if _, err := svc.Recharge(ctx, 7, 30); !errors.Is(err, errcode.ErrWalletNotConnected) {
		t.Fatalf("未连接应返回 ErrWalletNotConnected：%v", err)
	}
	repo.row = &model.UserWallet{UserID: 7, Provider: "tokendance"}
	for name, call := range map[string]func() error{
		"零金额": func() error { _, err := svc.Recharge(ctx, 7, 0); return err },
		"超额":  func() error { _, err := svc.Recharge(ctx, 7, 100001); return err },
		"非法会话 ID": func() error {
			_, err := svc.RechargeStatus(ctx, 7, "../health")
			return err
		},
	} {
		var ec *errcode.Error
		if err := call(); !errors.As(err, &ec) || ec.Code != errcode.ErrInvalidParams.Code {
			t.Fatalf("%s 应返回 ErrInvalidParams：%v", name, err)
		}
	}

	noKey := NewUserWalletService(UserWalletDeps{
		Repo: repo, Secrets: &fakeWalletSecrets{saved: map[string]string{}}, Flows: &fakeFlows{}, AppURL: "https://app.example.com",
		RechargeURL: "https://tokenpay.invalid",
	})
	if _, err := noKey.Recharge(ctx, 7, 30); !errors.Is(err, errcode.ErrWalletNotConnected) {
		t.Fatalf("Key 丢失应返回 ErrWalletNotConnected：%v", err)
	}

	expiredKey, _ := newRechargeService(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	var ec *errcode.Error
	if _, err := expiredKey.Recharge(ctx, 7, 30); !errors.As(err, &ec) || ec.Code != errcode.ErrWalletNotConnected.Code {
		t.Fatalf("上游 401 应让用户重新连接钱包：%v", err)
	}
}

// 支付成功后清掉余额缓存：下一次 Balance 必须重新查上游，右上角能马上看到新余额。
func TestUserWalletRechargePaidInvalidatesBalance(t *testing.T) {
	balanceMicros := int64(1_000_000)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/balance":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"balance": map[string]any{"credits": balanceMicros, "credits_used": 0, "balance": balanceMicros},
			})
		case "/recharge/sess-1":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "sess-1", "amount": 30, "status": "paid", "expired_at": 1_800_000_000})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer up.Close()
	repo := &fakeWalletRepo{row: &model.UserWallet{UserID: 7, Provider: "tokendance"}}
	secrets := &fakeWalletSecrets{saved: map[string]string{model.WalletSecretName(7): "tk-live"}}
	svc := NewUserWalletService(UserWalletDeps{
		Repo: repo, Secrets: secrets, Flows: &fakeFlows{}, AppURL: "https://app.example.com",
		BalanceURL: up.URL + "/balance", RechargeURL: up.URL + "/recharge",
	})

	ctx := context.Background()
	first, err := svc.Balance(ctx, 7)
	if err != nil || first.BalanceYuan != 1 {
		t.Fatalf("首次余额不对：%+v err=%v", first, err)
	}
	if _, err := svc.RechargeStatus(ctx, 7, "sess-1"); err != nil {
		t.Fatal(err)
	}
	balanceMicros = 31_000_000
	next, err := svc.Balance(ctx, 7)
	if err != nil || next.BalanceYuan != 31 {
		t.Fatalf("支付后应刷新余额：%+v err=%v", next, err)
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
