package sdk

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/biox-dev/opencode/internal/config"
	"github.com/biox-dev/opencode/internal/history"
	llmagent "github.com/biox-dev/opencode/internal/llm/agent"
	"github.com/biox-dev/opencode/internal/lsp"
	"github.com/biox-dev/opencode/internal/message"
	"github.com/biox-dev/opencode/internal/permission"
	"github.com/biox-dev/opencode/internal/pubsub"
	"github.com/biox-dev/opencode/internal/session"
	"github.com/google/uuid"
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
	ctx         context.Context
	cancel      context.CancelFunc
	autoApprove bool

	permissions permission.Service
	sessions    session.Service
	messages    message.Service
	history     history.Service
	coderAgent  llmagent.Service
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

	appCtx, cancel := context.WithCancel(ctx)

	permissionsSvc := permission.NewPermissionService()
	sessionsSvc := newInMemorySessionService()
	messagesSvc := newInMemoryMessageService()
	historySvc := newInMemoryHistoryService()

	agentSvc, err := llmagent.NewAgent(
		config.AgentCoder,
		sessionsSvc,
		messagesSvc,
		llmagent.CoderAgentTools(
			permissionsSvc,
			sessionsSvc,
			messagesSvc,
			historySvc,
			map[string]*lsp.Client{},
		),
	)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("create opencode agent: %w", err)
	}

	return &Client{
		ctx:         appCtx,
		cancel:      cancel,
		autoApprove: opts.AutoApprove,
		permissions: permissionsSvc,
		sessions:    sessionsSvc,
		messages:    messagesSvc,
		history:     historySvc,
		coderAgent:  agentSvc,
	}, nil
}

func (c *Client) Close() error {
	if c == nil {
		return nil
	}
	c.cancel()
	shutdown := func(target any) {
		if svc, ok := target.(interface{ Shutdown() }); ok {
			svc.Shutdown()
		}
	}
	shutdown(c.permissions)
	shutdown(c.sessions)
	shutdown(c.messages)
	shutdown(c.history)
	shutdown(c.coderAgent)
	return nil
}

func (c *Client) CreateSession(ctx context.Context, title string) (*Session, error) {
	if c == nil || c.coderAgent == nil {
		return nil, errors.New("opencode sdk client is not initialized")
	}
	if strings.TrimSpace(title) == "" {
		title = "SDK Session"
	}
	sess, err := c.sessions.Create(ctx, title)
	if err != nil {
		return nil, err
	}
	result := toSession(sess)
	return &result, nil
}

func (c *Client) SendMessageStream(ctx context.Context, req RunRequest, handler StreamHandler) (*RunResult, error) {
	if c == nil || c.coderAgent == nil {
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
		c.permissions.AutoApproveSession(sess.ID)
	}

	if handler != nil {
		handler(Event{Type: EventSessionCreated, SessionID: sess.ID})
	}

	messageSub := c.messages.Subscribe(ctx)
	done, err := c.coderAgent.Run(ctx, sess.ID, prompt)
	if err != nil {
		return nil, err
	}

	seenText := map[string]int{}
	seenThinking := map[string]int{}
	finalAssistantSeenInMessageStream := map[string]bool{}
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
			if msgEvent.Type == pubsub.UpdatedEvent && snapshot.Role == RoleAssistant && snapshot.Finished {
				finalAssistantSeenInMessageStream[snapshot.ID] = true
			}

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
			completedEvent := Event{Type: EventAgentCompleted, SessionID: sess.ID}
			if !finalAssistantSeenInMessageStream[finalSnapshot.ID] {
				completedEvent.Message = &finalSnapshot
			}
			emit(handler, completedEvent)
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
		sess, err := c.sessions.Get(ctx, req.SessionID)
		if err == nil {
			return sess, nil
		}
	}
	title := strings.TrimSpace(req.SessionTitle)
	if title == "" {
		title = "SDK Session"
	}
	return c.sessions.Create(ctx, title)
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

type inMemorySessionService struct {
	*pubsub.Broker[session.Session]
	mu    sync.RWMutex
	items map[string]session.Session
	order []string
}

func newInMemorySessionService() *inMemorySessionService {
	return &inMemorySessionService{
		Broker: pubsub.NewBroker[session.Session](),
		items:  make(map[string]session.Session),
		order:  make([]string, 0),
	}
}

