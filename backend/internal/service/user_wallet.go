package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"video-canvas/internal/cache"
	"video-canvas/internal/model"
	"video-canvas/internal/pkg/errcode"
	"video-canvas/internal/pkg/outbound"
	"video-canvas/internal/repository"
)

const (
	// TokenPay 相关的固定上游地址：授权页、授权码交换接口（见 https://tokendance.space/docs/api-key-oauth）。
	tokenPayAuthBase        = "https://tokendance.space/auth"
	tokenPayExchangeDefault = "https://tokendance.space/portal/api/v1/auth/keys"
	tokenPayProviderName    = "tokendance"
	// tokenPayVerifierBytes 是 PKCE code_verifier 的随机字节数：base64url 无填充编码后 43 个字符，落在上游要求的 43–128 区间。
	tokenPayVerifierBytes = 32
	// tokenPayExchangeTimeout 是授权码交换请求的超时：上游要求 code 在 10 分钟内使用，交换本身要快。
	tokenPayExchangeTimeout = 15 * time.Second
	// tokenPayBalanceURL 是官方开放平台的余额查询（见 https://tokendance.space/docs/open-api）：用用户自己的 Key 查余额与已消耗。
	// 注意是 /portal 前缀：一级域名下的 /api/v1/... 是官网前端路由，会返回 HTML（SPA fallback）而不是 JSON。
	tokenPayBalanceURL = "https://tokendance.space/portal/api/v1/user/balance"
	// tokenPayUsageURL 是官方开放平台的调用记录（同上，/portal 前缀）：逐条真实花费的唯一来源。
	// 钱包场景没有「平台积分 × 汇率」这回事——用户在自己 TokenDance 账号里付多少钱，这里就显示多少钱。
	tokenPayUsageURL = "https://tokendance.space/portal/api/v1/usage"
	// tokenPayBalanceTimeout 是余额查询的超时：它挂在多个页面的加载路径上，不能拖慢首屏。
	tokenPayBalanceTimeout = 8 * time.Second
	// tokenPayUsageTimeout 是调用记录查询的超时：记录列表页要等它，比余额宽松一些。
	tokenPayUsageTimeout = 12 * time.Second
	// tokenPayBalanceTTL 是余额的进程内缓存时长：上游限流同一用户 1QPS，多个页面同时加载只会打一次。
	tokenPayBalanceTTL = 60 * time.Second
	// tokenPayUsageTTL 是调用记录的缓存时长：同样受 1QPS 限制，且记录不会秒级变化。
	tokenPayUsageTTL = 60 * time.Second
	// tokenPayUsageLimit 是每次拉取的记录条数：上游 limit 上限 500。
	tokenPayUsageLimit = 500
	// tokenPayUsageWindow 是拉取的时间窗：只覆盖最近的记录，更早的显示「—」而不是无限翻页（1QPS 下翻页会把页面拖死）。
	tokenPayUsageWindow = "30d"
	// tokenPayMicrosPerYuan 是上游金额单位（微元）与元的换算：1 元 = 1e6 微元。
	tokenPayMicrosPerYuan = 1_000_000
)

// tokenPayHTTP 是统一出站客户端：授权码交换会记 ext.call 与 payload 日志（架构测试禁止业务代码自建 http.Client）。
var tokenPayHTTP = outbound.Client("tokenpay", &http.Client{Timeout: tokenPayExchangeTimeout})

// tokenPayBalanceHTTP 是余额查询的出站客户端（单独 purpose，日志里能和授权交换区分）。
var tokenPayBalanceHTTP = outbound.Client("tokenpay.balance", &http.Client{Timeout: tokenPayBalanceTimeout})

// tokenPayUsageHTTP 是调用记录查询的出站客户端。
var tokenPayUsageHTTP = outbound.Client("tokenpay.usage", &http.Client{Timeout: tokenPayUsageTimeout})

