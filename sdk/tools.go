package sdk

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/biox-dev/opencode/internal/config"
	"github.com/biox-dev/opencode/internal/llm/tools"
	"github.com/biox-dev/opencode/internal/permission"
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

// HostToolPermission 声明宿主工具的授权动作；配置后该工具在执行前会走
// permission.Service.Request（与 opencode 内置工具同一套权限门禁）。
//
// 未配置（nil）表示内部只读工具，不触发权限请求。
type HostToolPermission struct {
	Action      string // execute / write / read / fetch / delete ...
	PathFrom    string // 可选：从入参 JSON 中取文件路径的字段名（如 "file_path"）
	Description string // 可选：权限描述；为空时回退为 "Run tool: <name>"
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

	// Permission 可选：声明该宿主工具的授权动作。为空则不触发权限门禁。
	Permission *HostToolPermission
}

// hostToolAdapter 把宿主工具适配为 opencode 内部的 tools.BaseTool。
//
// permissions 为 nil 时跳过权限门禁（等价于所有宿主工具免授权）。
type hostToolAdapter struct {
	tool        HostTool
	permissions permission.Service
}

func (a hostToolAdapter) Info() tools.ToolInfo {
	return tools.ToolInfo{
		Name:        a.tool.Name,
		Description: a.tool.Description,
		Parameters:  a.tool.Parameters,
		Required:    a.tool.Required,
	}
}

func (a hostToolAdapter) Run(ctx context.Context, call tools.ToolCall) (tools.ToolResponse, error) {
	if err := a.authorize(ctx, call); err != nil {
		return tools.ToolResponse{}, err
	}

	res, err := a.tool.Handler(ctx, HostToolCall{ID: call.ID, Name: call.Name, Input: call.Input})
	if err != nil {
		return tools.ToolResponse{}, err
	}
	resp := tools.NewTextResponse(res.Content)
	resp.IsError = res.IsError
	return tools.WithResponseMetadata(resp, res.Metadata), nil
}

// authorize 在宿主工具执行前请求权限（未声明 Permission 时直接放行）。
//
// 授权链路与内置工具一致：permission.Service.Request → 宿主注入的 Handler
// （PermissionHandler）→ 上层 Runtime 决策。拒绝时返回 ErrorPermissionDenied。
func (a hostToolAdapter) authorize(ctx context.Context, call tools.ToolCall) error {
	perm := a.tool.Permission
	if perm == nil || a.permissions == nil {
		return nil
	}

	sessionID, messageID := tools.GetContextValues(ctx)
	if sessionID == "" || messageID == "" {
		return fmt.Errorf("session ID and message ID are required for host tool %q", a.tool.Name)
	}

	description := strings.TrimSpace(perm.Description)
	if description == "" {
		description = fmt.Sprintf("Run tool: %s", a.tool.Name)
	}

	allowed := a.permissions.Request(ctx, permission.CreatePermissionRequest{
		SessionID:   sessionID,
		Path:        pathFromInput(perm, call.Input),
		ToolName:    a.tool.Name,
		Action:      perm.Action,
		Description: description,
		// 透传原始入参，供宿主抽取 diff / command 等细节。
		Params: json.RawMessage(call.Input),
	})
	if !allowed {
		return permission.ErrorPermissionDenied
	}
	return nil
}

// pathFromInput 按 PathFrom 从入参 JSON 中抽取路径；缺失或非法时回退到工作目录。
func pathFromInput(perm *HostToolPermission, raw string) string {
	field := strings.TrimSpace(perm.PathFrom)
	if field == "" {
		return config.WorkingDirectory()
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return config.WorkingDirectory()
	}
	if s, ok := m[field].(string); ok && strings.TrimSpace(s) != "" {
		return s
	}
	return config.WorkingDirectory()
}

// adaptHostTools 把宿主工具适配为 opencode 工具列表；跳过无效项（无名称或无 Handler）。
func adaptHostTools(hostTools []HostTool, permissions permission.Service) []tools.BaseTool {
	if len(hostTools) == 0 {
		return nil
	}
	out := make([]tools.BaseTool, 0, len(hostTools))
	for _, t := range hostTools {
		if t.Name == "" || t.Handler == nil {
			continue
		}
		out = append(out, hostToolAdapter{tool: t, permissions: permissions})
	}
	return out
}