func (s *inMemorySessionService) Create(ctx context.Context, title string) (session.Session, error) {
	_ = ctx
	now := time.Now().Unix()
	item := session.Session{
		ID:        uuid.New().String(),
		Title:     title,
		CreatedAt: now,
		UpdatedAt: now,
	}

	s.mu.Lock()
	s.items[item.ID] = item
	s.order = append(s.order, item.ID)
	s.mu.Unlock()

	s.Publish(pubsub.CreatedEvent, item)
	return item, nil
}

func (s *inMemorySessionService) CreateTitleSession(ctx context.Context, parentSessionID string) (session.Session, error) {
	_ = ctx
	now := time.Now().Unix()
	item := session.Session{
		ID:              "title-" + parentSessionID,
		ParentSessionID: parentSessionID,
		Title:           "Generate a title",
		CreatedAt:       now,
		UpdatedAt:       now,
	}

	s.mu.Lock()
	if _, exists := s.items[item.ID]; exists {
		s.mu.Unlock()
		return session.Session{}, fmt.Errorf("session already exists: %s", item.ID)
	}
	s.items[item.ID] = item
	s.order = append(s.order, item.ID)
	s.mu.Unlock()

	s.Publish(pubsub.CreatedEvent, item)
	return item, nil
}

func (s *inMemorySessionService) CreateTaskSession(ctx context.Context, toolCallID, parentSessionID, title string) (session.Session, error) {
	_ = ctx
	now := time.Now().Unix()
	item := session.Session{
		ID:              toolCallID,
		ParentSessionID: parentSessionID,
		Title:           title,
		CreatedAt:       now,
		UpdatedAt:       now,
	}

	s.mu.Lock()
	if _, exists := s.items[item.ID]; exists {
		s.mu.Unlock()
		return session.Session{}, fmt.Errorf("session already exists: %s", item.ID)
	}
	s.items[item.ID] = item
	s.order = append(s.order, item.ID)
	s.mu.Unlock()

	s.Publish(pubsub.CreatedEvent, item)
	return item, nil
}

func (s *inMemorySessionService) Get(ctx context.Context, id string) (session.Session, error) {
	_ = ctx
	s.mu.RLock()
	item, ok := s.items[id]
	s.mu.RUnlock()
	if !ok {
		return session.Session{}, fmt.Errorf("session not found: %s", id)
	}
	return item, nil
}

func (s *inMemorySessionService) List(ctx context.Context) ([]session.Session, error) {
	_ = ctx
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]session.Session, 0, len(s.order))
	for _, id := range s.order {
		if item, ok := s.items[id]; ok {
			result = append(result, item)
		}
	}
	return result, nil
}

func (s *inMemorySessionService) Save(ctx context.Context, item session.Session) (session.Session, error) {
	_ = ctx
	s.mu.Lock()
	_, exists := s.items[item.ID]
	if !exists {
		s.mu.Unlock()
		return session.Session{}, fmt.Errorf("session not found: %s", item.ID)
	}
	if item.CreatedAt == 0 {
		item.CreatedAt = s.items[item.ID].CreatedAt
	}
	item.UpdatedAt = time.Now().Unix()
	s.items[item.ID] = item
	s.mu.Unlock()

	s.Publish(pubsub.UpdatedEvent, item)
	return item, nil
}

func (s *inMemorySessionService) Delete(ctx context.Context, id string) error {
	_ = ctx
	s.mu.Lock()
	item, exists := s.items[id]
	if !exists {
		s.mu.Unlock()
		return fmt.Errorf("session not found: %s", id)
	}
	delete(s.items, id)
	for i, v := range s.order {
		if v == id {
			s.order = append(s.order[:i], s.order[i+1:]...)
			break
		}
	}
	s.mu.Unlock()

	s.Publish(pubsub.DeletedEvent, item)
	return nil
}

type inMemoryMessageService struct {
	*pubsub.Broker[message.Message]
	mu        sync.RWMutex
	items     map[string]message.Message
	bySession map[string][]string
}

func newInMemoryMessageService() *inMemoryMessageService {
	return &inMemoryMessageService{
		Broker:    pubsub.NewBroker[message.Message](),
		items:     make(map[string]message.Message),
		bySession: make(map[string][]string),
	}
}