// UserWalletRepo 用户钱包元数据的仓储契约（service 层依赖接口，见 repository.UserWalletRepository）。
type UserWalletRepo interface {
	GetByUser(ctx context.Context, userID uint64) (*model.UserWallet, error)
	Upsert(ctx context.Context, w *model.UserWallet) error
	Delete(ctx context.Context, userID uint64) error
}

// WalletSecrets 是钱包 Key 的加密存储契约，由 AIConfigService 实现（ai_secrets，AES-256-GCM）。
type WalletSecrets interface {
	// SetSecret 加密保存（覆盖）；明文入库后任何接口都读不回
	SetSecret(ctx context.Context, name, value string, adminID uint64) error
	// ForgetSecret 清掉本实例对这一凭证的明文缓存
	ForgetSecret(name string)
	// Get 解密读取：请求上游用，绝不进响应
	Get(ctx context.Context, name string) (string, error)
}

// UserWalletService 用户 TokenPay（TokenDance）钱包：发起授权、用一次性授权码交换 Key 并加密保存、断开。
// Key 密文存 ai_secrets（wallet:<user_id>），钱包渠道的任务执行时取它支付上游；平台不冻结、不扣积分。
type UserWalletService struct {
	repo        UserWalletRepo
	secrets     WalletSecrets
	flows       cache.OAuthFlowStore
	appURL      string // 站点对外地址：归因 App URL + 回调基址；空表示本部署未启用钱包连接
	keyName     string // 授权页展示的应用名
	exchangeURL string // 授权码交换接口；默认 TokenDance 官方地址，测试可注入
	balanceURL  string // 余额查询接口；默认 TokenDance 官方地址，测试可注入
	usageURL    string // 调用记录接口；默认 TokenDance 官方地址，测试可注入
	rechargeURL string // 充值会话接口；默认 TokenDance 官方地址，测试可注入
	now         func() time.Time

	balMu    sync.Mutex
	balCache map[uint64]walletBalanceEntry // 余额缓存：上游 1QPS 限流，按用户缓存
	useMu    sync.Mutex
	useCache map[uint64]walletUsageEntry // 调用记录缓存：同样受 1QPS 限制
}

// UserWalletDeps 是钱包服务的依赖。
type UserWalletDeps struct {
	Repo    UserWalletRepo
	Secrets WalletSecrets        // Key 的加密保存
	Flows   cache.OAuthFlowStore // OAuth 授权流程（PKCE verifier + 发起用户），Redis / 内存实现
	AppURL  string
	KeyName string
	// ExchangeURL 覆盖授权码交换接口；测试注入假上游用，生产留空
	ExchangeURL string
	// BalanceURL 覆盖余额查询接口；测试注入假上游用，生产留空
	BalanceURL string
	// UsageURL 覆盖调用记录接口；测试注入假上游用，生产留空
	UsageURL string
	// RechargeURL 覆盖充值会话接口；测试注入假上游用，生产留空
	RechargeURL string
}

// walletBalanceEntry 是某个用户的余额缓存：值与存入时间。
type walletBalanceEntry struct {
	at   time.Time
	view WalletBalance
}

// NewUserWalletService 创建钱包服务。
func NewUserWalletService(deps UserWalletDeps) *UserWalletService {
	exchangeURL := deps.ExchangeURL
	if exchangeURL == "" {
		exchangeURL = tokenPayExchangeDefault
	}
	balanceURL := deps.BalanceURL
	if balanceURL == "" {
		balanceURL = tokenPayBalanceURL
	}
	usageURL := deps.UsageURL
	if usageURL == "" {
		usageURL = tokenPayUsageURL
	}
	rechargeURL := deps.RechargeURL
	if rechargeURL == "" {
		rechargeURL = tokenPayRechargeURL
	}
	return &UserWalletService{
		repo:        deps.Repo,
		secrets:     deps.Secrets,
		flows:       deps.Flows,
		appURL:      strings.TrimRight(deps.AppURL, "/"),
		keyName:     deps.KeyName,
		exchangeURL: exchangeURL,
		balanceURL:  balanceURL,
		usageURL:    usageURL,
		rechargeURL: strings.TrimRight(rechargeURL, "/"),
		now:         time.Now,
		balCache:    map[uint64]walletBalanceEntry{},
		useCache:    map[uint64]walletUsageEntry{},
	}
}

