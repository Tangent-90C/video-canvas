package agent

import (
	"context"

	"video-canvas/internal/model"
	"video-canvas/internal/provider"
	"video-canvas/internal/provider/modelcfg"
)

// UpstreamPricingProvider 返回 TokenDance 公开模型价目；模型选择器只展示这份真实价目。
type UpstreamPricingProvider interface {
	Prices(ctx context.Context) (map[string]provider.UpstreamPricing, error)
}

// RegistryAgentModels 把后台已发布、已上架的 agent 类型钱包模型，作为画布 Agent 可选的大语言模型。
type RegistryAgentModels struct {
	reg     provider.Registry
	pricing UpstreamPricingProvider
}

// NewRegistryAgentModels 创建基于模型注册表的 Agent 模型清单；pricing 可选。
func NewRegistryAgentModels(reg provider.Registry, pricing ...UpstreamPricingProvider) *RegistryAgentModels {
	m := &RegistryAgentModels{reg: reg}
	if len(pricing) > 0 {
		m.pricing = pricing[0]
	}
	return m
}

// List 返回当前可用的 Agent 钱包模型。注册表只给已发布、已上架且渠道可用的模型；
// 平台积分渠道的模型不再出现在用户端。
func (m *RegistryAgentModels) List(ctx context.Context) ([]model.AgentModelView, error) {
	infos, err := m.reg.ListModels(ctx, modelcfg.KindAgent)
	if err != nil {
		return nil, err
	}
	prices := map[string]provider.UpstreamPricing{}
	if m.pricing != nil && hasWalletInfo(infos) {
		if got, err := m.pricing.Prices(ctx); err == nil {
			prices = got
		}
	}
	out := make([]model.AgentModelView, 0, len(infos))
	for _, in := range infos {
		if !in.Wallet {
			continue
		}
		view := model.AgentModelView{
			Key: in.Key, Name: in.Label, Vendor: in.Vendor,
			Vision: in.Capabilities.Vision, Video: in.Capabilities.Video,
			UpstreamModel: in.UpstreamModel,
		}
		if c := in.Capabilities.Context; c != nil {
			view.ContextWindow = c.Window
		}
		if p, ok := prices[in.UpstreamModel]; ok {
			view.PriceLabel = p.Label
		}
		out = append(out, view)
	}
	return out, nil
}

// hasWalletInfo 判断清单里是否有需要上游价目的钱包模型。
func hasWalletInfo(infos []provider.ModelInfo) bool {
	for _, info := range infos {
		if info.Wallet {
			return true
		}
	}
	return false
}

// walletModelInfos 只保留绑定 TokenPay 钱包渠道的模型；用户侧工具、选模型提问和模型引用都必须用它收口。
func walletModelInfos(infos []provider.ModelInfo) []provider.ModelInfo {
	out := make([]provider.ModelInfo, 0, len(infos))
	for _, info := range infos {
		if info.Wallet {
			out = append(out, info)
		}
	}
	return out
}
