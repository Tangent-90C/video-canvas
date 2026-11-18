package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"video-canvas/internal/pkg/outbound"
	"video-canvas/internal/provider"
)

const (
	upstreamPricingURL   = "https://tokendance.space/portal/api/models/all"
	upstreamPricingTTL   = 5 * time.Minute
	upstreamPricingLimit = 4 << 20
)

// UpstreamPricingService 读取并缓存 TokenDance 的公开模型价目。
type UpstreamPricingService struct {
	url    string
	client *http.Client
	now    func() time.Time

	mu     sync.Mutex
	cache  map[string]provider.UpstreamPricing
	loaded time.Time
}

// NewUpstreamPricingService 创建价目服务；url 为空时使用 TokenDance 官方公开目录。
func NewUpstreamPricingService(url string) *UpstreamPricingService {
	if url == "" {
		url = upstreamPricingURL
	}
	return &UpstreamPricingService{
		url:    url,
		client: outbound.Client("tokenpay.pricing", &http.Client{Timeout: 10 * time.Second}),
		now:    time.Now,
	}
}

// SetNow 替换时钟，供测试控制缓存过期。
func (s *UpstreamPricingService) SetNow(now func() time.Time) {
	if now != nil {
		s.now = now
	}
}

type upstreamPricingEnvelope struct {
	Models []struct {
		Slug    string `json:"slug"`
		Pricing struct {
			Items []upstreamPriceItem `json:"items"`
		} `json:"pricing"`
	} `json:"models"`
}

type upstreamPriceItem struct {
	ID    string `json:"id"`
	Plans []struct {
		Unit string `json:"unit"`
		Rate string `json:"rate"`
	} `json:"plans"`
	SKUs []struct {
		Rate string `json:"rate"`
		Unit string `json:"unit"`
	} `json:"skus"`
}

// Prices 返回 slug -> 公开价目；一次请求全量缓存 5 分钟。
func (s *UpstreamPricingService) Prices(ctx context.Context) (map[string]provider.UpstreamPricing, error) {
	s.mu.Lock()
	if s.cache != nil && s.now().Sub(s.loaded) < upstreamPricingTTL {
		cache := cloneUpstreamPrices(s.cache)
		s.mu.Unlock()
		return cache, nil
	}
	s.mu.Unlock()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("拉取 TokenDance 模型价目失败：%w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("拉取 TokenDance 模型价目失败：HTTP %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, upstreamPricingLimit))
	if err != nil {
		return nil, err
	}
	var data upstreamPricingEnvelope
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, err
	}
	prices := make(map[string]provider.UpstreamPricing, len(data.Models))
	for _, model := range data.Models {
		if model.Slug == "" || len(model.Pricing.Items) == 0 {
			continue
		}
		if price, ok := normalizeUpstreamPricing(model.Pricing.Items); ok {
			prices[model.Slug] = price
		}
	}
	s.mu.Lock()
	s.cache = cloneUpstreamPrices(prices)
	s.loaded = s.now()
	cache := cloneUpstreamPrices(prices)
	s.mu.Unlock()
	return cache, nil
}

func cloneUpstreamPrices(in map[string]provider.UpstreamPricing) map[string]provider.UpstreamPricing {
	out := make(map[string]provider.UpstreamPricing, len(in))
	for key, price := range in {
		out[key] = price
	}
	return out
}

// normalizeUpstreamPricing 把协议 / SKU 价格压成模型选择器能读的一条摘要。
func normalizeUpstreamPricing(items []upstreamPriceItem) (provider.UpstreamPricing, bool) {
	input, output := tokenRates(items)
	if input != nil || output != nil {
		parts := make([]string, 0, 2)
		if input != nil {
			parts = append(parts, "输入 ¥"+formatPrice(*input))
		}
		if output != nil {
			parts = append(parts, "输出 ¥"+formatPrice(*output))
		}
		return provider.UpstreamPricing{
			Currency: "CNY", Label: strings.Join(parts, " / ") + " / 百万 Token",
			Input: input, Output: output, Unit: "millionTokens",
		}, true
	}
	lo, hi, unit, ok := primaryOutputPrice(items)
	if ok {
		amount := "¥" + formatPrice(lo)
		if hi > lo {
			amount += "–" + formatPrice(hi)
		}
		label := amount + " / " + priceUnitLabel(unit)
		return provider.UpstreamPricing{Currency: "CNY", Label: label, Min: &lo, Max: &hi, Unit: unit}, true
	}
	return provider.UpstreamPricing{}, false
}

