package service

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"

	"video-canvas/internal/cache"
	"video-canvas/internal/model"
	"video-canvas/internal/pkg/errcode"
	"video-canvas/internal/pkg/logger"
	"video-canvas/internal/pkg/outbound"
	"video-canvas/internal/repository"
)

const (
	watchaAuthorizeURL  = "https://watcha.cn/oauth/authorize"
	watchaExchangeURL   = "https://watcha.cn/oauth/api/token"
	watchaUserInfoURL   = "https://watcha.cn/oauth/api/userinfo"
	watchaScope         = "read"
	watchaHTTPTimeout   = 15 * time.Second
	watchaMaxResponse   = 1 << 20
	watchaUserAgent     = "video-canvas"
	watchaNicknameRunes = 32
)

// WatchaUserRepo 是观猹登录要用的用户仓储原语（真实实现是 repository.UserRepository）。
type WatchaUserRepo interface {
	// GetByOAuth 按第三方 provider + subject 查已绑定用户，不存在返回 repository.ErrNotFound。
	GetByOAuth(ctx context.Context, provider, subject string) (*model.User, error)
	// CreateWithOAuth 在事务里创建用户与身份绑定；唯一约束冲突返回 repository.ErrDuplicate。
	CreateWithOAuth(ctx context.Context, provider, subject string, u *model.User) error
	// Update 更新用户的辅助字段（最近登录时间），用户不存在返回 repository.ErrNotFound。
	Update(ctx context.Context, id uint64, fields map[string]any) error
	// InsertLoginLog 写一条登录记录。
	InsertLoginLog(ctx context.Context, l *model.UserLoginLog) error
}

// WatchaDeps 是观猹 OAuth 登录服务的依赖与配置。
type WatchaDeps struct {
	Repo         WatchaUserRepo
	Users        *UserService
	Flows        cache.WatchaFlowStore
	Policy       RegisterPolicy
	ClientID     string
	ClientSecret string
	RedirectURI  string

	// 下面三个地址只供测试注入；生产使用观猹官方地址。
	AuthorizeURL string
	TokenURL     string
	UserInfoURL  string

	// ExchangeClient 处理包含 client_secret 的换 token 请求；UserInfoClient 携带 access_token。
	// 两者都使用不记录请求头和请求体的敏感出站客户端。
	ExchangeClient *http.Client
	UserInfoClient *http.Client
	Now            func() time.Time
}

// WatchaService 负责观猹 OAuth2 Authorization Code + PKCE 登录。
// 登录成功后签发本站 JWT；第三方用户信息只用来建立本站账号和昵称，不保存 access_token。
type WatchaService struct {
	WatchaDeps
}

// NewWatchaService 创建观猹登录服务并填充生产默认值。
func NewWatchaService(deps WatchaDeps) *WatchaService {
	if deps.AuthorizeURL == "" {
		deps.AuthorizeURL = watchaAuthorizeURL
	}
	if deps.TokenURL == "" {
		deps.TokenURL = watchaExchangeURL
	}
	if deps.UserInfoURL == "" {
		deps.UserInfoURL = watchaUserInfoURL
	}
	if deps.ExchangeClient == nil {
		deps.ExchangeClient = outbound.SensitiveClient("watcha.oauth", &http.Client{Timeout: watchaHTTPTimeout})
	}
	if deps.UserInfoClient == nil {
		deps.UserInfoClient = outbound.SensitiveClient("watcha.userinfo", &http.Client{Timeout: watchaHTTPTimeout})
	}
	if deps.Now == nil {
		deps.Now = time.Now
	}
	return &WatchaService{WatchaDeps: deps}
}

// Enabled 表示观猹登录配置完整。
func (s *WatchaService) Enabled() bool {
	return s.ClientID != "" && s.ClientSecret != "" && s.RedirectURI != ""
}

// Start 发起观猹登录：生成 PKCE verifier 和一次性 state，返回授权页地址。
// next 只允许站内路径，避免登录完成后被带到外部站点。
func (s *WatchaService) Start(ctx context.Context, next string) (*model.WatchaAuthStartView, error) {
	if !s.Enabled() {
		return nil, errcode.ErrWatchaDisabled
	}
	verifier, err := randomToken(tokenPayVerifierBytes)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	state, err := s.Flows.Issue(ctx, verifier, safeWatchaNext(next), cache.WatchaFlowTTL)
	if err != nil {
		return nil, err
	}
	q := url.Values{}
	q.Set("response_type", "code")
	q.Set("client_id", s.ClientID)
	q.Set("redirect_uri", s.RedirectURI)
	q.Set("scope", watchaScope)
	q.Set("state", state)
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	return &model.WatchaAuthStartView{AuthURL: s.AuthorizeURL + "?" + q.Encode()}, nil
}

