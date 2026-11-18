package cache_test

import (
	"context"
	"testing"
	"time"

	. "video-canvas/internal/cache"
)

// 观猹登录 state 只能消费一次，并把 PKCE verifier 与站内落点一起带回。
func TestWatchaFlowIssueConsume(t *testing.T) {
	store := NewMemoryWatchaFlowStore(time.Now)
	ctx := context.Background()

	state, err := store.Issue(ctx, "verifier-abc", "/canvas/123?x=1", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if state == "" {
		t.Fatal("state 不应为空")
	}
	flow, ok := store.Consume(ctx, state)
	if !ok || flow.Verifier != "verifier-abc" || flow.Next != "/canvas/123?x=1" {
		t.Fatalf("消费失败：ok=%v flow=%+v", ok, flow)
	}
	if _, ok := store.Consume(ctx, state); ok {
		t.Fatal("同一个 state 被消费了两次")
	}
}

// 过期的观猹流程不能用于登录。
func TestWatchaFlowExpired(t *testing.T) {
	now := time.Now()
	store := NewMemoryWatchaFlowStore(func() time.Time { return now })
	state, err := store.Issue(context.Background(), "verifier", "/", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Minute)
	if _, ok := store.Consume(context.Background(), state); ok {
		t.Fatal("过期流程不应消费成功")
	}
}