// Enabled 表示本部署启用了钱包连接（配了 tokenpay.app_url）。未启用时用户侧接口一律返回 ErrWalletDisabled。
func (s *UserWalletService) Enabled() bool { return s.appURL != "" }

// Connected 实现 GenerationTaskService.WalletChecker：用户是否已连接钱包。任务创建前的钱包渠道检查用它。
func (s *UserWalletService) Connected(ctx context.Context, userID uint64) (bool, error) {
	_, err := s.repo.GetByUser(ctx, userID)
	if errors.Is(err, repository.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// WalletView 是用户钱包的展示视图：Key 永远只回掩码。
type WalletView struct {
	Enabled     bool   `json:"enabled"`
	Connected   bool   `json:"connected"`
	AppURL      string `json:"app_url"`
	KeyMasked   string `json:"key_masked"`
	ConnectedAt string `json:"connected_at"`
}

// View 返回当前的钱包状态。
func (s *UserWalletService) View(ctx context.Context, userID uint64) (*WalletView, error) {
	view := &WalletView{Enabled: s.Enabled(), AppURL: s.appURL}
	row, err := s.repo.GetByUser(ctx, userID)
	if errors.Is(err, repository.ErrNotFound) {
		return view, nil
	}
	if err != nil {
		return nil, err
	}
	view.Connected = true
	view.AppURL = row.AppURL
	view.KeyMasked = row.KeyMasked
	view.ConnectedAt = row.UpdatedAt.Format(time.RFC3339)
	return view, nil
}

// WalletBalance 是钱包余额视图：TokenDance 账户的余额与已消耗（元）。
type WalletBalance struct {
	Enabled     bool    `json:"enabled"`
	Connected   bool    `json:"connected"`
	BalanceYuan float64 `json:"balance_yuan"` // 账户剩余额度（元）
	UsedYuan    float64 `json:"used_yuan"`    // 账户已消耗（元）
	// Stale 为 true 表示这次是降级数据：上游查询失败但有旧缓存，或刚连上还没查到。
	// 前端照常展示，只在 tooltip 里说明「可能不是最新」。
	Stale bool `json:"stale"`
}

// Balance 查钱包余额：未启用 / 未连接返回降级视图（不报错，页面照常渲染）；
// 已连接则用用户自己的 Key 调上游余额接口，60 秒内直接取缓存（上游限流同一用户 1QPS）。
// 上游失败时返回旧缓存并标 Stale；没有缓存就返回 Stale 的零值，绝不把失败透传给页面。
func (s *UserWalletService) Balance(ctx context.Context, userID uint64) (*WalletBalance, error) {
	if !s.Enabled() {
		return &WalletBalance{}, nil
	}
	if _, err := s.repo.GetByUser(ctx, userID); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return &WalletBalance{Enabled: true}, nil
		}
		return nil, err
	}

	s.balMu.Lock()
	cached, ok := s.balCache[userID]
	s.balMu.Unlock()
	if ok && s.now().Sub(cached.at) < tokenPayBalanceTTL {
		view := cached.view
		return &view, nil
	}

	view := WalletBalance{Enabled: true, Connected: true}
	key, err := s.secrets.Get(ctx, model.WalletSecretName(userID))
	if err != nil || key == "" {
		// Key 读不到（存储异常）：告诉前端已连接但余额查不到
		view.Stale = true
		if ok {
			view = cached.view
			view.Stale = true
		}
		return &view, nil
	}
	balanceYuan, usedYuan, err := s.fetchBalance(ctx, key)
	if err != nil {
		view.Stale = true
		if ok {
			view = cached.view
			view.Stale = true
		}
		return &view, nil
	}
	view.BalanceYuan = balanceYuan
	view.UsedYuan = usedYuan
	s.balMu.Lock()
	s.balCache[userID] = walletBalanceEntry{at: s.now(), view: view}
	s.balMu.Unlock()
	return &view, nil
}

