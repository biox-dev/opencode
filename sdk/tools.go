package sdk

import (
	"context"

	"github.com/biox-dev/opencode/internal/llm/tools"
)

// HostToolCall 是传给宿主工具 Handler 的一次调用。
type HostToolCall struct {
	ID    string // 工具调用 ID
	Name  string // 工具名
	Input string // 模型生成的参数（JSON）
}

// HostToolResult 是宿主工具 Handler 返回的结果。
type HostToolResult struct {
	Content  string // 回传给模型的文本
	IsError  bool   // 是否为失败结果（模型据此纠正行为）
	Metadata any    // 可选的结构化元信息
}

// HostTool 是宿主注入的自定义工具：暴露给模型，并在 opencode 的内置 tool-call
// 循环中执行。
//
// Parameters / Required 与 opencode 内置工具（tools.ToolInfo）保持同一种组织方式：
// Parameters 是 JSON Schema 的 properties 映射，Required 是必填字段名列表。
type HostTool struct {
	Name        string
	Description string
	Parameters  map[string]any
	Required    []string
	Handler     func(ctx context.Context, call HostToolCall) (HostToolResult, error)
}

// hostToolAdapter 把宿主工具适配为 opencode 内部的 tools.BaseTool。
type hostToolAdapter struct{ tool HostTool }

func (a hostToolAdapter) Info() tools.ToolInfo {
	return tools.ToolInfo{
		Name:        a.tool.Name,
		Description: a.tool.Description,
		Parameters:  a.tool.Parameters,
		Required:    a.tool.Required,
	}
}

func (a hostToolAdapter) Run(ctx context.Context, call tools.ToolCall) (tools.ToolResponse, error) {
	res, err := a.tool.Handler(ctx, HostToolCall{ID: call.ID, Name: call.Name, Input: call.Input})
	if err != nil {
		return tools.ToolResponse{}, err
	}
	resp := tools.NewTextResponse(res.Content)
	resp.IsError = res.IsError
	return tools.WithResponseMetadata(resp, res.Metadata), nil
}

// adaptHostTools 把宿主工具适配为 opencode 工具列表；跳过无效项（无名称或无 Handler）。
func adaptHostTools(hostTools []HostTool) []tools.BaseTool {
	if len(hostTools) == 0 {
		return nil
	}
	out := make([]tools.BaseTool, 0, len(hostTools))
	for _, t := range hostTools {
		if t.Name == "" || t.Handler == nil {
			continue
		}
		out = append(out, hostToolAdapter{tool: t})
	}
	return out
}