func (s *inMemoryMessageService) Create(ctx context.Context, sessionID string, params message.CreateMessageParams) (message.Message, error) {
	_ = ctx
	parts := make([]message.ContentPart, 0, len(params.Parts)+1)
	parts = append(parts, params.Parts...)
	if params.Role != message.Assistant {
		parts = append(parts, message.Finish{Reason: "stop"})
	}
	now := time.Now().Unix()
	msg := message.Message{
		ID:        uuid.New().String(),
		Role:      params.Role,
		SessionID: sessionID,
		Parts:     parts,
		Model:     params.Model,
		CreatedAt: now,
		UpdatedAt: now,
	}

	s.mu.Lock()
	s.items[msg.ID] = msg
	s.bySession[sessionID] = append(s.bySession[sessionID], msg.ID)
	s.mu.Unlock()

	s.Publish(pubsub.CreatedEvent, msg)
	return msg, nil
}

func (s *inMemoryMessageService) Update(ctx context.Context, msg message.Message) error {
	_ = ctx
	s.mu.Lock()
	current, ok := s.items[msg.ID]
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("message not found: %s", msg.ID)
	}
	if msg.CreatedAt == 0 {
		msg.CreatedAt = current.CreatedAt
	}
	msg.UpdatedAt = time.Now().Unix()
	s.items[msg.ID] = msg
	s.mu.Unlock()

	s.Publish(pubsub.UpdatedEvent, msg)
	return nil
}

func (s *inMemoryMessageService) Get(ctx context.Context, id string) (message.Message, error) {
	_ = ctx
	s.mu.RLock()
	msg, ok := s.items[id]
	s.mu.RUnlock()
	if !ok {
		return message.Message{}, fmt.Errorf("message not found: %s", id)
	}
	return msg, nil
}

func (s *inMemoryMessageService) List(ctx context.Context, sessionID string) ([]message.Message, error) {
	_ = ctx
	s.mu.RLock()
	ids := append([]string(nil), s.bySession[sessionID]...)
	result := make([]message.Message, 0, len(ids))
	for _, id := range ids {
		if msg, ok := s.items[id]; ok {
			result = append(result, msg)
		}
	}
	s.mu.RUnlock()

	sort.SliceStable(result, func(i, j int) bool {
		return result[i].CreatedAt < result[j].CreatedAt
	})
	return result, nil
}

func (s *inMemoryMessageService) Delete(ctx context.Context, id string) error {
	_ = ctx
	s.mu.Lock()
	msg, ok := s.items[id]
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("message not found: %s", id)
	}
	delete(s.items, id)
	ids := s.bySession[msg.SessionID]
	for i, v := range ids {
		if v == id {
			s.bySession[msg.SessionID] = append(ids[:i], ids[i+1:]...)
			break
		}
	}
	s.mu.Unlock()

	s.Publish(pubsub.DeletedEvent, msg)
	return nil
}

func (s *inMemoryMessageService) DeleteSessionMessages(ctx context.Context, sessionID string) error {
	msgs, err := s.List(ctx, sessionID)
	if err != nil {
		return err
	}
	for _, msg := range msgs {
		if err := s.Delete(ctx, msg.ID); err != nil {
			return err
		}
	}
	return nil
}

type inMemoryHistoryService struct {
	*pubsub.Broker[history.File]
	mu    sync.RWMutex
	items map[string]history.File
}

func newInMemoryHistoryService() *inMemoryHistoryService {
	return &inMemoryHistoryService{
		Broker: pubsub.NewBroker[history.File](),
		items:  make(map[string]history.File),
	}
}

func (s *inMemoryHistoryService) Create(ctx context.Context, sessionID, path, content string) (history.File, error) {
	_ = ctx
	return s.createWithVersion(sessionID, path, content, history.InitialVersion), nil
}

func (s *inMemoryHistoryService) CreateVersion(ctx context.Context, sessionID, path, content string) (history.File, error) {
	_ = ctx

	latest := s.latestVersionForPath(path)
	if latest == "" {
		return s.createWithVersion(sessionID, path, content, history.InitialVersion), nil
	}
	if latest == history.InitialVersion {
		return s.createWithVersion(sessionID, path, content, "v1"), nil
	}
	if strings.HasPrefix(latest, "v") {
		if n, err := strconv.Atoi(strings.TrimPrefix(latest, "v")); err == nil {
			return s.createWithVersion(sessionID, path, content, fmt.Sprintf("v%d", n+1)), nil
		}
	}
	return s.createWithVersion(sessionID, path, content, fmt.Sprintf("v%d", time.Now().Unix())), nil
}