// walletBalanceUpstream 是上游余额响应：金额单位微元（1 元 = 1e6）。
type walletBalanceUpstream struct {
	Balance struct {
		Credits     int64 `json:"credits"`      // 历史总额度（充值获得）
		CreditsUsed int64 `json:"credits_used"` // 已消耗
		Balance     int64 `json:"balance"`      // 剩余可用
	} `json:"balance"`
}

// fetchBalance 调上游余额接口，返回换算成元的余额与已消耗。
func (s *UserWalletService) fetchBalance(ctx context.Context, key string) (balance float64, used float64, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.balanceURL, nil)
	if err != nil {
		return 0, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Accept", "application/json")
	resp, err := tokenPayBalanceHTTP.Do(req)
	if err != nil {
		return 0, 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return 0, 0, err
	}
	if resp.StatusCode != http.StatusOK {
		return 0, 0, fmt.Errorf("上游余额接口返回 HTTP %d", resp.StatusCode)
	}
	var body walletBalanceUpstream
	if err := json.Unmarshal(raw, &body); err != nil {
		return 0, 0, err
	}
	return microsToYuan(body.Balance.Balance), microsToYuan(body.Balance.CreditsUsed), nil
}

// microsToYuan 把上游的微元金额换算成元。
func microsToYuan(micros int64) float64 {
	return float64(micros) / tokenPayMicrosPerYuan
}

// walletUsageEntry 是某个用户的调用记录缓存：值与存入时间。
type walletUsageEntry struct {
	at   time.Time
	view WalletUsageView
}

// WalletUsageView 是用户 TokenDance 账号的调用记录：逐条真实花费， newest first。
// 界面上的金额只认它——钱包任务不扣平台积分，「平台积分 × 汇率」在本部署不存在。
type WalletUsageView struct {
	Enabled   bool          `json:"enabled"`
	Connected bool          `json:"connected"`
	Items     []WalletUsage `json:"items"`
	// Stale 为 true 表示这次是降级数据：上游查询失败但有旧缓存。前端照常展示旧记录。
	Stale bool `json:"stale"`
}

// WalletUsage 是一条上游调用记录（金额单位：元）。
type WalletUsage struct {
	// ModelID 是 TokenDance 内部模型 id（不透明，如 01KMM9…），**不是**模型名，不能拿来匹配
	ModelID string `json:"model_id"`
	// ModelSlug 是模型别名（如 seedream-5.0-lite），与本站模型的 upstream_model 一致；上游只对有别名的模型给
	ModelSlug   string    `json:"model_slug"`
	CreatedAt   time.Time `json:"created_at"` // UTC，调用完成时间
	Success     bool      `json:"success"`
	CostYuan    float64   `json:"cost_yuan"`
}

// Usage 拉用户的调用记录：未启用 / 未连接返回空视图（不报错，页面照常渲染）；
// 已连接则用用户自己的 Key 调上游，60 秒内直接取缓存（上游限流同一用户 1QPS）。
// 上游失败时返回旧缓存并标 Stale；没有缓存就返回空列表，绝不把失败透传给页面。
func (s *UserWalletService) Usage(ctx context.Context, userID uint64) (*WalletUsageView, error) {
	if !s.Enabled() {
		return &WalletUsageView{}, nil
	}
	if _, err := s.repo.GetByUser(ctx, userID); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return &WalletUsageView{Enabled: true}, nil
		}
		return nil, err
	}

	s.useMu.Lock()
	cached, ok := s.useCache[userID]
	s.useMu.Unlock()
	if ok && s.now().Sub(cached.at) < tokenPayUsageTTL {
		view := cached.view
		return &view, nil
	}

	key, err := s.secrets.Get(ctx, model.WalletSecretName(userID))
	if err != nil || key == "" {
		// Key 读不到（存储异常）：已连接但记录查不到
		view := WalletUsageView{Enabled: true, Connected: true, Stale: true}
		if ok {
			view = cached.view
			view.Stale = true
		}
		return &view, nil
	}
	items, err := s.fetchUsage(ctx, key)
	if err != nil {
		view := WalletUsageView{Enabled: true, Connected: true, Stale: true}
		if ok {
			view = cached.view
			view.Stale = true
		}
		return &view, nil
	}
	view := WalletUsageView{Enabled: true, Connected: true, Items: items}
	s.useMu.Lock()
	s.useCache[userID] = walletUsageEntry{at: s.now(), view: view}
	s.useMu.Unlock()
	return &view, nil
}

