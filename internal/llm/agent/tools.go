package agent

import (
	"context"

	"github.com/biox-dev/opencode/internal/history"
	"github.com/biox-dev/opencode/internal/llm/tools"
	"github.com/biox-dev/opencode/internal/lsp"
	"github.com/biox-dev/opencode/internal/message"
	"github.com/biox-dev/opencode/internal/permission"
	"github.com/biox-dev/opencode/internal/session"
)

func CoderAgentTools(
	permissions permission.Service,
	sessions session.Service,
	messages message.Service,
	history history.Service,
	lspClients map[string]*lsp.Client,
) []tools.BaseTool {
	ctx := context.Background()
	otherTools := GetMcpTools(ctx, permissions)
	if len(lspClients) > 0 {
		otherTools = append(otherTools, tools.NewDiagnosticsTool(lspClients))
	}
	return append(
		[]tools.BaseTool{
			tools.NewBashTool(permissions),
			tools.NewEditTool(lspClients, permissions, history),
			tools.NewFetchTool(permissions),
			tools.NewGlobTool(),
			tools.NewGrepTool(),
			tools.NewLsTool(),
			// skills 相关工具已注释：opencode 作为 gobrave 类库使用时，技能统一由
			// gobrave/internal/agent/skill 实现（以系统提示词注入技能元信息，不注册工具），
			// 此处避免 skill_list / skill_load 工具名与 gobrave 冲突。
			// tools.NewSkillListTool(),
			// tools.NewSkillLoadTool(),
			tools.NewSourcegraphTool(),
			tools.NewViewTool(lspClients),
			tools.NewPatchTool(lspClients, permissions, history),
			tools.NewWriteTool(lspClients, permissions, history),
			NewAgentTool(sessions, messages, lspClients),
		}, otherTools...,
	)
}

func TaskAgentTools(lspClients map[string]*lsp.Client) []tools.BaseTool {
	return []tools.BaseTool{
		tools.NewGlobTool(),
		tools.NewGrepTool(),
		tools.NewLsTool(),
		// 同 CoderAgentTools：技能工具已注释，避免与 gobrave/internal/agent/skill 冲突。
		// tools.NewSkillListTool(),
		// tools.NewSkillLoadTool(),
		tools.NewSourcegraphTool(),
		tools.NewViewTool(lspClients),
	}
}
