package sdk

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	appcore "github.com/biox-dev/opencode/internal/app"
	"github.com/biox-dev/opencode/internal/config"
	"github.com/biox-dev/opencode/internal/db"
	llmagent "github.com/biox-dev/opencode/internal/llm/agent"
	"github.com/biox-dev/opencode/internal/message"
	"github.com/biox-dev/opencode/internal/pubsub"
	"github.com/biox-dev/opencode/internal/session"
)

type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

type EventType string

const (
	EventSessionCreated       EventType = "session_created"
	EventMessageCreated       EventType = "message_created"
	EventMessageUpdated       EventType = "message_updated"
	EventAssistantTextDelta   EventType = "assistant_text_delta"
	EventAssistantThinkDelta  EventType = "assistant_thinking_delta"
	EventAgentCompleted       EventType = "agent_completed"
	EventAgentError           EventType = "agent_error"
	EventAgentCanceled        EventType = "agent_canceled"
	EventAgentSummaryProgress EventType = "agent_summary_progress"
)

type ToolCall struct {
	ID       string
	Name     string
	Input    string
	Type     string
	Finished bool
}

type ToolResult struct {
	ToolCallID string
	Name       string
	Content    string
	Metadata   string
	IsError    bool
}

type MessageSnapshot struct {
	ID           string
	SessionID    string
	Role         Role
	Text         string
	Thinking     string
	ToolCalls    []ToolCall
	ToolResults  []ToolResult
	FinishReason string
	Finished     bool
	Model        string
	CreatedAt    int64
	UpdatedAt    int64
}

type Event struct {
	Type      EventType
	SessionID string
	Message   *MessageSnapshot
	Delta     string
	Error     error
	Progress  string
}

type Session struct {
	ID               string
	ParentSessionID  string
	Title            string
	PromptTokens     int64
	CompletionTokens int64
	SummaryMessageID string
	Cost             float64
	CreatedAt        int64
	UpdatedAt        int64
}

type RunRequest struct {
	SessionID    string
	SessionTitle string
	Prompt       string
	AutoApprove  bool
}

type RunResult struct {
	SessionID string
	Message   MessageSnapshot
}

type StreamHandler func(event Event)

type Options struct {
	WorkingDir  string
	Debug       bool
	AutoApprove bool
}

type Client struct {
	app    *appcore.App
	conn   *sql.DB
	ctx    context.Context
	cancel context.CancelFunc

	mu          sync.Mutex
	autoApprove bool
}

func NewClient(ctx context.Context, opts Options) (*Client, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	workingDir := strings.TrimSpace(opts.WorkingDir)
	if workingDir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("resolve working directory: %w", err)
		}
		workingDir = cwd
	}

	if _, err := config.Load(workingDir, opts.Debug); err != nil {
		return nil, fmt.Errorf("load opencode config: %w", err)
	}

	conn, err := db.Connect()
	if err != nil {
		return nil, fmt.Errorf("connect opencode db: %w", err)
	}

	appCtx, cancel := context.WithCancel(ctx)
	appInstance, err := appcore.New(appCtx, conn)
	if err != nil {
		cancel()
		_ = conn.Close()
		return nil, fmt.Errorf("create opencode app: %w", err)
	}

	return &Client{
		app:         appInstance,
		conn:        conn,
		ctx:         appCtx,
		cancel:      cancel,
		autoApprove: opts.AutoApprove,
	}, nil
}

