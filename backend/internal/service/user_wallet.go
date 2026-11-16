package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
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
)

// tokenPayHTTP 是统一出站客户端：授权码交换会记 ext.call 与 payload 日志（架构测试禁止业务代码自建 http.Client）。
var tokenPayHTTP = outbound.Client("tokenpay", &http.Client{Timeout: tokenPayExchangeTimeout})

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
	now         func() time.Time
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
}

// NewUserWalletService 创建钱包服务。
func NewUserWalletService(deps UserWalletDeps) *UserWalletService {
	exchangeURL := deps.ExchangeURL
	if exchangeURL == "" {
		exchangeURL = tokenPayExchangeDefault
	}
	return &UserWalletService{
		repo:        deps.Repo,
		secrets:     deps.Secrets,
		flows:       deps.Flows,
		appURL:      strings.TrimRight(deps.AppURL, "/"),
		keyName:     deps.KeyName,
		exchangeURL: exchangeURL,
		now:         time.Now,
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
	return view, nil
}

// Disconnect 断开钱包：删除元数据与 Key 密文。渠道里的钱包模型之后对该用户不可用。
func (s *UserWalletService) Disconnect(ctx context.Context, userID uint64) error {
	if err := s.repo.Delete(ctx, userID); err != nil {
		return err
	}
	s.secrets.ForgetSecret(model.WalletSecretName(userID))
	return nil
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