// Callback 用观猹回调带回的 state 和 code 完成登录，返回本站 JWT 与发起登录时的站内落点。
func (s *WatchaService) Callback(ctx context.Context, state, code string, meta ClientMeta) (*model.WatchaLoginView, error) {
	if !s.Enabled() {
		return nil, errcode.ErrWatchaDisabled
	}
	flow, ok := s.Flows.Consume(ctx, state)
	if !ok {
		return nil, errcode.ErrWatchaState
	}
	accessToken, err := s.exchange(ctx, code, flow.Verifier)
	if err != nil {
		return nil, err
	}
	profile, err := s.fetchProfile(ctx, accessToken)
	if err != nil {
		return nil, err
	}
	user, err := s.findOrCreateUser(ctx, profile)
	if err != nil {
		return nil, err
	}
	if user.Status == model.UserStatusDisabled {
		return nil, errcode.ErrAccountDisabled
	}
	view, err := s.Users.issueToken(user)
	if err != nil {
		return nil, err
	}
	if err := s.Repo.Update(ctx, user.ID, map[string]any{"last_login_at": s.Now()}); err != nil {
		logger.Warn("更新观猹登录用户最近登录时间失败", zap.Error(err), zap.Uint64("user_id", user.ID))
	}
	s.Users.logLogin(ctx, user.ID, model.LoginKindOAuth, model.LoginResultOK, meta)
	return &model.WatchaLoginView{Token: view.Token, ExpireAt: view.ExpireAt, Role: view.Role, Next: flow.Next}, nil
}

// findOrCreateUser 先按观猹 user_id 找绑定用户；没有绑定时，只有在注册开关打开时才创建新用户。
func (s *WatchaService) findOrCreateUser(ctx context.Context, profile watchaProfile) (*model.User, error) {
	user, err := s.Repo.GetByOAuth(ctx, model.OAuthProviderWatcha, profile.UserID)
	if err == nil {
		return user, nil
	}
	if !errors.Is(err, repository.ErrNotFound) {
		return nil, err
	}
	open, err := s.Policy.RegisterEnabled(ctx)
	if err != nil {
		return nil, err
	}
	if !open {
		return nil, errcode.ErrRegisterClosed
	}
	return s.createUser(ctx, profile)
}

// createUser 创建本站账号并绑定观猹身份。身份唯一约束可能被并发登录抢先写入，此时回查已有用户。
func (s *WatchaService) createUser(ctx context.Context, profile watchaProfile) (*model.User, error) {
	password, err := randomToken(32)
	if err != nil {
		return nil, err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}
	for attempt := 0; attempt < 2; attempt++ {
		username := watchaUsername(profile.UserID)
		if attempt > 0 {
			suffix, suffixErr := randomToken(4)
			if suffixErr != nil {
				return nil, suffixErr
			}
			username = truncateList(username+"_"+suffix, 64)
		}
		user := &model.User{
			Username: username,
			Password: string(hash),
			Nickname: watchaNickname(profile.Nickname),
			Role:     model.RoleUser,
			Status:   model.UserStatusActive,
		}
		if err := s.Repo.CreateWithOAuth(ctx, model.OAuthProviderWatcha, profile.UserID, user); err == nil {
			return user, nil
		} else if !errors.Is(err, repository.ErrDuplicate) {
			return nil, err
		}
		// 身份唯一约束命中：另一个并发回调已经建好了同一个第三方用户，直接复用它。
		existing, lookupErr := s.Repo.GetByOAuth(ctx, model.OAuthProviderWatcha, profile.UserID)
		if lookupErr == nil {
			return existing, nil
		}
		if !errors.Is(lookupErr, repository.ErrNotFound) {
			return nil, lookupErr
		}
	}
	return nil, errcode.ErrWatchaOAuthFailed.WithMsg("创建观猹账号失败，请重试")
}

