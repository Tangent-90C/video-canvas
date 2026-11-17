package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"video-canvas/internal/model"
	"video-canvas/internal/pkg/errcode"
	"video-canvas/internal/pkg/outbound"
	"video-canvas/internal/repository"
)

const (
	// tokenPayRechargeURL 是 TokenDance Agent 支付会话接口，见 https://tokendance.space/docs/agent-payment。
	tokenPayRechargeURL = "https://tokendance.space/portal/api/v1/payment/sessions"
	// tokenPayRechargeTimeout 覆盖创建与查单；充值请求由用户主动等待，但不能无限拖住弹窗。
	tokenPayRechargeTimeout = 15 * time.Second
	// 单次充值金额边界，单位元。
	minWalletRechargeAmount = 1
	maxWalletRechargeAmount = 100_000
	maxWalletSessionIDLen   = 128
)

// tokenPayRechargeHTTP 是充值会话的出站客户端，和余额 / 调用记录分开记录 purpose。
var tokenPayRechargeHTTP = outbound.Client("tokenpay.recharge", &http.Client{Timeout: tokenPayRechargeTimeout})

// WalletRechargeView 是返回给前端的 TokenDance 充值会话；不包含上游 status_url 与任何 Key。
type WalletRechargeView struct {
	ID         string `json:"id"`
	Amount     int    `json:"amount"` // 单位元
	Status     string `json:"status"`
	PaymentURL string `json:"payment_url,omitempty"` // 二维码内容；查单响应没有
	AlipayURL  string `json:"alipay_url,omitempty"`  // 支付宝深链；上游可能不返回
	ExpiresAt  string `json:"expires_at"`            // RFC3339
	PaidAt     string `json:"paid_at,omitempty"`     // RFC3339；未支付为空
}

// walletRechargeUpstream 是 TokenDance 支付会话的响应。时间统一是 Unix 秒。
type walletRechargeUpstream struct {
	ID         string `json:"id"`
	Amount     int    `json:"amount"`
	Status     string `json:"status"`
	PaymentURL string `json:"payment_url"`
	AlipayURL  string `json:"alipay_url"`
	ExpiredAt  int64  `json:"expired_at"`
	PaidAt     int64  `json:"paid_at"`
}

// Recharge 用当前用户的钱包 Key 创建 TokenDance 充值会话。
func (s *UserWalletService) Recharge(ctx context.Context, userID uint64, amount int) (*WalletRechargeView, error) {
	if amount < minWalletRechargeAmount || amount > maxWalletRechargeAmount {
		return nil, errcode.ErrInvalidParams.WithMsg("充值金额需为 1–100000 元的整数")
	}
	key, err := s.rechargeKey(ctx, userID)
	if err != nil {
		return nil, err
	}
	payload, _ := json.Marshal(map[string]int{"amount": amount})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.rechargeURL, strings.NewReader(string(payload)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	up, err := s.doRechargeRequest(req, http.StatusCreated)
	if err != nil {
		return nil, err
	}
	if up.ID == "" {
		return nil, errcode.ErrInternal.WithMsg("TokenDance 充值会话响应不完整，请稍后重试")
	}
	if up.Amount <= 0 {
		up.Amount = amount
	}
	view := rechargeView(*up)
	return &view, nil
}

// RechargeStatus 查询充值会话。上游 status_url 不透传给浏览器，这里始终按固定接口 + 会话 ID 查询。
func (s *UserWalletService) RechargeStatus(ctx context.Context, userID uint64, sessionID string) (*WalletRechargeView, error) {
	if !validWalletSessionID(sessionID) {
		return nil, errcode.ErrInvalidParams.WithMsg("充值会话 ID 不合法")
	}
	key, err := s.rechargeKey(ctx, userID)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.rechargeURL+"/"+url.PathEscape(sessionID), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Accept", "application/json")
	up, err := s.doRechargeRequest(req, http.StatusOK)
	if err != nil {
		return nil, err
	}
	if up.ID == "" {
		up.ID = sessionID
	}
	if up.Status == "paid" {
		s.forgetBalance(userID)
	}
	view := rechargeView(*up)
	return &view, nil
}

// rechargeKey 做充值接口共用的启用 / 连接 / Key 检查。
func (s *UserWalletService) rechargeKey(ctx context.Context, userID uint64) (string, error) {
	if !s.Enabled() {
		return "", errcode.ErrWalletDisabled
	}
	if _, err := s.repo.GetByUser(ctx, userID); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return "", errcode.ErrWalletNotConnected
		}
		return "", err
	}
	key, err := s.secrets.Get(ctx, model.WalletSecretName(userID))
	if err != nil || key == "" {
		return "", errcode.ErrWalletNotConnected
	}
	return key, nil
}

// doRechargeRequest 发请求并解析响应；401/403 说明钱包授权失效，其它上游失败统一给用户可重试文案。
func (s *UserWalletService) doRechargeRequest(req *http.Request, wantStatus int) (*walletRechargeUpstream, error) {
	resp, err := tokenPayRechargeHTTP.Do(req)
	if err != nil {
		return nil, errcode.ErrInternal.WithMsg("TokenDance 充值服务暂时不可用，请稍后重试")
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, errcode.ErrInternal.WithMsg("TokenDance 充值服务暂时不可用，请稍后重试")
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, errcode.ErrWalletNotConnected.WithMsg("TokenDance 钱包授权已失效，请重新连接钱包")
	}
	if resp.StatusCode != wantStatus {
		return nil, errcode.ErrInternal.WithMsg("TokenDance 充值服务暂时不可用，请稍后重试")
	}
	var up walletRechargeUpstream
	if err := json.Unmarshal(raw, &up); err != nil {
		return nil, errcode.ErrInternal.WithMsg("TokenDance 充值服务响应无法解析，请稍后重试")
	}
	return &up, nil
}

// rechargeView 把上游字段换成前端契约：状态收敛、Unix 时间转 RFC3339。
func rechargeView(up walletRechargeUpstream) WalletRechargeView {
	return WalletRechargeView{
		ID: up.ID, Amount: up.Amount, Status: rechargeStatus(up.Status),
		PaymentURL: up.PaymentURL, AlipayURL: up.AlipayURL,
		ExpiresAt: unixRFC3339(up.ExpiredAt), PaidAt: unixRFC3339(up.PaidAt),
	}
}

// rechargeStatus 把未知的新状态保留为 unknown，前端继续显示处理中而不是误判为失败。
func rechargeStatus(status string) string {
	switch status {
	case "pending", "paid", "failed", "closed", "refunded":
		return status
	default:
		return "unknown"
	}
}

// unixRFC3339 把上游 Unix 秒转成 UTC RFC3339；0 表示没有该时间。
func unixRFC3339(seconds int64) string {
	if seconds <= 0 {
		return ""
	}
	return time.Unix(seconds, 0).UTC().Format(time.RFC3339)
}

// validWalletSessionID 只允许 URL 安全的短 ID，防止把查单接口变成任意路径代理。
func validWalletSessionID(id string) bool {
	if id == "" || len(id) > maxWalletSessionIDLen {
		return false
	}
	for _, ch := range id {
		if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '-' || ch == '_' {
			continue
		}
		return false
	}
	return true
}
