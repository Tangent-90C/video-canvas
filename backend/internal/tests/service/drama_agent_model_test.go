package service_test

import (
	"context"
	"testing"

	"video-canvas/internal/llmgateway"
	"video-canvas/internal/model"
	"video-canvas/internal/provider"
	"video-canvas/internal/provider/modelcfg"
)

// 短剧文本步骤只用后台的 Agent 模型；只配了文本模型时等同于没有可用模型。
func TestDramaIgnoresTextModels(t *testing.T) {
	r := newDramaMemory()
	dramaAddEpisode(r, 1, "")
	s, registry := dramaTestService(r, &dramaTextStreamer{}, nil)
	registry.models = []provider.ModelInfo{{Key: "text", Kind: "text"}}
	_, err := s.Estimate(context.Background(), 7, 1, &model.DramaJobReq{Step: "scripts"})
	dramaCheckCode(t, err, 67007)
}

// 模型请求发往快照里的渠道，带用户钱包 Key、上游模型名和接口格式。
func TestDramaCallsAgentChannel(t *testing.T) {
	r := newDramaMemory()
	dramaAddEpisode(r, 1, "")
	e := &dramaTextStreamer{outputs: []string{`{"script_md":"剧本"}`}}
	s, _ := dramaTestService(r, e, nil)
	j, err := s.CreateJob(context.Background(), 7, 1, &model.DramaJobReq{Step: "scripts", Episodes: []int{1}})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.HandleDrama(context.Background(), 7, 1, uint64(j.ID)); err != nil {
		t.Fatal(err)
	}
	if len(e.targets) != 1 {
		t.Fatalf("calls=%d", len(e.targets))
	}
	got := e.targets[0]
	if got.ChannelKey != "chan" || got.BaseURL != "https://llm.example" || got.APIKey != "sk-wallet-test" || got.UpstreamModel != "upstream-chat" || got.Format != "openai" {
		t.Fatalf("target=%+v", got)
	}
}

// TokenPay 钱包模型由上游直接计费；平台任务只记录用量语义并保持积分为 0。
func TestDramaWalletModelKeepsPlatformCreditsAtZero(t *testing.T) {
	r := newDramaMemory()
	dramaAddEpisode(r, 1, "")
	e := &dramaTextStreamer{outputs: []string{`{"script_md":"剧本"}`}, usage: llmgateway.Usage{InputTokens: 1000, OutputTokens: 500}}
	s, registry := dramaTestService(r, e, nil)
	registry.snap.Model.Pricing = modelcfg.Pricing{Billing: modelcfg.BillingToken, Token: &modelcfg.TokenPrice{In: 1000, Out: 4000}}
	j, err := s.CreateJob(context.Background(), 7, 1, &model.DramaJobReq{Step: "scripts", Episodes: []int{1}})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.HandleDrama(context.Background(), 7, 1, uint64(j.ID)); err != nil {
		t.Fatal(err)
	}
	if r.jobs[0].Status != "succeeded" || r.jobs[0].Credits != 0 || r.credit.Balance != 1000 || r.credit.Frozen != 0 || len(r.ledger) != 0 {
		t.Fatalf("钱包计费不应触碰平台积分：job=%+v credit=%+v ledger=%+v", r.jobs[0], r.credit, r.ledger)
	}
}

// 钱包模型不读取渠道的产品方 Key；渠道名变化不影响用户自己的钱包请求。
func TestDramaWalletModelDoesNotNeedChannelSecret(t *testing.T) {
	r := newDramaMemory()
	dramaAddEpisode(r, 1, "")
	e := &dramaTextStreamer{outputs: []string{`{"script_md":"剧本"}`}}
	s, registry := dramaTestService(r, e, nil)
	registry.snap.Channel.Key = "other"
	j, err := s.CreateJob(context.Background(), 7, 1, &model.DramaJobReq{Step: "scripts", Episodes: []int{1}})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.HandleDrama(context.Background(), 7, 1, uint64(j.ID)); err != nil {
		t.Fatal(err)
	}
	if e.calls != 1 || r.jobs[0].Status != "succeeded" || r.credit.Balance != 1000 || r.credit.Frozen != 0 {
		t.Fatalf("calls=%d job=%+v credit=%+v", e.calls, r.jobs[0], r.credit)
	}
}

// 首页创建短剧时选的 Agent 模型：存进项目，研读任务就用它；不选则跟随后台第一个（存空串）。
func TestDramaCreateKeepsChosenAgentModel(t *testing.T) {
	for _, tc := range []struct{ name, chosen, want string }{
		{"选了模型", "pro", "pro"},
		{"没选跟随默认", "", "text"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newDramaMemory()
			s, registry := dramaTestService(r, &dramaTextStreamer{}, nil)
			registry.models = []provider.ModelInfo{{Key: "text", Kind: "agent", Wallet: true}, {Key: "pro", Kind: "agent", Wallet: true}}
			_, err := s.Create(context.Background(), 7, &model.CreateDramaReq{SourceToken: seedDramaDraft(r, "第一章\n正文"), SourceKind: "novel", StyleKey: "custom", StyleCustom: "写实", TextModel: tc.chosen})
			if err != nil {
				t.Fatal(err)
			}
			if r.project.TextModel != tc.chosen {
				t.Fatalf("项目里的模型=%q want %q", r.project.TextModel, tc.chosen)
			}
			if len(registry.requested) == 0 || registry.requested[0] != tc.want {
				t.Fatalf("研读任务应先用 %q，实际请求 %v", tc.want, registry.requested)
			}
		})
	}
}
