package sdk

import (
	"context"

	"github.com/biox-dev/opencode/internal/permission"
)

// PermissionDecision 是权限委托返回的决策结果。
type PermissionDecision int

const (
	// PermissionDeny 拒绝（默认值，安全兜底）。
	PermissionDeny PermissionDecision = iota
	// PermissionAllow 允许执行。
	PermissionAllow
)

// PermissionRequest 是一次待决策的权限请求（面向宿主的公开视图）。
type PermissionRequest struct {
	SessionID   string
	ToolName    string
	Action      string // execute / write / read / fetch ...
	Path        string
	Description string
	Params      any
}

// PermissionHandler 由宿主注入，用于把工具权限请求委托给外部策略 / UI。
//
//	clientOpts.PermissionHandler = func(ctx context.Context, req sdk.PermissionRequest) sdk.PermissionDecision {
//	    op := toOperation(req)                       // 转成上层的中立 Operation
//	    decision, err := rt.RequestPermission(ctx, userID, op)
//	    if err != nil || decision != agent.DecisionAllow {
//	        return sdk.PermissionDeny
//	    }
//	    return sdk.PermissionAllow
//	}
//
// 配置后，SDK 不再阻塞等待命令行交互，而是逐次调用该委托决策。
type PermissionHandler func(ctx context.Context, req PermissionRequest) PermissionDecision

// toInternalPermissionHandler 把公开委托适配为 internal/permission 的 Handler。
func toInternalPermissionHandler(h PermissionHandler) permission.Handler {
	if h == nil {
		return nil
	}
	return func(ctx context.Context, req permission.PermissionRequest) bool {
		return h(ctx, PermissionRequest{
			SessionID:   req.SessionID,
			ToolName:    req.ToolName,
			Action:      req.Action,
			Path:        req.Path,
			Description: req.Description,
			Params:      req.Params,
		}) == PermissionAllow
	}
}
