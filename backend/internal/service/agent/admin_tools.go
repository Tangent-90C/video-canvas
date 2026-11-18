package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"video-canvas/internal/model"
	"video-canvas/internal/pkg/errcode"
)

// requireAdminRun 在平台入口使用数据库角色做后台授权。
func (b *AgentBridge) requireAdminRun(ctx context.Context, rc RunContext) error {
	if rc.Mode != "admin" {
		return nil
	}
	if b.d.Admin == nil {
		return fail("后台助手未配置")
	}
	return b.d.Admin.RequireAdmin(ctx, rc.UserID)
}

// toolAdminListModels 返回后台模型概要，可按类型筛选，不发出渠道凭证。
func (b *AgentBridge) toolAdminListModels(ctx context.Context, tc *toolCall) (*toolOut, error) {
	var req struct {
		Kind    string `json:"kind"`
		Enabled *bool  `json:"enabled"`
		Channel string `json:"channel"`
		Q       string `json:"q"`
	}
	if json.Unmarshal(tc.args, &req) != nil {
		return nil, fail("参数格式不正确")
	}
	rows, err := b.d.Admin.d.Models.LoadModels(ctx)
	if err != nil {
		return nil, err
	}
	usage, err := b.d.Admin.d.Audit.RecentModelUsage(ctx, b.d.Admin.d.Now().Add(-7*24*time.Hour))
	if err != nil {
		return nil, err
	}
	out := []map[string]any{}
	for _, row := range rows {
		if req.Kind != "" && row.Kind != req.Kind {
			continue
		}
		var body map[string]any
		_ = json.Unmarshal(row.Body, &body)
		if req.Enabled != nil && row.Enabled != *req.Enabled {
			continue
		}
		var header struct {
			Channels []struct {
				Channel string `json:"channel"`
			} `json:"channels"`
		}
		_ = json.Unmarshal(row.Body, &header)
		channel := ""
		if len(header.Channels) > 0 {
			channel = header.Channels[0].Channel
		}
		if req.Channel != "" && channel != req.Channel {
			continue
		}
		if req.Q != "" && !strings.Contains(strings.ToLower(row.Key+" "+fmt.Sprint(body["label"])), strings.ToLower(req.Q)) {
			continue
		}
		out = append(out, map[string]any{"key": row.Key, "kind": row.Kind, "enabled": row.Enabled, "label": body["label"], "channel": channel, "calls_7d": usage[row.Key]})
	}
	return adminToolOut(out)
}

// toolAdminGetModel 给出原始配置和用于提案的摘要。
func (b *AgentBridge) toolAdminGetModel(ctx context.Context, tc *toolCall) (*toolOut, error) {
	var req struct {
		Key string `json:"key"`
	}
	if json.Unmarshal(tc.args, &req) != nil || req.Key == "" {
		return nil, fail("需要模型 key")
	}
	body, sha, live, err := b.d.Admin.ReadModel(ctx, req.Key)
	if err != nil {
		return nil, err
	}
	return adminToolOut(map[string]any{"body": body, "body_sha256": sha, "live": live})
}

// toolAdminListChannels 只投影安全的渠道概要，HeadersJSON 可能包含密钥，绝不下发。
func (b *AgentBridge) toolAdminListChannels(ctx context.Context, _ *toolCall) (*toolOut, error) {
	rows, err := b.d.Admin.d.Channels.ListChannels(ctx)
	if err != nil {
		return nil, err
	}
	secrets, err := b.d.Admin.d.Models.ListSecrets(ctx)
	if err != nil {
		return nil, err
	}
	configured := map[string]bool{}
	for _, secret := range secrets {
		configured[secret.Name] = true
	}
	out := []map[string]any{}
	for _, row := range rows {
		out = append(out, map[string]any{"key": row.Key, "name": row.Name, "base_url": row.BaseURL, "enabled": row.Enabled, "has_key": configured[model.ChannelSecretName(row.Key)], "secret_configured": configured[model.ChannelSecretName(row.Key)]})
	}
	return adminToolOut(out)
}

// toolAdminPropose 只有至少一项可应用才出审批卡，所有快照由平台签名。
func (b *AgentBridge) toolAdminPropose(ctx context.Context, tc *toolCall) (*toolOut, error) {
	if len(b.d.DramaApprovalKey) == 0 {
		return nil, fail("后台审批签名未配置")
	}
	var req AdminChangeRequest
	if json.Unmarshal(tc.args, &req) != nil {
		return nil, fail("参数格式不正确")
	}
	p, err := b.d.Admin.buildChanges(ctx, req, tc.run.ID)
	if err != nil {
		return nil, err
	}
	applicable := 0
	for _, item := range p.Items {
		if item.Status == "ok" {
			applicable++
		}
	}
	if applicable == 0 {
		lines := []string{"所有条目均不可应用，请重新读取并修正："}
		for _, item := range p.Items {
			// 错误只返回修正所需信息，避免完整配置快照挤占模型上下文。
			raw, _ := json.Marshal(struct {
				Key      string   `json:"key"`
				Status   string   `json:"status"`
				Issues   []string `json:"issues"`
				Warnings []string `json:"warnings,omitempty"`
			}{item.Key, item.Status, item.Issues, item.Warnings})
			lines = append(lines, string(raw))
		}
		// 逐项摘要已精简，使用业务错误保留全部条目，避免通用工具错误截断最后一行。
		return nil, errcode.ErrInvalidParams.WithMsg(strings.Join(lines, "\n"))
	}
	rc := RunContext{RunID: tc.run.ID, SessionID: tc.run.SessionID, UserID: tc.run.UserID, CanvasID: tc.run.CanvasID, Mode: tc.run.Mode}
	p.Signature = b.adminProposalSignature(rc, *p)
	if err := b.requestApproval(ctx, tc, "admin_change", p, 0); err != nil {
		return nil, err
	}
	return &toolOut{content: "变更单已提交，等待用户决定。", summary: fmt.Sprintf("%s（%d 项可应用）", p.Title, applicable), terminate: true}, nil
}

// adminToolOut 将只读结果序列化成模型可用的文本。
func adminToolOut(value any) (*toolOut, error) {
	text, err := jsonOut(value)
	return &toolOut{content: text}, err
}