// walletUsageUpstream 是上游调用记录响应（见 https://tokendance.space/docs/open-api）。
// 注意：model_id 是内部不透明 id（如 01KMM9…），模型名在 model_slug 里（只对有别名的模型给）——
// 线上实测踩过：按 model_id 匹配永远匹配不上，界面全显示「—」。
type walletUsageUpstream struct {
	Items []struct {
		ModelID    string `json:"model_id"`
		ModelSlug  string `json:"model_slug"` // 模型别名，如 seedream-5.0-lite；没有时为空
		CreatedAt  string `json:"created_at"` // UTC ISO 8601
		Cost       int64  `json:"cost"`       // 微元
		Error      string `json:"error"`      // 仅失败请求有
		Successful *bool  `json:"is_success"` // 缺省时按「有没有 error」判断
	} `json:"items"`
}

// usageSucceeded 判断一条记录是否成功：上游给了 is_success 就认它，否则按有没有 error 字段。
func usageSucceeded(flag *bool, errText string) bool {
	if flag != nil {
		return *flag
	}
	return strings.TrimSpace(errText) == ""
}

// fetchUsage 调上游调用记录接口，返回换算成元的记录（newest first，原样保留顺序）。
func (s *UserWalletService) fetchUsage(ctx context.Context, key string) ([]WalletUsage, error) {
	q := url.Values{}
	q.Set("limit", strconv.Itoa(tokenPayUsageLimit))
	q.Set("window", tokenPayUsageWindow)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.usageURL+"?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Accept", "application/json")
	resp, err := tokenPayUsageHTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("上游调用记录接口返回 HTTP %d", resp.StatusCode)
	}
	var body walletUsageUpstream
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, err
	}
	out := make([]WalletUsage, 0, len(body.Items))
	for _, item := range body.Items {
		at, err := time.Parse(time.RFC3339, item.CreatedAt)
		if err != nil {
			continue // 时间解析不了就没法匹配，跳过这条
		}
		out = append(out, WalletUsage{
			ModelID:   item.ModelID,
			ModelSlug: item.ModelSlug,
			CreatedAt: at.UTC(),
			Success:   usageSucceeded(item.Successful, item.Error),
			CostYuan:  microsToYuan(item.Cost),
		})
	}
	return out, nil
}

// Start 发起授权：生成 PKCE verifier 与 state，返回跳转 TokenDance 授权页的地址。
// 用户确认后上游 302 回 <app_url>/wallet/tokenpay/callback?flow=<state>&next=..&code=..，前端把 flow/code POST 回 /connect。
func (s *UserWalletService) Start(ctx context.Context, userID uint64, next string) (string, error) {
	if !s.Enabled() {
		return "", errcode.ErrWalletDisabled
	}
	verifier, err := randomToken(tokenPayVerifierBytes)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	state, err := s.flows.Issue(ctx, verifier, userID, cache.OAuthFlowTTL)
	if err != nil {
		return "", err
	}
	callback := fmt.Sprintf("%s/wallet/tokenpay/callback?flow=%s", s.appURL, url.QueryEscape(state))
	if next != "" {
		callback += "&next=" + url.QueryEscape(next)
	}
	q := url.Values{}
	q.Set("callback_url", callback)
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	q.Set("app_url", s.appURL)
	q.Set("key_name", s.keyName)
	return tokenPayAuthBase + "?" + q.Encode(), nil
}

