package plugin_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"video-canvas/internal/provider"
)

// TokenDance 用 TokenDance-Recovery-Action 头声明确切的恢复路径（官方 OAuth 文档的硬性要求），
// 宿主按它直接定分类，优先于插件的 classifyError 与文本启发式：
//   - top_up_balance → provider_balance（钱包任务文案「余额不足，请充值」）
//   - reauthorize_api_key → secret_unavailable（钱包任务文案「请重新连接钱包」）
//   - api_key_quota → key_quota（等待额度刷新或重新授权）
// 同一判定对钱包任务和普通渠道任务都生效（渠道无关，认头不认渠道）。
func TestTokendanceRecoveryActionHeader(t *testing.T) {
	cases := []struct {
		action string
		status int
		want   provider.ErrorClass
	}{
		{"top_up_balance", http.StatusPaymentRequired, provider.ClassProviderBalance},
		{"reauthorize_api_key", http.StatusUnauthorized, provider.ClassSecretUnavailable},
		{"api_key_quota", http.StatusBadRequest, provider.ClassKeyQuota},
	}
	for _, tc := range cases {
		t.Run(tc.action, func(t *testing.T) {
			up := &fakeNewAPI{handler: func(w http.ResponseWriter, _ *http.Request, _ map[string]any) {
				w.Header().Set("TokenDance-Recovery-Action", tc.action)
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(`{"error":{"message":"upstream says no"}}`))
			}}
			srv := httptest.NewServer(up)
			defer srv.Close()

			resolver := &walletResolver{values: map[string]string{
				"channel:tp-main": "channel-key",
				"wallet:7":        "wallet-key",
			}}
			env := newTokendanceEnv(t, resolver)
			_, err := env.exec.Submit(context.Background(), env.snapshot(srv.URL, true), provider.SubmitInput{
				Task:  provider.TaskRef{ID: 1, UserID: 7, Wallet: true},
				Input: map[string]any{"prompt": "一只猫在跳舞"},
			})
			if err == nil {
				t.Fatalf("%s 应该提交失败", tc.action)
			}
			var pe *provider.Error
			if !errors.As(err, &pe) {
				t.Fatalf("应是 *provider.Error：%v", err)
			}
			if pe.Class != tc.want {
				t.Fatalf("恢复动作 %s 应分类为 %s，实际 %s（upstream=%s）", tc.action, tc.want, pe.Class, pe.Upstream)
			}
			if pe.Upstream == "" {
				t.Fatal("应保留上游原文给用户看")
			}
		})
	}
}

// 普通渠道任务（非钱包）同样认头：渠道 Key 撞上 api_key_quota 也分类为 key_quota。
func TestRecoveryActionHeaderAppliesToPlainChannel(t *testing.T) {
	up := &fakeNewAPI{handler: func(w http.ResponseWriter, _ *http.Request, _ map[string]any) {
		w.Header().Set("TokenDance-Recovery-Action", "api_key_quota")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":{"message":"quota exceeded"}}`))
	}}
	srv := httptest.NewServer(up)
	defer srv.Close()

	resolver := &walletResolver{values: map[string]string{"channel:tp-main": "channel-key"}}
	env := newTokendanceEnv(t, resolver)
	_, err := env.exec.Submit(context.Background(), env.snapshot(srv.URL, false), provider.SubmitInput{
		Task:  provider.TaskRef{ID: 2, UserID: 7},
		Input: map[string]any{"prompt": "一只猫在跳舞"},
	})
	if err == nil {
		t.Fatal("应该提交失败")
	}
	var pe *provider.Error
	if !errors.As(err, &pe) || pe.Class != provider.ClassKeyQuota {
		t.Fatalf("普通渠道任务也应按恢复动作头分类为 key_quota：%v", err)
	}
}
