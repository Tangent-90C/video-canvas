package stepkit_test

import (
	"context"
	"errors"
	"testing"

	"video-canvas/internal/llmgateway"
	"video-canvas/internal/pkg/errcode"
	"video-canvas/internal/pkg/logger"
	"video-canvas/internal/provider"
	"video-canvas/internal/stepkit"
)

type gatewaySecrets map[string]string

func (s gatewaySecrets) Get(_ context.Context, name string) (string, error) {
	if value := s[name]; value != "" {
		return value, nil
	}
	return "", context.Canceled
}

type gatewayStreamer struct {
	target llmgateway.Target
}

func (s *gatewayStreamer) Stream(_ context.Context, target llmgateway.Target, _ llmgateway.Request, _ func(llmgateway.Event)) (*llmgateway.Result, error) {
	s.target = target
	return &llmgateway.Result{Text: "ok"}, nil
}

func gatewaySnapshot(wallet bool) *provider.Snapshot {
	return &provider.Snapshot{
		Model:   provider.ModelSnapshot{Key: "m1", Kind: "text", UpstreamModel: "up"},
		Channel: provider.ChannelSnapshot{Key: "tokenpay", BaseURL: "https://example.com", Wallet: wallet},
	}
}

func TestGatewayCallerWalletUsesUserSecret(t *testing.T) {
	streamer := &gatewayStreamer{}
	caller := &stepkit.GatewayCaller{Streamer: streamer, Secrets: gatewaySecrets{
		"channel:tokenpay": "product-key",
		"wallet:7":         "user-key",
	}}
	ctx, _ := logger.BeginRun(context.Background(), logger.RunRef{Kind: "mkt", UserID: 7})

	if _, err := caller.Complete(ctx, gatewaySnapshot(true), nil); err != nil {
		t.Fatal(err)
	}
	if streamer.target.APIKey != "user-key" {
		t.Fatalf("钱包模型应使用 wallet:<uid> 的 Key，实际 %q", streamer.target.APIKey)
	}
}

func TestGatewayCallerWalletUsesAuthenticatedRequestUser(t *testing.T) {
	streamer := &gatewayStreamer{}
	caller := &stepkit.GatewayCaller{Streamer: streamer, Secrets: gatewaySecrets{
		"channel:tokenpay": "product-key",
		"wallet:7":         "user-key",
	}}
	ctx := logger.WithUser(context.Background(), 7)

	if _, err := caller.Complete(ctx, gatewaySnapshot(true), nil); err != nil {
		t.Fatal(err)
	}
	if streamer.target.APIKey != "user-key" {
		t.Fatalf("同步 HTTP 路径的钱包模型也应使用 wallet:<uid>，实际 %q", streamer.target.APIKey)
	}
}

func TestGatewayCallerPlainChannelFailsClosed(t *testing.T) {
	streamer := &gatewayStreamer{}
	caller := &stepkit.GatewayCaller{Streamer: streamer, Secrets: gatewaySecrets{
		"channel:tokenpay": "product-key",
		"wallet:7":         "user-key",
	}}
	ctx, _ := logger.BeginRun(context.Background(), logger.RunRef{Kind: "mkt", UserID: 7})

	if _, err := caller.Complete(ctx, gatewaySnapshot(false), nil); err == nil {
		t.Fatal("非钱包模型必须失败，不能回退到产品方渠道 Key")
	}
	if streamer.target.APIKey != "" {
		t.Fatalf("非钱包模型不应发起请求，实际 Key %q", streamer.target.APIKey)
	}
}

func TestGatewayCallerMissingWalletKeyReturnsConnectGuidance(t *testing.T) {
	streamer := &gatewayStreamer{}
	caller := &stepkit.GatewayCaller{Streamer: streamer, Secrets: gatewaySecrets{}}
	ctx := logger.WithUser(context.Background(), 7)

	_, err := caller.Complete(ctx, gatewaySnapshot(true), nil)
	var business *errcode.Error
	if !errors.As(err, &business) || business.Code != errcode.ErrWalletNotConnected.Code {
		t.Fatalf("缺少用户钱包 Key 时应返回 40016 连接引导，实际 %v", err)
	}
}
