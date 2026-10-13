package permission

import (
	"context"
	"errors"
	"path/filepath"
	"sync"

	"github.com/biox-dev/opencode/internal/config"
	"github.com/biox-dev/opencode/internal/pubsub"
	"github.com/google/uuid"
)

var ErrorPermissionDenied = errors.New("permission denied")

type CreatePermissionRequest struct {
	SessionID   string `json:"session_id"`
	ToolName    string `json:"tool_name"`
	Description string `json:"description"`
	Action      string `json:"action"`
	Params      any    `json:"params"`
	Path        string `json:"path"`
}

type PermissionRequest struct {
	ID          string `json:"id"`
	SessionID   string `json:"session_id"`
	ToolName    string `json:"tool_name"`
	Description string `json:"description"`
	Action      string `json:"action"`
	Params      any    `json:"params"`
	Path        string `json:"path"`
}

// Handler 是宿主注入的权限委托：把权限请求交给外部策略 / UI 决策。
//
// 返回 true 表示允许，false 表示拒绝；ctx 用于取消等待。
// 为 nil 时回退到「阻塞等待人工 Grant / Deny」的交互式流程（如 TUI）。
type Handler func(ctx context.Context, req PermissionRequest) bool

type Service interface {
	pubsub.Suscriber[PermissionRequest]
	GrantPersistant(permission PermissionRequest)
	Grant(permission PermissionRequest)
	Deny(permission PermissionRequest)
	Request(ctx context.Context, opts CreatePermissionRequest) bool
	AutoApproveSession(sessionID string)
}

type permissionService struct {
	*pubsub.Broker[PermissionRequest]

	handler Handler

	mu                  sync.RWMutex
	sessionPermissions  []PermissionRequest
	pendingRequests     sync.Map
	autoApproveSessions map[string]struct{}
}

func (s *permissionService) GrantPersistant(permission PermissionRequest) {
	respCh, ok := s.pendingRequests.Load(permission.ID)
	if ok {
		respCh.(chan bool) <- true
	}
	s.mu.Lock()
	s.sessionPermissions = append(s.sessionPermissions, permission)
	s.mu.Unlock()
}

func (s *permissionService) Grant(permission PermissionRequest) {
	respCh, ok := s.pendingRequests.Load(permission.ID)
	if ok {
		respCh.(chan bool) <- true
	}
}

func (s *permissionService) Deny(permission PermissionRequest) {
	respCh, ok := s.pendingRequests.Load(permission.ID)
	if ok {
		respCh.(chan bool) <- false
	}
}

func (s *permissionService) Request(ctx context.Context, opts CreatePermissionRequest) bool {
	// 1) 会话级自动放行。
	s.mu.RLock()
	_, autoApprove := s.autoApproveSessions[opts.SessionID]
	s.mu.RUnlock()
	if autoApprove {
		return true
	}

	dir := filepath.Dir(opts.Path)
	if dir == "." {
		dir = config.WorkingDirectory()
	}
	permission := PermissionRequest{
		ID:          uuid.New().String(),
		Path:        dir,
		SessionID:   opts.SessionID,
		ToolName:    opts.ToolName,
		Description: opts.Description,
		Action:      opts.Action,
		Params:      opts.Params,
	}

	// 2) 命中已持久化的授权则直接放行。
	s.mu.RLock()
	for _, p := range s.sessionPermissions {
		if p.ToolName == permission.ToolName && p.Action == permission.Action &&
			p.SessionID == permission.SessionID && p.Path == permission.Path {
			s.mu.RUnlock()
			return true
		}
	}
	handler := s.handler
	s.mu.RUnlock()

	// 3) 广播请求（供 UI / 审计消费）。
	s.Publish(pubsub.CreatedEvent, permission)

	// 4) 有宿主委托：交给外部策略决策（如 gobrave Runtime.RequestPermission）。
	if handler != nil {
		return handler(ctx, permission)
	}

	// 5) 无委托：阻塞等待人工 Grant / Deny（交互式 TUI 场景）。
	respCh := make(chan bool, 1)
	s.pendingRequests.Store(permission.ID, respCh)
	defer s.pendingRequests.Delete(permission.ID)

	select {
	case resp := <-respCh:
		return resp
	case <-ctx.Done():
		return false
	}
}

func (s *permissionService) AutoApproveSession(sessionID string) {
	s.mu.Lock()
	s.autoApproveSessions[sessionID] = struct{}{}
	s.mu.Unlock()
}

// NewPermissionService 创建权限服务；handler 为宿主注入的委托（可为 nil）。
func NewPermissionService(handler Handler) Service {
	return &permissionService{
		Broker:              pubsub.NewBroker[PermissionRequest](),
		handler:             handler,
		sessionPermissions:  make([]PermissionRequest, 0),
		autoApproveSessions: make(map[string]struct{}),
	}
}