// primaryOutputPrice 从一组 SKU 中选用户实际购买的主产物价：优先生成图 / 输出视频，跳过输入图和参考视频秒数。
// 同一个模型常常同时有输入素材费和输出素材费，取第一项会把 Seedream 的 ¥0.05 输入图价误显示成生成价。
func primaryOutputPrice(items []upstreamPriceItem) (float64, float64, string, bool) {
	bestScore := -1 << 30
	var lo, hi float64
	var unit string
	ok := false
	for _, item := range items {
		if len(item.Plans) == 0 && len(item.SKUs) == 0 {
			continue
		}
		itemLo, itemHi, itemUnit, itemOK := priceRange(item)
		if !itemOK {
			continue
		}
		score := upstreamPricePriority(item.ID, itemUnit)
		if !ok || score > bestScore {
			bestScore = score
			lo, hi, unit, ok = itemLo, itemHi, itemUnit, true
		}
	}
	return lo, hi, unit, ok
}

func upstreamPricePriority(id, unit string) int {
	s := strings.ToLower(id)
	score := 0
	if strings.Contains(s, "input") || strings.Contains(s, "cached") || strings.Contains(s, "cache_") {
		score -= 100
	}
	if strings.Contains(s, "generated") || strings.Contains(s, "output") || strings.Contains(s, "completion") {
		score += 60
	}
	if strings.Contains(s, "video_seconds") || strings.Contains(s, "video-seconds") {
		score += 50
	}
	if strings.Contains(s, "video_input_seconds") {
		score -= 40
	}
	if unit == "seconds" {
		score += 20
	}
	if strings.Contains(s, "images") {
		score += 10
	}
	return score
}

func tokenRates(items []upstreamPriceItem) (input, output *float64) {
	for _, item := range items {
		lower := strings.ToLower(item.ID)
		if len(item.SKUs) > 0 || strings.Contains(lower, "cached") || strings.Contains(lower, "cache_") {
			continue
		}
		rate, unit, ok := firstRate(item)
		if !ok {
			continue
		}
		switch {
		case strings.Contains(lower, "input_tokens"):
			if input == nil && unit == "millionTokens" {
				input = &rate
			}
		case strings.Contains(lower, "completion_tokens"), strings.Contains(lower, "output_tokens"):
			if output == nil && unit == "millionTokens" {
				output = &rate
			}
		}
	}
	return input, output
}

func firstRate(item upstreamPriceItem) (float64, string, bool) {
	if len(item.Plans) > 0 {
		rate, err := strconv.ParseFloat(item.Plans[0].Rate, 64)
		return rate, item.Plans[0].Unit, err == nil
	}
	if len(item.SKUs) > 0 {
		rate, err := strconv.ParseFloat(item.SKUs[0].Rate, 64)
		return rate, item.SKUs[0].Unit, err == nil
	}
	return 0, "", false
}

func priceRange(item upstreamPriceItem) (lo, hi float64, unit string, ok bool) {
	parse := func(raw, u string) {
		rate, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return
		}
		if !ok || rate < lo {
			lo = rate
		}
		if !ok || rate > hi {
			hi = rate
		}
		unit = u
		ok = true
	}
	for _, plan := range item.Plans {
		parse(plan.Rate, plan.Unit)
	}
	for _, sku := range item.SKUs {
		parse(sku.Rate, sku.Unit)
	}
	return lo, hi, unit, ok
}

func formatPrice(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
}

func priceUnitLabel(unit string) string {
	switch unit {
	case "millionTokens":
		return "百万 Token"
	case "kCharacters":
		return "千字符"
	case "images":
		return "张"
	case "seconds":
		return "秒"
	case "counts":
		return "次"
	case "characters":
		return "字符"
	default:
		return unit
	}
}
