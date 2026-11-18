package cache

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"video-canvas/internal/pkg/logger"
)

// WatchaFlowTTL 是观猹 OAuth 登录流程的有效期：授权码和 state 都只应短时间有效。
const WatchaFlowTTL = 10 * time.Minute

const watchaFlowKeyPrefix = "watcha:flow:"

// WatchaFlow 是一次登录授权流程：PKCE verifier 和发起登录时的站内落点。
type WatchaFlow struct {
	Verifier string `json:"verifier"`
	Next     string `json:"next"`
}

// WatchaFlowStore 保存观猹登录流程，按 state 一次性消费，防止重放和 CSRF。
type WatchaFlowStore interface {
	// Issue 生成随机 state 并保存流程，返回 state。
	Issue(ctx context.Context, verifier, next string, ttl time.Duration) (string, error)
	// Consume 原子取出并删除流程；不存在、过期或已消费返回 ok=false。
	Consume(ctx context.Context, state string) (WatchaFlow, bool)
}

// NewWatchaFlowStore 按 Redis 是否启用选择实现；未启用 Redis 时使用进程内存，只适合单实例。
func NewWatchaFlowStore(rdb *redis.Client) WatchaFlowStore {
	if rdb == nil {
		return NewMemoryWatchaFlowStore(time.Now)
	}
	return NewRedisWatchaFlowStore(rdb)
}

func encodeWatchaFlow(flow WatchaFlow) string {
	raw, _ := json.Marshal(flow)
	return string(raw)
}

func decodeWatchaFlow(raw string) (WatchaFlow, bool) {
	var flow WatchaFlow
	if err := json.Unmarshal([]byte(raw), &flow); err != nil || flow.Verifier == "" {
		return WatchaFlow{}, false
	}
	return flow, true
}

// RedisWatchaFlowStore 把登录流程存在 Redis，key 带 TTL。
type RedisWatchaFlowStore struct {
	rdb *redis.Client
}

// NewRedisWatchaFlowStore 创建 Redis 版 WatchaFlowStore。
func NewRedisWatchaFlowStore(rdb *redis.Client) *RedisWatchaFlowStore {
	return &RedisWatchaFlowStore{rdb: rdb}
}

// consumeWatchaFlowScript 原子读取并删除 state，避免并发回调重复消费。
var consumeWatchaFlowScript = redis.NewScript(`
local v = redis.call('GET', KEYS[1])
if v then redis.call('DEL', KEYS[1]) end
return v
`)

// Issue 生成并保存一次 Redis OAuth 流程。
func (s *RedisWatchaFlowStore) Issue(ctx context.Context, verifier, next string, ttl time.Duration) (string, error) {
	state, err := newOAuthState()
	if err != nil {
		return "", err
	}
	if err := s.rdb.Set(ctx, watchaFlowKeyPrefix+state, encodeWatchaFlow(WatchaFlow{Verifier: verifier, Next: next}), ttl).Err(); err != nil {
		return "", err
	}
	return state, nil
}

// Consume 原子读取并删除 Redis 登录流程。
func (s *RedisWatchaFlowStore) Consume(ctx context.Context, state string) (WatchaFlow, bool) {
	if state == "" {
		return WatchaFlow{}, false
	}
	raw, err := consumeWatchaFlowScript.Run(ctx, s.rdb, []string{watchaFlowKeyPrefix + state}).Text()
	if err != nil {
		if !errors.Is(err, redis.Nil) {
			logger.Warn("Redis 消费观猹 OAuth 授权流程失败", zap.Error(err))
		}
		return WatchaFlow{}, false
	}
	return decodeWatchaFlow(raw)
}

// MemoryWatchaFlowStore 是 Redis 关闭时的进程内存实现。
type MemoryWatchaFlowStore struct {
	mu        sync.Mutex
	items     map[string]watchaMemoryFlow
	lastSweep time.Time
	now       func() time.Time
}

type watchaMemoryFlow struct {
	flow    WatchaFlow
	expires time.Time
}

// NewMemoryWatchaFlowStore 创建内存版 WatchaFlowStore；now 可注入便于测试过期。
func NewMemoryWatchaFlowStore(now func() time.Time) *MemoryWatchaFlowStore {
	return &MemoryWatchaFlowStore{items: make(map[string]watchaMemoryFlow), now: now}
}

// Issue 生成并保存一次内存 OAuth 流程。
func (s *MemoryWatchaFlowStore) Issue(_ context.Context, verifier, next string, ttl time.Duration) (string, error) {
	state, err := newOAuthState()
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	s.sweepLocked(now)
	s.items[state] = watchaMemoryFlow{flow: WatchaFlow{Verifier: verifier, Next: next}, expires: now.Add(ttl)}
	return state, nil
}

// Consume 读取并删除内存流程；取出即删，过期或已消费都拿不到。
func (s *MemoryWatchaFlowStore) Consume(_ context.Context, state string) (WatchaFlow, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	s.sweepLocked(now)
	item, ok := s.items[state]
	if !ok {
		return WatchaFlow{}, false
	}
	delete(s.items, state)
	if !now.Before(item.expires) {
		return WatchaFlow{}, false
	}
	return item.flow, true
}

func (s *MemoryWatchaFlowStore) sweepLocked(now time.Time) {
	if now.Sub(s.lastSweep) < oauthMemSweepEvery {
		return
	}
	s.lastSweep = now
	for key, item := range s.items {
		if !now.Before(item.expires) {
			delete(s.items, key)
		}
	}
}