// Connect 用回调带回的一次性授权码交换 Key：验 flow 归属与一次性，换到的 Key 加密入库，元数据落 user_wallets。
// 完整 Key 只在交换响应里出现一次，密文入库后任何接口都读不回明文。
func (s *UserWalletService) Connect(ctx context.Context, userID uint64, flow, code string) (*WalletView, error) {
	if !s.Enabled() {
		return nil, errcode.ErrWalletDisabled
	}
	stored, ok := s.flows.Consume(ctx, flow, userID)
	if !ok {
		return nil, errcode.ErrInvalidParams.WithMsg("授权流程不存在或已过期，请重新发起连接")
	}
	key, err := s.exchange(ctx, code, stored.Verifier)
	if err != nil {
		return nil, err
	}
	// Key 加密入库（明文只在这里出现一次）；失败则授权码已被消费但 Key 没存下来，用户需要重新授权
	if err := s.secrets.SetSecret(ctx, model.WalletSecretName(userID), key, userID); err != nil {
		return nil, err
	}
	now := s.now()
	masked := maskKey(key)
	row := &model.UserWallet{UserID: userID, Provider: tokenPayProviderName, AppURL: s.appURL, KeyMasked: masked, CreatedAt: now, UpdatedAt: now}
	if err := s.repo.Upsert(ctx, row); err != nil {
		return nil, err
	}
	view := &WalletView{Enabled: true, Connected: true, AppURL: row.AppURL, KeyMasked: masked, ConnectedAt: row.UpdatedAt.Format(time.RFC3339)}
	s.forgetBalance(userID)
	return view, nil
}

// Disconnect 断开钱包：删除元数据与 Key 密文。渠道里的钱包模型之后对该用户不可用。
func (s *UserWalletService) Disconnect(ctx context.Context, userID uint64) error {
	if err := s.repo.Delete(ctx, userID); err != nil {
		return err
	}
	s.secrets.ForgetSecret(model.WalletSecretName(userID))
	s.forgetBalance(userID)
	return nil
}

// forgetBalance 清掉某个用户的余额与调用记录缓存：连接状态变化后旧数据不能再用。
func (s *UserWalletService) forgetBalance(userID uint64) {
	s.balMu.Lock()
	delete(s.balCache, userID)
	s.balMu.Unlock()
	s.useMu.Lock()
	delete(s.useCache, userID)
	s.useMu.Unlock()
}

// exchange 用一次性授权码 + PKCE verifier 向 TokenDance 换 API Key；403 表示 code 无效 / 过期 / verifier 不匹配。
func (s *UserWalletService) exchange(ctx context.Context, code, verifier string) (string, error) {
	payload, _ := json.Marshal(map[string]string{
		"code":                  code,
		"code_verifier":         verifier,
		"code_challenge_method": "S256",
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.exchangeURL, strings.NewReader(string(payload)))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := tokenPayHTTP.Do(req)
	if err != nil {
		return "", errcode.ErrInternal.WithMsg("连接 TokenDance 失败，请稍后重试")
	}
	defer func() { _ = resp.Body.Close() }()
	var body struct {
		Key   string `json:"key"`
		Error string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", errcode.ErrInternal.WithMsg("TokenDance 响应无法解析，请稍后重试")
	}
	if resp.StatusCode != http.StatusOK || body.Key == "" {
		msg := strings.TrimSpace(body.Error)
		if msg == "" {
			msg = fmt.Sprintf("TokenDance 拒绝了授权码交换（HTTP %d）", resp.StatusCode)
		}
		return "", errcode.ErrInvalidParams.WithMsg(msg)
	}
	return body.Key, nil
}

// maskKey 掩码展示 Key：前 6 位 + … + 后 4 位；太短只露前 3 位。
func maskKey(key string) string {
	if len(key) <= 12 {
		if len(key) <= 3 {
			return "…"
		}
		return key[:3] + "…"
	}
	return key[:6] + "…" + key[len(key)-4:]
}

// randomToken 生成 n 字节随机数的 base64url（无填充）字符串。
func randomToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
