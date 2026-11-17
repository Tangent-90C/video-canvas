package cache

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"video-canvas/internal/pkg/logger"
)

// OAuthFlowTTL 是授权流程的有效期：与上游一次性授权码的有效期（10 分钟）对齐。
const OAuthFlowTTL = 10 * time.Minute

const (
	oauthFlowBytes     = 32               // 随机字节数，base64url 编码后 43 个字符
	oauthFlowKeyPrefix = "oauth:flow:"    // Redis key 前缀
	oauthMemSweepEvery = 10 * time.Second // 内存实现里，最多每隔这么久顺带清理一次过期项
)

// OAuthFlow 一次授权流程携带的数据：PKCE verifier 与发起授权的用户。
type OAuthFlow struct {
	Verifier string
	UserID   uint64
}

// OAuthFlowStore 管理 OAuth 授权流程：发起时保存（state -> verifier + 发起用户），
// 回调时一次性取出。防重放：同一个 state 只能消费一次，且消费即销毁——
// 发起人不匹配（state 被别人的浏览器带回）同样销毁，发起人需要重新发起连接。
type OAuthFlowStore interface {
	// Issue 生成随机 state 并保存流程，ttl 后过期。返回 state。
	Issue(ctx context.Context, verifier string, userID uint64, ttl time.Duration) (string, error)
	// Consume 校验并消费流程：一次性，取出即删；不存在、已用过、已过期、发起人不是 uid 都返回 ok=false。
	Consume(ctx context.Context, state string, uid uint64) (OAuthFlow, bool)
}

// NewOAuthFlowStore 按 Redis 是否启用选择实现：rdb 为 nil（Redis 关闭）时降级为进程内存。
// 内存实现只在单实例下正确；多实例部署必须开启 Redis。
func NewOAuthFlowStore(rdb *redis.Client) OAuthFlowStore {
	if rdb == nil {
		return NewMemoryOAuthFlowStore(time.Now)
	}
	return NewRedisOAuthFlowStore(rdb)
}

// newOAuthState 生成 32 字节随机数的 base64url（无填充）字符串。
func newOAuthState() (string, error) {
	b := make([]byte, oauthFlowBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// encodeFlow / decodeFlow 把流程编码成 "verifier|userID" 文本存取。
func encodeFlow(flow OAuthFlow) string {
	return flow.Verifier + "|" + strconv.FormatUint(flow.UserID, 10)
}

func decodeFlow(raw string) (OAuthFlow, bool) {
	verifier, uidText, found := strings.Cut(raw, "|")
	if !found || verifier == "" {
		return OAuthFlow{}, false
	}
	uid, err := strconv.ParseUint(uidText, 10, 64)
	if err != nil || uid == 0 {
		return OAuthFlow{}, false
	}
	return OAuthFlow{Verifier: verifier, UserID: uid}, true
}

// ---------------------------------------------------------------------------
// Redis 实现
// ---------------------------------------------------------------------------

// RedisOAuthFlowStore 把流程存在 Redis，key 带 TTL，天然过期。
type RedisOAuthFlowStore struct {
	rdb *redis.Client
}

// NewRedisOAuthFlowStore 创建 Redis 版 OAuthFlowStore。
func NewRedisOAuthFlowStore(rdb *redis.Client) *RedisOAuthFlowStore {
	return &RedisOAuthFlowStore{rdb: rdb}
}

// consumeFlowScript 原子地“读取并删除”：用 Lua 而不是 GETDEL，是为了兼容 Redis 6.2 以下的版本。
var consumeFlowScript = redis.NewScript(`
local v = redis.call('GET', KEYS[1])
if v then redis.call('DEL', KEYS[1]) end
return v
`)

// Issue 生成并保存一次 Redis OAuth 流程。
func (s *RedisOAuthFlowStore) Issue(ctx context.Context, verifier string, userID uint64, ttl time.Duration) (string, error) {
	state, err := newOAuthState()
	if err != nil {
		return "", err
	}
	if err := s.rdb.Set(ctx, oauthFlowKeyPrefix+state, encodeFlow(OAuthFlow{Verifier: verifier, UserID: userID}), ttl).Err(); err != nil {
		return "", err
	}
	return state, nil
}

// Consume 原子读取并删除 Redis OAuth 流程，同时校验发起用户。
func (s *RedisOAuthFlowStore) Consume(ctx context.Context, state string, uid uint64) (OAuthFlow, bool) {
	if state == "" {
		return OAuthFlow{}, false
	}
	v, err := consumeFlowScript.Run(ctx, s.rdb, []string{oauthFlowKeyPrefix + state}).Text()
	if err != nil {
		// redis.Nil 表示不存在/已过期/已被消费，属于正常的“无效流程”；其他错误记日志，同样按无效处理（不放行）
		if !errors.Is(err, redis.Nil) {
			logger.Warn("Redis 消费 OAuth 授权流程失败", zap.Error(err))
		}
		return OAuthFlow{}, false
	}
	flow, ok := decodeFlow(v)
	if !ok || flow.UserID != uid {
		return OAuthFlow{}, false
	}
	return flow, true
}

// ---------------------------------------------------------------------------
// 内存实现
// ---------------------------------------------------------------------------

type memOAuthFlow struct {
	flow    OAuthFlow
	expires time.Time
}

// MemoryOAuthFlowStore 是进程内存实现，Redis 关闭时的降级方案。
// 不起后台 goroutine：过期项在 Issue/Consume 时顺带清理，避免额外的生命周期管理。
type MemoryOAuthFlowStore struct {
	mu        sync.Mutex
	items     map[string]memOAuthFlow
	lastSweep time.Time
	now       func() time.Time // 可注入，便于测试过期
}

// NewMemoryOAuthFlowStore 创建内存版 OAuthFlowStore；now 可注入以便测试过期。
func NewMemoryOAuthFlowStore(now func() time.Time) *MemoryOAuthFlowStore {
	return &MemoryOAuthFlowStore{items: make(map[string]memOAuthFlow), now: now}
}

// Issue 生成并保存一次内存 OAuth 流程。
func (s *MemoryOAuthFlowStore) Issue(_ context.Context, verifier string, userID uint64, ttl time.Duration) (string, error) {
	state, err := newOAuthState()
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	s.sweepLocked(now)
	s.items[state] = memOAuthFlow{flow: OAuthFlow{Verifier: verifier, UserID: userID}, expires: now.Add(ttl)}
	return state, nil
}

// Consume 读取并删除内存 OAuth 流程，同时校验发起用户。
func (s *MemoryOAuthFlowStore) Consume(_ context.Context, state string, uid uint64) (OAuthFlow, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	s.sweepLocked(now)
	it, ok := s.items[state]
	if !ok {
		return OAuthFlow{}, false
	}
	delete(s.items, state) // 取出即删：无论是否过期都不能再用
	if !now.Before(it.expires) {
		return OAuthFlow{}, false
	}
	if it.flow.UserID != uid {
		return OAuthFlow{}, false
	}
	return it.flow, true
}

// sweepLocked 清理过期项；限频，避免每次调用都全表扫描。
func (s *MemoryOAuthFlowStore) sweepLocked(now time.Time) {
	if now.Sub(s.lastSweep) < oauthMemSweepEvery {
		return
	}
	s.lastSweep = now
	for k, it := range s.items {
		if !now.Before(it.expires) {
			delete(s.items, k)
		}
	}
}
