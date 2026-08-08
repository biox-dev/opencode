package agent

import (
	"context"

	"github.com/gobravedev/opencode/internal/history"
	"github.com/gobravedev/opencode/internal/llm/tools"
	"github.com/gobravedev/opencode/internal/lsp"
	"github.com/gobravedev/opencode/internal/message"
	"github.com/gobravedev/opencode/internal/permission"
	"github.com/gobravedev/opencode/internal/session"
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
			tools.NewSkillListTool(),
			tools.NewSkillLoadTool(),
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
		tools.NewSkillListTool(),
		tools.NewSkillLoadTool(),
		tools.NewSourcegraphTool(),
		tools.NewViewTool(lspClients),
	}
}
