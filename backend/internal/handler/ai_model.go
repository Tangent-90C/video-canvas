package handler

import (
	"context"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"video-canvas/internal/pkg/logger"
	"video-canvas/internal/pkg/response"
	"video-canvas/internal/provider"
)

// UpstreamPricingProvider 返回公开目录里的上游模型价目，key 是上游模型 slug。
type UpstreamPricingProvider interface {
	// Prices 返回当前缓存的上游价目表；拿不到时由调用方省略价格字段。
	Prices(ctx context.Context) (map[string]provider.UpstreamPricing, error)
}

// AIModelHandler 面向画布的模型清单接口。
type AIModelHandler struct {
	registry provider.Registry
	pricing  UpstreamPricingProvider
}

// NewAIModelHandler 创建模型清单接口；pricing 可选，只有钱包模型需要展示上游价目。
func NewAIModelHandler(registry provider.Registry, pricing ...UpstreamPricingProvider) *AIModelHandler {
	h := &AIModelHandler{registry: registry}
	if len(pricing) > 0 {
		h.pricing = pricing[0]
	}
	return h
}

// listModelsQuery 是 GET /models 的查询参数。
type listModelsQuery struct {
	Kind string `form:"kind" binding:"omitempty,oneof=video image audio text agent" label:"种类"`
}

// List 返回已发布且上架的模型清单（可按 kind 过滤）。
// 只返回 provider.ModelInfo 的公开字段（key / kind / label / hint / vendor / tags / capabilities / wallet / upstream_pricing），
// 不含 params / mapping / provider / 凭证等内部配置——这是它的类型保证，不依赖手工过滤。
func (h *AIModelHandler) List(c *gin.Context) {
	var q listModelsQuery
	if !BindQuery(c, &q) {
		return
	}
	models, err := h.registry.ListModels(c.Request.Context(), q.Kind)
	if err != nil {
		response.Fail(c, err)
		return
	}
	models = walletModels(models)
	if h.pricing != nil && hasWalletModel(models) {
		prices, err := h.pricing.Prices(c.Request.Context())
		if err != nil {
			logger.Warn("拉取上游模型价目失败，模型清单暂不显示上游价格", zap.Error(err))
		} else {
			for i := range models {
				if !models[i].Wallet {
					continue
				}
				if price, ok := prices[models[i].UpstreamModel]; ok {
					p := price
					models[i].UpstreamPricing = &p
				}
			}
		}
	}
	response.OK(c, models)
}

// walletModels 只返回绑定 TokenPay 钱包渠道的模型；平台积分渠道不再向用户端暴露。
func walletModels(models []provider.ModelInfo) []provider.ModelInfo {
	out := make([]provider.ModelInfo, 0, len(models))
	for _, model := range models {
		if model.Wallet {
			out = append(out, model)
		}
	}
	return out
}

// hasWalletModel 判断清单里是否有需要上游价目的钱包模型。
func hasWalletModel(models []provider.ModelInfo) bool {
	for _, model := range models {
		if model.Wallet {
			return true
		}
	}
	return false
}
