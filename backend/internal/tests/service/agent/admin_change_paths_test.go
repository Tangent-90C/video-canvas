package agent_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	. "video-canvas/internal/service/agent"
)

func TestAdminChangeRejectsPlatformPointPricing(t *testing.T) {
	f := newAdminFake()
	svc, _, _ := adminEnv(f)
	model := relativeInput(f, "a")
	model.Patch = map[string]any{"pricing": map[string]any{"unit": float64(3)}}
	settings := AdminChangeInput{Target: "pricing_settings", Key: "pricing", Patch: map[string]any{"credits_per_yuan": float64(7)}}
	p, err := svc.BuildChanges(context.Background(), AdminChangeRequest{Title: "平台积分已停用", Reason: "验证禁止回退", Items: []AdminChangeInput{model, settings}})
	if err != nil {
		t.Fatal(err)
	}
	for i, item := range p.Items {
		if item.Status != "invalid" {
			t.Fatalf("第 %d 项应拒绝：%+v", i+1, item)
		}
		if !strings.Contains(strings.Join(item.Issues, "\n"), "不允许修改字段：pricing") && !strings.Contains(strings.Join(item.Issues, "\n"), "target 只能是 model") {
			t.Fatalf("第 %d 项缺少拒绝原因：%v", i+1, item.Issues)
		}
	}
}

func TestAdminProposeFailureCompact(t *testing.T) {
	f := newAdminFake()
	f.live["a"] = true
	_, bridge, rc := adminEnv(f)
	first, second := relativeInput(f, "a"), relativeInput(f, "b")
	first.BaseSHA256 = "过期摘要"
	second.Patch = map[string]any{"pricing": map[string]any{"unit": float64(3)}}
	raw, _ := json.Marshal(AdminChangeRequest{Title: "改价", Reason: "要求", Items: []AdminChangeInput{first, second}})
	result, err := bridge.ProposeApproval(context.Background(), rc, "admin_change", raw)
	if err != nil || !result.IsError {
		t.Fatalf("应返回工具错误：%+v %v", result, err)
	}
	lines := strings.Split(result.Content, "\n")
	if len(lines) != 3 {
		t.Fatalf("应为说明加两个条目，每项一行：%s", result.Content)
	}
	for i, line := range lines[1:] {
		var item map[string]any
		if err := json.Unmarshal([]byte(line), &item); err != nil {
			t.Fatal(err)
		}
		wantKey, wantStatus := "a", "stale"
		if i == 1 {
			wantKey, wantStatus = "b", "invalid"
		}
		if item["key"] != wantKey || item["status"] != wantStatus || len(item["issues"].([]any)) == 0 {
			t.Fatalf("缺少修正信息：%v", item)
		}
		for key := range item {
			if key != "key" && key != "status" && key != "issues" && key != "warnings" {
				t.Fatalf("不应返回字段 %s", key)
			}
		}
		if i == 0 && len(item["warnings"].([]any)) == 0 {
			t.Fatal("应保留保存即生效警告")
		}
	}
}

func TestAdminProposeFailureCompactBatch(t *testing.T) {
	f := newAdminFake()
	_, bridge, rc := adminEnv(f)
	items := make([]AdminChangeInput, 50)
	for i := range items {
		items[i] = relativeInput(f, "a")
		items[i].Patch = map[string]any{"pricing": map[string]any{"unit": float64(3)}}
	}
	raw, _ := json.Marshal(AdminChangeRequest{Title: "批量改价", Reason: "要求", Items: items})
	result, err := bridge.ProposeApproval(context.Background(), rc, "admin_change", raw)
	if err != nil || !result.IsError {
		t.Fatalf("应返回工具错误：%+v %v", result, err)
	}
	lines := strings.Split(result.Content, "\n")
	if len(lines) != 51 {
		t.Fatalf("批量错误应完整返回每项一行，实际 %d 行", len(lines))
	}
	for _, line := range lines[1:] {
		var item map[string]any
		if err := json.Unmarshal([]byte(line), &item); err != nil {
			t.Fatalf("条目被截断：%v", err)
		}
		if len(item) != 3 || item["key"] != "a" || item["status"] != "invalid" {
			t.Fatalf("条目未精简：%v", item)
		}
	}
}

func TestAdminCreateRejectsPlatformPointPricing(t *testing.T) {
	f := newAdminFake()
	svc, _, _ := adminEnv(f)
	var body map[string]any
	if err := json.Unmarshal(f.bodies["a"], &body); err != nil {
		t.Fatal(err)
	}
	body["key"] = "new-priced"
	body["pricing"] = map[string]any{"billing": "per_call", "unit": 3}
	p, err := svc.BuildChanges(context.Background(), AdminChangeRequest{
		Title: "新建模型", Reason: "验证不能写平台积分价格",
		Items: []AdminChangeInput{{Target: "model_create", Key: "new-priced", Patch: body}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Items) != 1 || p.Items[0].Status != "invalid" || !strings.Contains(strings.Join(p.Items[0].Issues, "\n"), "pricing 已停用") {
		t.Fatalf("model_create 不应接受 pricing：%+v", p.Items)
	}
}
