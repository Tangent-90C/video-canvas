package service_test

import (
	"context"
	"encoding/json"
	"testing"
)

func TestAIConfigService_LegacyPricingDoesNotBlockWalletModel(t *testing.T) {
	svc, repo, dry, tasks := aicRunSetup(t)
	var body map[string]any
	_ = json.Unmarshal(aicModelBody("m1", "c1", ""), &body)
	body["enabled"] = false
	delete(body, "pricing")
	raw, _ := json.Marshal(body)
	saved, err := aicSave(svc, "m1", false, raw)
	aicWantCode(t, err, 0)
	if len(saved.Issues) != 0 {
		t.Fatalf("没有 pricing 的钱包模型不应有校验问题：%+v", saved.Issues)
	}
	if _, err = svc.DryRun(context.Background(), 9, "m1", map[string]any{"prompt": "test"}); err != nil {
		t.Fatalf("dry-run 不应依赖 platform pricing：%v", err)
	}
	if dry.Snap == nil {
		t.Fatal("未执行离线试跑")
	}
	view, err := svc.TestRun(context.Background(), 9, "m1", map[string]any{"prompt": "test"})
	aicWantCode(t, err, 0)
	if view == nil || view.ID == 0 || tasks.Snap == nil {
		t.Fatal("未创建管理员试跑")
	}
	aicWantCode(t, svc.SetModelEnabled(context.Background(), "m1", true, 9), 0)
	if !repo.Models["m1"].Enabled {
		t.Fatal("钱包模型没有平台定价也应能启用")
	}

	// 旧正文里的 pending/价格结构只作历史兼容，不能再阻止保存或启用。
	body["pricing"] = map[string]any{"billing": "per_call", "pending": true}
	raw, _ = json.Marshal(body)
	_, err = aicSave(svc, "m1", false, raw)
	aicWantCode(t, err, 0)
	aicWantCode(t, svc.SetModelEnabled(context.Background(), "m1", true, 9), 0)
	if !repo.Models["m1"].Enabled {
		t.Fatal("历史 pricing 状态不应阻止钱包模型启用")
	}
}
