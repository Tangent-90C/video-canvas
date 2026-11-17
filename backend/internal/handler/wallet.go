package handler

import (
	"github.com/gin-gonic/gin"

	"video-canvas/internal/pkg/response"
	"video-canvas/internal/service"
)

// WalletHandler 处理用户 TokenPay 钱包接口：发起授权、回调后的授权码交换、连接状态、断开。
// 全部要求登录（JWT）；授权页跳转由前端拿 auth_url 后整页跳转。
type WalletHandler struct {
	svc *service.UserWalletService
}

// NewWalletHandler 创建钱包 handler。
func NewWalletHandler(svc *service.UserWalletService) *WalletHandler {
	return &WalletHandler{svc: svc}
}

// Start 发起授权（GET /wallet/tokenpay/start?next=/profile）：返回 TokenDance 授权页地址，前端整页跳转。
func (h *WalletHandler) Start(c *gin.Context) {
	authURL, err := h.svc.Start(c.Request.Context(), currentUserID(c), c.Query("next"))
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, gin.H{"auth_url": authURL})
}

// Connect 用授权回调带回的 flow 与 code 交换并保存 Key（POST /wallet/tokenpay/connect）。
func (h *WalletHandler) Connect(c *gin.Context) {
	var req connectWalletReq
	if !BindJSON(c, &req) {
		return
	}
	v, err := h.svc.Connect(c.Request.Context(), currentUserID(c), req.Flow, req.Code)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, v)
}

// Connection 返回当前钱包状态（GET /wallet/tokenpay/connection）。Key 只回掩码。
func (h *WalletHandler) Connection(c *gin.Context) {
	v, err := h.svc.View(c.Request.Context(), currentUserID(c))
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, v)
}

// Balance 返回钱包余额与已消耗（GET /wallet/tokenpay/balance）：未启用 / 未连接返回降级视图，
// 不报错；上游查询失败时返回旧缓存并标 stale。
func (h *WalletHandler) Balance(c *gin.Context) {
	v, err := h.svc.Balance(c.Request.Context(), currentUserID(c))
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, v)
}

// Usage 返回用户的 TokenDance 调用记录（GET /wallet/tokenpay/usage）：逐条真实花费，
// 是钱包界面所有金额的唯一来源。未启用 / 未连接返回空列表，上游失败返回旧缓存并标 stale。
func (h *WalletHandler) Usage(c *gin.Context) {
	v, err := h.svc.Usage(c.Request.Context(), currentUserID(c))
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, v)
}

// Disconnect 断开钱包（DELETE /wallet/tokenpay/connection）：删除本地保存的 Key 与元数据。
func (h *WalletHandler) Disconnect(c *gin.Context) {
	if err := h.svc.Disconnect(c.Request.Context(), currentUserID(c)); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, nil)
}

// connectWalletReq 是授权码交换的请求体：flow 是发起授权时带进回调地址的 state，code 是上游 302 追加的一次性授权码。
type connectWalletReq struct {
	Flow string `json:"flow" binding:"required,max=256"`
	Code string `json:"code" binding:"required,max=256"`
}