func (c *Client) Close() error {
	if c == nil {
		return nil
	}
	c.cancel()
	c.app.Shutdown()
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

func (c *Client) CreateSession(ctx context.Context, title string) (*Session, error) {
	if c == nil || c.app == nil {
		return nil, errors.New("opencode sdk client is not initialized")
	}
	if strings.TrimSpace(title) == "" {
		title = "SDK Session"
	}
	sess, err := c.app.Sessions.Create(ctx, title)
	if err != nil {
		return nil, err
	}
	result := toSession(sess)
	return &result, nil
}

func (c *Client) SendMessageStream(ctx context.Context, req RunRequest, handler StreamHandler) (*RunResult, error) {
	if c == nil || c.app == nil {
		return nil, errors.New("opencode sdk client is not initialized")
	}
	if ctx == nil {
		ctx = c.ctx
	}
	prompt := strings.TrimSpace(req.Prompt)
	if prompt == "" {
		return nil, errors.New("prompt is required")
	}

	sess, err := c.ensureSession(ctx, req)
	if err != nil {
		return nil, err
	}

	shouldAutoApprove := c.autoApprove || req.AutoApprove
	if shouldAutoApprove {
		c.app.Permissions.AutoApproveSession(sess.ID)
	}

	if handler != nil {
		handler(Event{Type: EventSessionCreated, SessionID: sess.ID})
	}

	messageSub := c.app.Messages.Subscribe(ctx)
	done, err := c.app.CoderAgent.Run(ctx, sess.ID, prompt)
	if err != nil {
		return nil, err
	}

	seenText := map[string]int{}
	seenThinking := map[string]int{}
	for {
		select {
		case msgEvent, ok := <-messageSub:
			if !ok {
				continue
			}
			if msgEvent.Payload.SessionID != sess.ID {
				continue
			}
			snapshot := toSnapshot(msgEvent.Payload)
			emitMessageEvent(handler, sess.ID, msgEvent.Type, snapshot)
			emitAssistantDeltaEvents(handler, sess.ID, snapshot, seenText, seenThinking)

		case agentResult, ok := <-done:
			if !ok {
				return nil, errors.New("agent result channel closed")
			}
			if agentResult.Error != nil {
				if errors.Is(agentResult.Error, context.Canceled) || errors.Is(agentResult.Error, llmagent.ErrRequestCancelled) {
					emit(handler, Event{Type: EventAgentCanceled, SessionID: sess.ID, Error: agentResult.Error})
					return nil, agentResult.Error
				}
				emit(handler, Event{Type: EventAgentError, SessionID: sess.ID, Error: agentResult.Error})
				return nil, agentResult.Error
			}

			finalSnapshot := toSnapshot(agentResult.Message)
			emit(handler, Event{Type: EventAgentCompleted, SessionID: sess.ID, Message: &finalSnapshot})
			return &RunResult{SessionID: sess.ID, Message: finalSnapshot}, nil

		case <-ctx.Done():
			emit(handler, Event{Type: EventAgentCanceled, SessionID: sess.ID, Error: ctx.Err()})
			return nil, ctx.Err()
		}
	}
}

func emitMessageEvent(handler StreamHandler, sessionID string, typ pubsub.EventType, snapshot MessageSnapshot) {
	if handler == nil {
		return
	}
	if typ == pubsub.CreatedEvent {
		handler(Event{Type: EventMessageCreated, SessionID: sessionID, Message: &snapshot})
		return
	}
	if typ == pubsub.UpdatedEvent {
		handler(Event{Type: EventMessageUpdated, SessionID: sessionID, Message: &snapshot})
	}
}

func emitAssistantDeltaEvents(
	handler StreamHandler,
	sessionID string,
	snapshot MessageSnapshot,
	seenText map[string]int,
	seenThinking map[string]int,
) {
	if handler == nil || snapshot.Role != RoleAssistant {
		return
	}
	prevThinking := seenThinking[snapshot.ID]
	if len(snapshot.Thinking) > prevThinking {
		handler(Event{
			Type:      EventAssistantThinkDelta,
			SessionID: sessionID,
			Message:   &snapshot,
			Delta:     snapshot.Thinking[prevThinking:],
		})
		seenThinking[snapshot.ID] = len(snapshot.Thinking)
	}

	prevText := seenText[snapshot.ID]
	if len(snapshot.Text) > prevText {
		handler(Event{
			Type:      EventAssistantTextDelta,
			SessionID: sessionID,
			Message:   &snapshot,
			Delta:     snapshot.Text[prevText:],
		})
		seenText[snapshot.ID] = len(snapshot.Text)
	}
}

func emit(handler StreamHandler, event Event) {
	if handler != nil {
		handler(event)
	}
}

func (c *Client) ensureSession(ctx context.Context, req RunRequest) (session.Session, error) {
	if strings.TrimSpace(req.SessionID) != "" {
		sess, err := c.app.Sessions.Get(ctx, req.SessionID)
		if err == nil {
			return sess, nil
		}
	}
	title := strings.TrimSpace(req.SessionTitle)
	if title == "" {
		title = "SDK Session"
	}
	return c.app.Sessions.Create(ctx, title)
}

func toSession(sess session.Session) Session {
	return Session{
		ID:               sess.ID,
		ParentSessionID:  sess.ParentSessionID,
		Title:            sess.Title,
		PromptTokens:     sess.PromptTokens,
		CompletionTokens: sess.CompletionTokens,
		SummaryMessageID: sess.SummaryMessageID,
		Cost:             sess.Cost,
		CreatedAt:        sess.CreatedAt,
		UpdatedAt:        sess.UpdatedAt,
	}
}

func toSnapshot(msg message.Message) MessageSnapshot {
	toolCalls := msg.ToolCalls()
	calls := make([]ToolCall, 0, len(toolCalls))
	for _, call := range toolCalls {
		calls = append(calls, ToolCall{
			ID:       call.ID,
			Name:     call.Name,
			Input:    call.Input,
			Type:     call.Type,
			Finished: call.Finished,
		})
	}
	toolResults := msg.ToolResults()
	results := make([]ToolResult, 0, len(toolResults))
	for _, result := range toolResults {
		results = append(results, ToolResult{
			ToolCallID: result.ToolCallID,
			Name:       result.Name,
			Content:    result.Content,
			Metadata:   result.Metadata,
			IsError:    result.IsError,
		})
	}

	finishReason := ""
	if finish := msg.FinishPart(); finish != nil {
		finishReason = string(finish.Reason)
	}

	return MessageSnapshot{
		ID:           msg.ID,
		SessionID:    msg.SessionID,
		Role:         Role(msg.Role),
		Text:         msg.Content().String(),
		Thinking:     msg.ReasoningContent().String(),
		ToolCalls:    calls,
		ToolResults:  results,
		FinishReason: finishReason,
		Finished:     msg.IsFinished(),
		Model:        string(msg.Model),
		CreatedAt:    msg.CreatedAt,
		UpdatedAt:    msg.UpdatedAt,
	}
}
