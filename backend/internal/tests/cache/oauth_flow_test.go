package cache_test

import (
	"context"
	"testing"
	"time"

	. "video-canvas/internal/cache"
)

// TestOAuthFlowIssueConsume 发起 → 用发起人的身份消费：返回 verifier，且 state 一次性（第二次消费拿不到）。
func TestOAuthFlowIssueConsume(t *testing.T) {
	store := NewMemoryOAuthFlowStore(time.Now)
	ctx := context.Background()

	state, err := store.Issue(ctx, "verifier-abc", 7, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if state == "" {
		t.Fatal("state 不应为空")
	}
	flow, ok := store.Consume(ctx, state, 7)
	if !ok || flow.Verifier != "verifier-abc" || flow.UserID != 7 {
		t.Fatalf("消费失败：ok=%v flow=%+v", ok, flow)
	}
	// 一次性：消费后同一个 state 再也拿不到
	if _, ok = store.Consume(ctx, state, 7); ok {
		t.Fatal("同一个 state 被消费了两次")
	}
}

// TestOAuthFlowWrongUser 别的用户拿到回调也换不走：发起人不匹配按无效处理。
// 消费即销毁（防重放），所以这次失败的尝试之后，发起人也要重新发起连接才能拿到新流程。
func TestOAuthFlowWrongUser(t *testing.T) {
	store := NewMemoryOAuthFlowStore(time.Now)
	ctx := context.Background()

	state, err := store.Issue(ctx, "verifier-xyz", 7, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := store.Consume(ctx, state, 8); ok {
		t.Fatal("别人的用户 ID 不应消费成功")
	}
	if _, ok := store.Consume(ctx, state, 7); ok {
		t.Fatal("消费即销毁：发起人本人也不应再消费成功")
	}
}

// TestOAuthFlowExpired 过了 ttl 的流程消费不到（内存实现的时钟可注入，直接拨快）。
func TestOAuthFlowExpired(t *testing.T) {
	now := time.Now()
	store := NewMemoryOAuthFlowStore(func() time.Time { return now })
	ctx := context.Background()

	state, err := store.Issue(ctx, "verifier-old", 7, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Minute)
	if _, ok := store.Consume(ctx, state, 7); ok {
		t.Fatal("过期流程不应消费成功")
	}
}