func (s *inMemoryHistoryService) Get(ctx context.Context, id string) (history.File, error) {
	_ = ctx
	s.mu.RLock()
	item, ok := s.items[id]
	s.mu.RUnlock()
	if !ok {
		return history.File{}, fmt.Errorf("history file not found: %s", id)
	}
	return item, nil
}

func (s *inMemoryHistoryService) GetByPathAndSession(ctx context.Context, path, sessionID string) (history.File, error) {
	_ = ctx
	s.mu.RLock()
	defer s.mu.RUnlock()

	var newest history.File
	found := false
	for _, item := range s.items {
		if item.Path == path && item.SessionID == sessionID {
			if !found || item.CreatedAt > newest.CreatedAt {
				newest = item
				found = true
			}
		}
	}
	if !found {
		return history.File{}, fmt.Errorf("history file not found for path=%s session=%s", path, sessionID)
	}
	return newest, nil
}

func (s *inMemoryHistoryService) ListBySession(ctx context.Context, sessionID string) ([]history.File, error) {
	_ = ctx
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]history.File, 0)
	for _, item := range s.items {
		if item.SessionID == sessionID {
			result = append(result, item)
		}
	}
	sort.SliceStable(result, func(i, j int) bool {
		return result[i].CreatedAt > result[j].CreatedAt
	})
	return result, nil
}

func (s *inMemoryHistoryService) ListLatestSessionFiles(ctx context.Context, sessionID string) ([]history.File, error) {
	_ = ctx
	s.mu.RLock()
	defer s.mu.RUnlock()

	byPath := make(map[string]history.File)
	for _, item := range s.items {
		if item.SessionID != sessionID {
			continue
		}
		curr, ok := byPath[item.Path]
		if !ok || item.CreatedAt > curr.CreatedAt {
			byPath[item.Path] = item
		}
	}

	result := make([]history.File, 0, len(byPath))
	for _, item := range byPath {
		result = append(result, item)
	}
	sort.SliceStable(result, func(i, j int) bool {
		return result[i].CreatedAt > result[j].CreatedAt
	})
	return result, nil
}

func (s *inMemoryHistoryService) Update(ctx context.Context, file history.File) (history.File, error) {
	_ = ctx
	s.mu.Lock()
	current, ok := s.items[file.ID]
	if !ok {
		s.mu.Unlock()
		return history.File{}, fmt.Errorf("history file not found: %s", file.ID)
	}
	if file.CreatedAt == 0 {
		file.CreatedAt = current.CreatedAt
	}
	file.UpdatedAt = time.Now().Unix()
	s.items[file.ID] = file
	s.mu.Unlock()

	s.Publish(pubsub.UpdatedEvent, file)
	return file, nil
}

func (s *inMemoryHistoryService) Delete(ctx context.Context, id string) error {
	_ = ctx
	s.mu.Lock()
	item, ok := s.items[id]
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("history file not found: %s", id)
	}
	delete(s.items, id)
	s.mu.Unlock()

	s.Publish(pubsub.DeletedEvent, item)
	return nil
}

func (s *inMemoryHistoryService) DeleteSessionFiles(ctx context.Context, sessionID string) error {
	files, err := s.ListBySession(ctx, sessionID)
	if err != nil {
		return err
	}
	for _, file := range files {
		if err := s.Delete(ctx, file.ID); err != nil {
			return err
		}
	}
	return nil
}

func (s *inMemoryHistoryService) latestVersionForPath(path string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	latestTs := int64(0)
	latestVersion := ""
	for _, item := range s.items {
		if item.Path == path && item.CreatedAt >= latestTs {
			latestTs = item.CreatedAt
			latestVersion = item.Version
		}
	}
	return latestVersion
}

func (s *inMemoryHistoryService) createWithVersion(sessionID, path, content, version string) history.File {
	now := time.Now().Unix()
	item := history.File{
		ID:        uuid.New().String(),
		SessionID: sessionID,
		Path:      path,
		Content:   content,
		Version:   version,
		CreatedAt: now,
		UpdatedAt: now,
	}

	s.mu.Lock()
	s.items[item.ID] = item
	s.mu.Unlock()

	s.Publish(pubsub.CreatedEvent, item)
	return item
}
