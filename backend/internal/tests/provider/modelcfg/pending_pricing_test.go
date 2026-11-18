package modelcfg_test

import (
	"testing"

	"video-canvas/internal/provider/modelcfg"
)

func TestParseModel_PendingPricingIsLegacyCompatibilityOnly(t *testing.T) {
	for _, tc := range []struct {
		name    string
		enabled bool
		pricing map[string]any
	}{
		{"旧未定价草稿", false, map[string]any{"billing": "per_call", "pending": true}},
		{"旧未定价标记不阻止启用", true, map[string]any{"billing": "per_call", "pending": true}},
		{"旧待定价占位价格", false, map[string]any{"billing": "per_call", "pending": true, "unit": 1}},
		{"旧空价格", false, map[string]any{"billing": "per_call"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := mcLoadFixture(t, "model_video.json")
			body["enabled"], body["pricing"] = tc.enabled, tc.pricing
			got, issues := modelcfg.ParseModel(mcMarshal(t, body))
			if got == nil || len(issues) != 0 {
				t.Fatalf("历史 pricing 不应再参与校验：got=%+v issues=%v", got, issues)
			}
		})
	}
}
