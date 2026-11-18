package service_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	. "video-canvas/internal/service"
)

func TestUpstreamPricingServiceNormalizesAndCaches(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"models": []any{
			map[string]any{"slug": "deepseek-v4.1-flash", "pricing": map[string]any{"items": []any{
				map[string]any{"id": "openai:chat-completions:input_tokens", "plans": []any{map[string]any{"unit": "millionTokens", "rate": "0.8"}}},
				map[string]any{"id": "openai:chat-completions:completion_tokens", "plans": []any{map[string]any{"unit": "millionTokens", "rate": "3.2"}}},
				map[string]any{"id": "openai:chat-completions:cached_tokens", "plans": []any{map[string]any{"unit": "millionTokens", "rate": "0.016"}}},
			}}},
			map[string]any{"slug": "seedream-5.0-lite", "pricing": map[string]any{"items": []any{
				map[string]any{"id": "ark:image-generations:generated_images", "plans": []any{map[string]any{"unit": "images", "rate": "0.22"}}},
			}}},
			map[string]any{"slug": "seedance-2.0", "pricing": map[string]any{"items": []any{
				map[string]any{"id": "seedance:generations:completion_tokens", "skus": []any{
					map[string]any{"unit": "millionTokens", "rate": "16"},
					map[string]any{"unit": "millionTokens", "rate": "51"},
				}},
			}}},
		}})
	}))
	defer srv.Close()

	svc := NewUpstreamPricingService(srv.URL)
	now := time.Now()
	svc.SetNow(func() time.Time { return now })
	ctx := context.Background()
	prices, err := svc.Prices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := prices["deepseek-v4.1-flash"].Label; got != "输入 ¥0.8 / 输出 ¥3.2 / 百万 Token" {
		t.Fatalf("文本价目不对：%q", got)
	}
	if got := prices["seedream-5.0-lite"].Label; got != "¥0.22 / 张" {
		t.Fatalf("图片价目不对：%q", got)
	}
	if got := prices["seedance-2.0"].Label; got != "¥16–51 / 百万 Token" {
		t.Fatalf("视频 SKU 区间不对：%q", got)
	}

	now = now.Add(4 * time.Minute)
	if _, err := svc.Prices(ctx); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("TTL 内应命中缓存，实际请求 %d 次", calls.Load())
	}
	now = now.Add(2 * time.Minute)
	if _, err := svc.Prices(ctx); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatalf("TTL 过期后应重新请求，实际请求 %d 次", calls.Load())
	}
}

func TestUpstreamPricingServiceDoesNotUseExpiredCacheAfterRefreshFailure(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) > 1 {
			http.Error(w, "upstream unavailable", http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"models":[{"slug":"m","pricing":{"items":[{"id":"openai:chat-completions:input_tokens","plans":[{"unit":"millionTokens","rate":"1"}]}]}}]}`))
	}))
	defer srv.Close()

	svc := NewUpstreamPricingService(srv.URL)
	now := time.Now()
	svc.SetNow(func() time.Time { return now })
	ctx := context.Background()
	if _, err := svc.Prices(ctx); err != nil {
		t.Fatal(err)
	}
	now = now.Add(6 * time.Minute)
	if prices, err := svc.Prices(ctx); err == nil || prices != nil {
		t.Fatalf("刷新失败时不能把过期缓存当实时价目返回：prices=%+v err=%v", prices, err)
	}
}
