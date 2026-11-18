package handler

import (
	"context"

	"github.com/gin-gonic/gin"

	"video-canvas/internal/model"
	"video-canvas/internal/pkg/response"
	"video-canvas/internal/service"
)

// WatchaService 是观猹 OAuth2 登录 handler 依赖的服务能力。
type WatchaService interface {
	// Start 生成授权页地址；next 是登录成功后回到的站内路径。
	Start(ctx context.Context, next string) (*model.WatchaAuthStartView, error)
	// Callback 用一次性 state 和 code 完成登录并返回本站 JWT。
	Callback(ctx context.Context, state, code string, meta service.ClientMeta) (*model.WatchaLoginView, error)
}

// WatchaHandler 处理观猹 OAuth2 登录的发起与回调。
type WatchaHandler struct {
	svc WatchaService
}

// NewWatchaHandler 创建观猹登录 handler。
func NewWatchaHandler(svc WatchaService) *WatchaHandler {
	return &WatchaHandler{svc: svc}
}

// Start 发起观猹授权（GET /auth/watcha/start?next=/）：返回授权页地址，前端整页跳转。
func (h *WatchaHandler) Start(c *gin.Context) {
	view, err := h.svc.Start(c.Request.Context(), c.Query("next"))
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, view)
}

// Callback 完成观猹授权（POST /auth/watcha/callback）：前端把回调参数换成 JWT，避免 token 出现在 URL 中。
func (h *WatchaHandler) Callback(c *gin.Context) {
	var req model.WatchaCallbackReq
	if !BindJSON(c, &req) {
		return
	}
	view, err := h.svc.Callback(c.Request.Context(), req.Flow, req.Code, clientMeta(c))
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, view)
}