// exchange 用授权码和 PKCE verifier 换 access_token。请求体含 client_secret，出站客户端不记录请求正文。
func (s *WatchaService) exchange(ctx context.Context, code, verifier string) (string, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", s.RedirectURI)
	form.Set("client_id", s.ClientID)
	form.Set("client_secret", s.ClientSecret)
	form.Set("code_verifier", verifier)
	form.Set("code_challenge_method", "S256")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := s.ExchangeClient.Do(req)
	if err != nil {
		logger.Warn("观猹 token 交换请求失败", zap.Error(err))
		return "", errcode.ErrWatchaOAuthFailed
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, watchaMaxResponse))
	if err != nil {
		return "", errcode.ErrWatchaOAuthFailed
	}
	if resp.StatusCode != http.StatusOK {
		logger.Warn("观猹 token 交换返回非 200", zap.Int("status", resp.StatusCode))
		return "", errcode.ErrWatchaOAuthFailed
	}
	var body struct {
		AccessToken      string `json:"access_token"`
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return "", errcode.ErrWatchaOAuthFailed
	}
	if body.AccessToken == "" {
		logger.Warn("观猹 token 交换未返回 access_token", zap.String("error", body.Error), zap.String("description", body.ErrorDescription))
		return "", errcode.ErrWatchaOAuthFailed
	}
	return body.AccessToken, nil
}

// fetchProfile 用 access_token 查询观猹用户资料。access_token 只放在请求头，不写日志。
func (s *WatchaService) fetchProfile(ctx context.Context, accessToken string) (watchaProfile, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.UserInfoURL, nil)
	if err != nil {
		return watchaProfile{}, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", watchaUserAgent)
	resp, err := s.UserInfoClient.Do(req)
	if err != nil {
		logger.Warn("观猹用户信息请求失败", zap.Error(err))
		return watchaProfile{}, errcode.ErrWatchaOAuthFailed
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, watchaMaxResponse))
	if err != nil {
		return watchaProfile{}, errcode.ErrWatchaOAuthFailed
	}
	if resp.StatusCode != http.StatusOK {
		logger.Warn("观猹用户信息返回非 200", zap.Int("status", resp.StatusCode))
		return watchaProfile{}, errcode.ErrWatchaOAuthFailed
	}
	var body struct {
		StatusCode int    `json:"statusCode"`
		Message    string `json:"message"`
		Data       struct {
			UserID   json.RawMessage `json:"user_id"`
			Nickname string          `json:"nickname"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return watchaProfile{}, errcode.ErrWatchaOAuthFailed
	}
	if body.StatusCode != 0 && body.StatusCode != http.StatusOK {
		logger.Warn("观猹用户信息业务状态失败", zap.Int("status", body.StatusCode), zap.String("message", body.Message))
		return watchaProfile{}, errcode.ErrWatchaOAuthFailed
	}
	userID, err := watchaID(body.Data.UserID)
	if err != nil {
		return watchaProfile{}, errcode.ErrWatchaOAuthFailed
	}
	return watchaProfile{UserID: userID, Nickname: body.Data.Nickname}, nil
}

type watchaProfile struct {
	UserID   string
	Nickname string
}

func watchaID(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "", fmt.Errorf("missing user_id")
	}
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		var number json.Number
		if err := json.Unmarshal(raw, &number); err != nil {
			return "", fmt.Errorf("invalid user_id")
		}
		text = number.String()
	}
	text = strings.TrimSpace(text)
	id, err := strconv.ParseUint(text, 10, 64)
	if err != nil || id == 0 {
		return "", fmt.Errorf("invalid user_id")
	}
	return strconv.FormatUint(id, 10), nil
}

func watchaUsername(userID string) string {
	var b strings.Builder
	for _, r := range userID {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			b.WriteRune(r)
		}
	}
	safe := b.String()
	if safe == "" {
		sum := sha256.Sum256([]byte(userID))
		safe = hex.EncodeToString(sum[:8])
	}
	return truncateList("watcha_"+safe, 64)
}

func watchaNickname(nickname string) string {
	nickname = strings.TrimSpace(nickname)
	if nickname == "" {
		return "观猹用户"
	}
	runes := []rune(nickname)
	if len(runes) > watchaNicknameRunes {
		nickname = string(runes[:watchaNicknameRunes])
	}
	return nickname
}

func truncateList(value string, maxLen int) string {
	if len(value) <= maxLen {
		return value
	}
	return value[:maxLen]
}

// safeWatchaNext 只允许站内绝对路径，防止 OAuth 回调把用户带到开放重定向目标。
func safeWatchaNext(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || !strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") {
		return "/"
	}
	u, err := url.Parse(raw)
	if err != nil || u.IsAbs() || u.Host != "" || strings.HasPrefix(u.Path, "/login") {
		return "/"
	}
	return raw
}
