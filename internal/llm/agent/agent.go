// Package agent contains the implementation of the AI agent service.
package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/catwalk/pkg/catwalk"
	"github.com/xinggaoya/crush/internal/config"
	"github.com/xinggaoya/crush/internal/csync"
	"github.com/xinggaoya/crush/internal/event"
	"github.com/xinggaoya/crush/internal/history"
	"github.com/xinggaoya/crush/internal/llm/prompt"
	"github.com/xinggaoya/crush/internal/llm/provider"
	"github.com/xinggaoya/crush/internal/llm/tools"
	"github.com/xinggaoya/crush/internal/log"
	"github.com/xinggaoya/crush/internal/lsp"
	"github.com/xinggaoya/crush/internal/message"
	"github.com/xinggaoya/crush/internal/permission"
	"github.com/xinggaoya/crush/internal/pubsub"
	"github.com/xinggaoya/crush/internal/session"
	"github.com/xinggaoya/crush/internal/shell"
)

type AgentEventType string

const (
	AgentEventTypeError     AgentEventType = "error"
	AgentEventTypeResponse  AgentEventType = "response"
	AgentEventTypeSummarize AgentEventType = "summarize"
)

type AgentEvent struct {
	Type    AgentEventType
	Message message.Message
	Error   error

	// When summarizing
	SessionID string
	Progress  string
	Done      bool
}

type Service interface {
	pubsub.Suscriber[AgentEvent]
	Model() catwalk.Model
	Run(ctx context.Context, sessionID string, content string, attachments ...message.Attachment) (<-chan AgentEvent, error)
	Cancel(sessionID string)
	CancelAll()
	IsSessionBusy(sessionID string) bool
	IsBusy() bool
	Summarize(ctx context.Context, sessionID string) error
	UpdateModel() error
	QueuedPrompts(sessionID string) int
	ClearQueue(sessionID string)
}

type agent struct {
	*pubsub.Broker[AgentEvent]
	agentCfg    config.Agent
	sessions    session.Service
	messages    message.Service
	permissions permission.Service
	baseTools   *csync.Map[string, tools.BaseTool]
	mcpTools    *csync.Map[string, tools.BaseTool]
	lspClients  *csync.Map[string, *lsp.Client]

	// We need this to be able to update it when model changes
	agentToolFn  func() (tools.BaseTool, error)
	cleanupFuncs []func()

	provider   provider.Provider
	providerID string

	titleProvider       provider.Provider
	summarizeProvider   provider.Provider
	summarizeProviderID string

	activeRequests *csync.Map[string, context.CancelFunc]
	promptQueue    *csync.Map[string, []string]
}

var agentPromptMap = map[string]prompt.PromptID{
	"coder": prompt.PromptCoder,
	"task":  prompt.PromptTask,
}

func NewAgent(
	ctx context.Context,
	agentCfg config.Agent,
	// These services are needed in the tools
	permissions permission.Service,
	sessions session.Service,
	messages message.Service,
	history history.Service,
	lspClients *csync.Map[string, *lsp.Client],
) (Service, error) {
	cfg := config.Get()

	var agentToolFn func() (tools.BaseTool, error)
	if agentCfg.ID == "coder" && slices.Contains(agentCfg.AllowedTools, AgentToolName) {
		agentToolFn = func() (tools.BaseTool, error) {
			taskAgentCfg := config.Get().Agents["task"]
			if taskAgentCfg.ID == "" {
				return nil, fmt.Errorf("task agent not found in config")
			}
			taskAgent, err := NewAgent(ctx, taskAgentCfg, permissions, sessions, messages, history, lspClients)
			if err != nil {
				return nil, fmt.Errorf("failed to create task agent: %w", err)
			}
			return NewAgentTool(taskAgent, sessions, messages), nil
		}
	}

	providerCfg := config.Get().GetProviderForModel(agentCfg.Model)
	if providerCfg == nil {
		return nil, fmt.Errorf("provider for agent %s not found in config", agentCfg.Name)
	}
	model := config.Get().GetModelByType(agentCfg.Model)

	if model == nil {
		return nil, fmt.Errorf("model not found for agent %s", agentCfg.Name)
	}

	promptID := agentPromptMap[agentCfg.ID]
	if promptID == "" {
		promptID = prompt.PromptDefault
	}
	opts := []provider.ProviderClientOption{
		provider.WithModel(agentCfg.Model),
		provider.WithSystemMessage(prompt.GetPrompt(promptID, providerCfg.ID, config.Get().Options.ContextPaths...)),
	}
	agentProvider, err := provider.NewProvider(*providerCfg, opts...)
	if err != nil {
		return nil, err
	}

	smallModelCfg := cfg.Models[config.SelectedModelTypeSmall]
	var smallModelProviderCfg *config.ProviderConfig
	if smallModelCfg.Provider == providerCfg.ID {
		smallModelProviderCfg = providerCfg
	} else {
		smallModelProviderCfg = cfg.GetProviderForModel(config.SelectedModelTypeSmall)

		if smallModelProviderCfg.ID == "" {
			return nil, fmt.Errorf("provider %s not found in config", smallModelCfg.Provider)
		}
	}
	smallModel := cfg.GetModelByType(config.SelectedModelTypeSmall)
	if smallModel.ID == "" {
		return nil, fmt.Errorf("model %s not found in provider %s", smallModelCfg.Model, smallModelProviderCfg.ID)
	}

	titleOpts := []provider.ProviderClientOption{
		provider.WithModel(config.SelectedModelTypeSmall),
		provider.WithSystemMessage(prompt.GetPrompt(prompt.PromptTitle, smallModelProviderCfg.ID)),
	}
	titleProvider, err := provider.NewProvider(*smallModelProviderCfg, titleOpts...)
	if err != nil {
		return nil, err
	}

	summarizeOpts := []provider.ProviderClientOption{
		provider.WithModel(config.SelectedModelTypeLarge),
		provider.WithSystemMessage(prompt.GetPrompt(prompt.PromptSummarizer, providerCfg.ID)),
	}
	summarizeProvider, err := provider.NewProvider(*providerCfg, summarizeOpts...)
	if err != nil {
		return nil, err
	}

	baseToolsFn := func() map[string]tools.BaseTool {
		slog.Debug("Initializing agent base tools", "agent", agentCfg.ID)
		defer func() {
			slog.Debug("Initialized agent base tools", "agent", agentCfg.ID)
		}()

		// Base tools available to all agents
		cwd := cfg.WorkingDir()
		result := make(map[string]tools.BaseTool)
		for _, tool := range []tools.BaseTool{
			tools.NewBashTool(permissions, cwd, cfg.Options.Attribution),
			tools.NewDownloadTool(permissions, cwd),
			tools.NewEditTool(lspClients, permissions, history, cwd),
			tools.NewMultiEditTool(lspClients, permissions, history, cwd),
			tools.NewFetchTool(permissions, cwd),
			tools.NewGlobTool(cwd),
			tools.NewGrepTool(cwd),
			tools.NewLsTool(permissions, cwd),
			tools.NewSourcegraphTool(),
			tools.NewViewTool(lspClients, permissions, cwd),
			tools.NewWriteTool(lspClients, permissions, history, cwd),
		} {
			result[tool.Name()] = tool
		}
		return result
	}
	mcpToolsFn := func() map[string]tools.BaseTool {
		slog.Debug("Initializing agent mcp tools", "agent", agentCfg.ID)
		defer func() {
			slog.Debug("Initialized agent mcp tools", "agent", agentCfg.ID)
		}()

		mcpToolsOnce.Do(func() {
			doGetMCPTools(ctx, permissions, cfg)
		})

		return maps.Collect(mcpTools.Seq2())
	}

	a := &agent{
		Broker:              pubsub.NewBroker[AgentEvent](),
		agentCfg:            agentCfg,
		provider:            agentProvider,
		providerID:          string(providerCfg.ID),
		messages:            messages,
		sessions:            sessions,
		titleProvider:       titleProvider,
		summarizeProvider:   summarizeProvider,
		summarizeProviderID: string(providerCfg.ID),
		agentToolFn:         agentToolFn,
		activeRequests:      csync.NewMap[string, context.CancelFunc](),
		mcpTools:            csync.NewLazyMap(mcpToolsFn),
		baseTools:           csync.NewLazyMap(baseToolsFn),
		promptQueue:         csync.NewMap[string, []string](),
		permissions:         permissions,
		lspClients:          lspClients,
	}
	a.setupEvents(ctx)
	return a, nil
}

func (a *agent) Model() catwalk.Model {
	return *config.Get().GetModelByType(a.agentCfg.Model)
}

func (a *agent) Cancel(sessionID string) {
	// Cancel regular requests
	if cancel, ok := a.activeRequests.Take(sessionID); ok && cancel != nil {
		slog.Info("Request cancellation initiated", "session_id", sessionID)
		cancel()
	}

	// Also check for summarize requests
	if cancel, ok := a.activeRequests.Take(sessionID + "-summarize"); ok && cancel != nil {
		slog.Info("Summarize cancellation initiated", "session_id", sessionID)
		cancel()
	}

	if a.QueuedPrompts(sessionID) > 0 {
		slog.Info("Clearing queued prompts", "session_id", sessionID)
		a.promptQueue.Del(sessionID)
	}
}

func (a *agent) IsBusy() bool {
	var busy bool
	for cancelFunc := range a.activeRequests.Seq() {
		if cancelFunc != nil {
			busy = true
			break
		}
	}
	return busy
}

func (a *agent) IsSessionBusy(sessionID string) bool {
	_, busy := a.activeRequests.Get(sessionID)
	return busy
}

func (a *agent) QueuedPrompts(sessionID string) int {
	l, ok := a.promptQueue.Get(sessionID)
	if !ok {
		return 0
	}
	return len(l)
}

func (a *agent) generateTitle(ctx context.Context, sessionID string, content string) error {
	if content == "" {
		return nil
	}
	if a.titleProvider == nil {
		return nil
	}
	session, err := a.sessions.Get(ctx, sessionID)
	if err != nil {
		return err
	}
	parts := []message.ContentPart{message.TextContent{
		Text: fmt.Sprintf("Generate a concise title for the following content:\n\n%s", content),
	}}

	// Use streaming approach like summarization
	response := a.titleProvider.StreamResponse(
		ctx,
		[]message.Message{
			{
				Role:  message.User,
				Parts: parts,
			},
		},
		nil,
	)

	var finalResponse *provider.ProviderResponse
	for r := range response {
		if r.Error != nil {
			return r.Error
		}
		finalResponse = r.Response
	}

	if finalResponse == nil {
		return fmt.Errorf("no response received from title provider")
	}

	title := strings.ReplaceAll(finalResponse.Content, "\n", " ")

	if idx := strings.Index(title, "</think>"); idx > 0 {
		title = title[idx+len("</think>"):]
	}

	title = strings.TrimSpace(title)
	if title == "" {
		return nil
	}

	session.Title = title
	_, err = a.sessions.Save(ctx, session)
	return err
}

func (a *agent) err(err error) AgentEvent {
	return AgentEvent{
		Type:  AgentEventTypeError,
		Error: err,
	}
}

func (a *agent) Run(ctx context.Context, sessionID string, content string, attachments ...message.Attachment) (<-chan AgentEvent, error) {
	if !a.Model().SupportsImages && attachments != nil {
		attachments = nil
	}
	events := make(chan AgentEvent, 1)
	if a.IsSessionBusy(sessionID) {
		existing, ok := a.promptQueue.Get(sessionID)
		if !ok {
			existing = []string{}
		}
		existing = append(existing, content)
		a.promptQueue.Set(sessionID, existing)
		return nil, nil
	}

	genCtx, cancel := context.WithCancel(ctx)
	a.activeRequests.Set(sessionID, cancel)
	startTime := time.Now()

	go func() {
		slog.Debug("Request started", "sessionID", sessionID)
		defer log.RecoverPanic("agent.Run", func() {
			events <- a.err(fmt.Errorf("panic while running the agent"))
		})
		var attachmentParts []message.ContentPart
		for _, attachment := range attachments {
			attachmentParts = append(attachmentParts, message.BinaryContent{Path: attachment.FilePath, MIMEType: attachment.MimeType, Data: attachment.Content})
		}
		result := a.processGeneration(genCtx, sessionID, content, attachmentParts)
		if result.Error != nil {
			if isCancelledErr(result.Error) {
				slog.Error("Request canceled", "sessionID", sessionID)
			} else {
				slog.Error("Request errored", "sessionID", sessionID, "error", result.Error.Error())
				event.Error(result.Error)
			}
		} else {
			slog.Debug("Request completed", "sessionID", sessionID)
		}
		a.eventPromptResponded(sessionID, time.Since(startTime).Truncate(time.Second))
		a.activeRequests.Del(sessionID)
		cancel()
		a.Publish(pubsub.CreatedEvent, result)
		events <- result
		close(events)
	}()
	a.eventPromptSent(sessionID)
	return events, nil
}

func (a *agent) processGeneration(ctx context.Context, sessionID, content string, attachmentParts []message.ContentPart) AgentEvent {
	cfg := config.Get()
	// List existing messages; if none, start title generation asynchronously.
	msgs, err := a.messages.List(ctx, sessionID)
	if err != nil {
		return a.err(fmt.Errorf("failed to list messages: %w", err))
	}
	if len(msgs) == 0 {
		go func() {
			defer log.RecoverPanic("agent.Run", func() {
				slog.Error("panic while generating title")
			})
			titleErr := a.generateTitle(ctx, sessionID, content)
			if titleErr != nil && !errors.Is(titleErr, context.Canceled) && !errors.Is(titleErr, context.DeadlineExceeded) {
				slog.Error("failed to generate title", "error", titleErr)
			}
		}()
	}
	session, err := a.sessions.Get(ctx, sessionID)
	if err != nil {
		return a.err(fmt.Errorf("failed to get session: %w", err))
	}
	if session.SummaryMessageID != "" {
		summaryMsgInex := -1
		for i, msg := range msgs {
			if msg.ID == session.SummaryMessageID {
				summaryMsgInex = i
				break
			}
		}
		if summaryMsgInex != -1 {
			msgs = msgs[summaryMsgInex:]
			msgs[0].Role = message.User
		}
	}

	userMsg, err := a.createUserMessage(ctx, sessionID, content, attachmentParts)
	if err != nil {
		return a.err(fmt.Errorf("failed to create user message: %w", err))
	}
	// Append the new user message to the conversation history.
	msgHistory := append(msgs, userMsg)

	for {
		// Check for cancellation before each iteration
		select {
		case <-ctx.Done():
			return a.err(ctx.Err())
		default:
			// Continue processing
		}
		agentMessage, toolResults, err := a.streamAndHandleEvents(ctx, sessionID, msgHistory)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				agentMessage.AddFinish(message.FinishReasonCanceled, "Request cancelled", "")
				a.messages.Update(context.Background(), agentMessage)
				return a.err(ErrRequestCancelled)
			}
			return a.err(fmt.Errorf("failed to process events: %w", err))
		}
		if cfg.Options.Debug {
			slog.Info("Result", "message", agentMessage.FinishReason(), "toolResults", toolResults)
		}
		if (agentMessage.FinishReason() == message.FinishReasonToolUse) && toolResults != nil {
			// We are not done, we need to respond with the tool response
			msgHistory = append(msgHistory, agentMessage, *toolResults)
			// If there are queued prompts, process the next one
			nextPrompt, ok := a.promptQueue.Take(sessionID)
			if ok {
				for _, prompt := range nextPrompt {
					// Create a new user message for the queued prompt
					userMsg, err := a.createUserMessage(ctx, sessionID, prompt, nil)
					if err != nil {
						return a.err(fmt.Errorf("failed to create user message for queued prompt: %w", err))
					}
					// Append the new user message to the conversation history
					msgHistory = append(msgHistory, userMsg)
				}
			}

			continue
		} else if agentMessage.FinishReason() == message.FinishReasonEndTurn {
			queuePrompts, ok := a.promptQueue.Take(sessionID)
			if ok {
				for _, prompt := range queuePrompts {
					if prompt == "" {
						continue
					}
					userMsg, err := a.createUserMessage(ctx, sessionID, prompt, nil)
					if err != nil {
						return a.err(fmt.Errorf("failed to create user message for queued prompt: %w", err))
					}
					msgHistory = append(msgHistory, userMsg)
				}
				continue
			}
		}
		if agentMessage.FinishReason() == "" {
			// Kujtim: could not track down where this is happening but this means its cancelled
			agentMessage.AddFinish(message.FinishReasonCanceled, "Request cancelled", "")
			_ = a.messages.Update(context.Background(), agentMessage)
			return a.err(ErrRequestCancelled)
		}
		return AgentEvent{
			Type:    AgentEventTypeResponse,
			Message: agentMessage,
			Done:    true,
		}
	}
}

func (a *agent) createUserMessage(ctx context.Context, sessionID, content string, attachmentParts []message.ContentPart) (message.Message, error) {
	parts := []message.ContentPart{message.TextContent{Text: content}}
	parts = append(parts, attachmentParts...)
	return a.messages.Create(ctx, sessionID, message.CreateMessageParams{
		Role:  message.User,
		Parts: parts,
	})
}

func (a *agent) getAllTools() ([]tools.BaseTool, error) {
	var allTools []tools.BaseTool
	for tool := range a.baseTools.Seq() {
		if a.agentCfg.AllowedTools == nil || slices.Contains(a.agentCfg.AllowedTools, tool.Name()) {
			allTools = append(allTools, tool)
		}
	}
	if a.agentCfg.ID == "coder" {
		allTools = slices.AppendSeq(allTools, a.mcpTools.Seq())
		if a.lspClients.Len() > 0 {
			allTools = append(allTools, tools.NewDiagnosticsTool(a.lspClients))
		}
	}
	if a.agentToolFn != nil {
		agentTool, agentToolErr := a.agentToolFn()
		if agentToolErr != nil {
			return nil, agentToolErr
		}
		allTools = append(allTools, agentTool)
	}
	return allTools, nil
}

func (a *agent) streamAndHandleEvents(ctx context.Context, sessionID string, msgHistory []message.Message) (message.Message, *message.Message, error) {
	ctx = context.WithValue(ctx, tools.SessionIDContextKey, sessionID)

	// Create the assistant message first so the spinner shows immediately
	assistantMsg, err := a.messages.Create(ctx, sessionID, message.CreateMessageParams{
		Role:     message.Assistant,
		Parts:    []message.ContentPart{},
		Model:    a.Model().ID,
		Provider: a.providerID,
	})
	if err != nil {
		return assistantMsg, nil, fmt.Errorf("failed to create assistant message: %w", err)
	}

	allTools, toolsErr := a.getAllTools()
	if toolsErr != nil {
		return assistantMsg, nil, toolsErr
	}
	// Now collect tools (which may block on MCP initialization)
	eventChan := a.provider.StreamResponse(ctx, msgHistory, allTools)

	// Add the session and message ID into the context if needed by tools.
	ctx = context.WithValue(ctx, tools.MessageIDContextKey, assistantMsg.ID)

loop:
	for {
		select {
		case event, ok := <-eventChan:
			if !ok {
				break loop
			}
			if processErr := a.processEvent(ctx, sessionID, &assistantMsg, event); processErr != nil {
				if errors.Is(processErr, context.Canceled) {
					a.finishMessage(context.Background(), &assistantMsg, message.FinishReasonCanceled, "Request cancelled", "")
				} else {
					a.finishMessage(ctx, &assistantMsg, message.FinishReasonError, "API Error", processErr.Error())
				}
				return assistantMsg, nil, processErr
			}
		case <-ctx.Done():
			a.finishMessage(context.Background(), &assistantMsg, message.FinishReasonCanceled, "Request cancelled", "")
			return assistantMsg, nil, ctx.Err()
		}
	}

	toolResults := make([]message.ToolResult, len(assistantMsg.ToolCalls()))
	toolCalls := assistantMsg.ToolCalls()
	for i, toolCall := range toolCalls {
		select {
		case <-ctx.Done():
			a.finishMessage(context.Background(), &assistantMsg, message.FinishReasonCanceled, "Request cancelled", "")
			// Make all future tool calls cancelled
			for j := i; j < len(toolCalls); j++ {
				toolResults[j] = message.ToolResult{
					ToolCallID: toolCalls[j].ID,
					Content:    "Tool execution canceled by user",
					IsError:    true,
				}
			}
			goto out
		default:
			// Continue processing
			var tool tools.BaseTool
			allTools, _ = a.getAllTools()
			for _, availableTool := range allTools {
				if availableTool.Info().Name == toolCall.Name {
					tool = availableTool
					break
				}
			}

			// Tool not found
			if tool == nil {
				toolResults[i] = message.ToolResult{
					ToolCallID: toolCall.ID,
					Content:    fmt.Sprintf("Tool not found: %s", toolCall.Name),
					IsError:    true,
				}
				continue
			}

			// Run tool in goroutine to allow cancellation
			type toolExecResult struct {
				response tools.ToolResponse
				err      error
			}
			resultChan := make(chan toolExecResult, 1)

			go func() {
				response, err := tool.Run(ctx, tools.ToolCall{
					ID:    toolCall.ID,
					Name:  toolCall.Name,
					Input: toolCall.Input,
				})
				resultChan <- toolExecResult{response: response, err: err}
			}()

			var toolResponse tools.ToolResponse
			var toolErr error

			select {
			case <-ctx.Done():
				a.finishMessage(context.Background(), &assistantMsg, message.FinishReasonCanceled, "Request cancelled", "")
				// Mark remaining tool calls as cancelled
				for j := i; j < len(toolCalls); j++ {
					toolResults[j] = message.ToolResult{
						ToolCallID: toolCalls[j].ID,
						Content:    "Tool execution canceled by user",
						IsError:    true,
					}
				}
				goto out
			case result := <-resultChan:
				toolResponse = result.response
				toolErr = result.err
			}

			if toolErr != nil {
				slog.Error("Tool execution error", "toolCall", toolCall.ID, "error", toolErr)
				if errors.Is(toolErr, permission.ErrorPermissionDenied) {
					toolResults[i] = message.ToolResult{
						ToolCallID: toolCall.ID,
						Content:    "Permission denied",
						IsError:    true,
					}
					for j := i + 1; j < len(toolCalls); j++ {
						toolResults[j] = message.ToolResult{
							ToolCallID: toolCalls[j].ID,
							Content:    "Tool execution canceled by user",
							IsError:    true,
						}
					}
					a.finishMessage(ctx, &assistantMsg, message.FinishReasonPermissionDenied, "Permission denied", "")
					break
				}
			}
			toolResults[i] = message.ToolResult{
				ToolCallID: toolCall.ID,
				Content:    toolResponse.Content,
				Metadata:   toolResponse.Metadata,
				IsError:    toolResponse.IsError,
			}
		}
	}
out:
	if len(toolResults) == 0 {
		return assistantMsg, nil, nil
	}
	parts := make([]message.ContentPart, 0)
	for _, tr := range toolResults {
		parts = append(parts, tr)
	}
	msg, err := a.messages.Create(context.Background(), assistantMsg.SessionID, message.CreateMessageParams{
		Role:     message.Tool,
		Parts:    parts,
		Provider: a.providerID,
	})
	if err != nil {
		return assistantMsg, nil, fmt.Errorf("failed to create cancelled tool message: %w", err)
	}

	return assistantMsg, &msg, err
}

func (a *agent) finishMessage(ctx context.Context, msg *message.Message, finishReason message.FinishReason, message, details string) {
	msg.AddFinish(finishReason, message, details)
	_ = a.messages.Update(ctx, *msg)
}

func (a *agent) processEvent(ctx context.Context, sessionID string, assistantMsg *message.Message, event provider.ProviderEvent) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		// Continue processing.
	}

	switch event.Type {
	case provider.EventThinkingDelta:
		assistantMsg.AppendReasoningContent(event.Thinking)
		return a.messages.Update(ctx, *assistantMsg)
	case provider.EventSignatureDelta:
		assistantMsg.AppendReasoningSignature(event.Signature)
		return a.messages.Update(ctx, *assistantMsg)
	case provider.EventContentDelta:
		assistantMsg.FinishThinking()
		assistantMsg.AppendContent(event.Content)
		return a.messages.Update(ctx, *assistantMsg)
	case provider.EventToolUseStart:
		assistantMsg.FinishThinking()
		slog.Info("Tool call started", "toolCall", event.ToolCall)
		assistantMsg.AddToolCall(*event.ToolCall)
		return a.messages.Update(ctx, *assistantMsg)
	case provider.EventToolUseDelta:
		assistantMsg.AppendToolCallInput(event.ToolCall.ID, event.ToolCall.Input)
		return a.messages.Update(ctx, *assistantMsg)
	case provider.EventToolUseStop:
		slog.Info("Finished tool call", "toolCall", event.ToolCall)
		assistantMsg.FinishToolCall(event.ToolCall.ID)
		return a.messages.Update(ctx, *assistantMsg)
	case provider.EventError:
		return event.Error
	case provider.EventComplete:
		assistantMsg.FinishThinking()
		assistantMsg.SetToolCalls(event.Response.ToolCalls)
		assistantMsg.AddFinish(event.Response.FinishReason, "", "")
		if err := a.messages.Update(ctx, *assistantMsg); err != nil {
			return fmt.Errorf("failed to update message: %w", err)
		}
		return a.trackUsage(ctx, sessionID, a.Model(), event.Response.Usage)
	}

	return nil
}

func (a *agent) trackUsage(ctx context.Context, sessionID string, model catwalk.Model, usage provider.TokenUsage) error {
	sess, err := a.sessions.Get(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("failed to get session: %w", err)
	}

	cost := model.CostPer1MInCached/1e6*float64(usage.CacheCreationTokens) +
		model.CostPer1MOutCached/1e6*float64(usage.CacheReadTokens) +
		model.CostPer1MIn/1e6*float64(usage.InputTokens) +
		model.CostPer1MOut/1e6*float64(usage.OutputTokens)

	a.eventTokensUsed(sessionID, usage, cost)

	sess.Cost += cost
	sess.CompletionTokens = usage.OutputTokens + usage.CacheReadTokens
	sess.PromptTokens = usage.InputTokens + usage.CacheCreationTokens

	_, err = a.sessions.Save(ctx, sess)
	if err != nil {
		return fmt.Errorf("failed to save session: %w", err)
	}
	return nil
}

func (a *agent) Summarize(ctx context.Context, sessionID string) error {
	if a.summarizeProvider == nil {
		return fmt.Errorf("summarize provider not available")
	}

	// Check if session is busy
	if a.IsSessionBusy(sessionID) {
		return ErrSessionBusy
	}

	// Create a new context with cancellation
	summarizeCtx, cancel := context.WithCancel(ctx)

	// Store the cancel function in activeRequests to allow cancellation
	a.activeRequests.Set(sessionID+"-summarize", cancel)

	go func() {
		defer a.activeRequests.Del(sessionID + "-summarize")
		defer cancel()
		event := AgentEvent{
			Type:     AgentEventTypeSummarize,
			Progress: "Starting summarization...",
		}

		a.Publish(pubsub.CreatedEvent, event)
		// Get all messages from the session
		msgs, err := a.messages.List(summarizeCtx, sessionID)
		if err != nil {
			event = AgentEvent{
				Type:  AgentEventTypeError,
				Error: fmt.Errorf("failed to list messages: %w", err),
				Done:  true,
			}
			a.Publish(pubsub.CreatedEvent, event)
			return
		}
		summarizeCtx = context.WithValue(summarizeCtx, tools.SessionIDContextKey, sessionID)

		if len(msgs) == 0 {
			event = AgentEvent{
				Type:  AgentEventTypeError,
				Error: fmt.Errorf("no messages to summarize"),
				Done:  true,
			}
			a.Publish(pubsub.CreatedEvent, event)
			return
		}

		event = AgentEvent{
			Type:     AgentEventTypeSummarize,
			Progress: "Analyzing conversation...",
		}
		a.Publish(pubsub.CreatedEvent, event)

		// Add a system message to guide the summarization
		summarizePrompt := "Provide a detailed but concise summary of our conversation above. Focus on information that would be helpful for continuing the conversation, including what we did, what we're doing, which files we're working on, and what we're going to do next."

		// Create a new message with the summarize prompt
		promptMsg := message.Message{
			Role:  message.User,
			Parts: []message.ContentPart{message.TextContent{Text: summarizePrompt}},
		}

		// Append the prompt to the messages
		msgsWithPrompt := append(msgs, promptMsg)

		event = AgentEvent{
			Type:     AgentEventTypeSummarize,
			Progress: "Generating summary...",
		}

		a.Publish(pubsub.CreatedEvent, event)

		// Send the messages to the summarize provider
		response := a.summarizeProvider.StreamResponse(
			summarizeCtx,
			msgsWithPrompt,
			nil,
		)
		var finalResponse *provider.ProviderResponse
		for r := range response {
			if r.Error != nil {
				event = AgentEvent{
					Type:  AgentEventTypeError,
					Error: fmt.Errorf("failed to summarize: %w", r.Error),
					Done:  true,
				}
				a.Publish(pubsub.CreatedEvent, event)
				return
			}
			finalResponse = r.Response
		}

		summary := strings.TrimSpace(finalResponse.Content)
		if summary == "" {
			event = AgentEvent{
				Type:  AgentEventTypeError,
				Error: fmt.Errorf("empty summary returned"),
				Done:  true,
			}
			a.Publish(pubsub.CreatedEvent, event)
			return
		}
		shell := shell.GetPersistentShell(config.Get().WorkingDir())
		summary += "\n\n**Current working directory of the persistent shell**\n\n" + shell.GetWorkingDir()
		event = AgentEvent{
			Type:     AgentEventTypeSummarize,
			Progress: "Creating new session...",
		}

		a.Publish(pubsub.CreatedEvent, event)
		oldSession, err := a.sessions.Get(summarizeCtx, sessionID)
		if err != nil {
			event = AgentEvent{
				Type:  AgentEventTypeError,
				Error: fmt.Errorf("failed to get session: %w", err),
				Done:  true,
			}

			a.Publish(pubsub.CreatedEvent, event)
			return
		}
		// Create a message in the new session with the summary
		msg, err := a.messages.Create(summarizeCtx, oldSession.ID, message.CreateMessageParams{
			Role: message.Assistant,
			Parts: []message.ContentPart{
				message.TextContent{Text: summary},
				message.Finish{
					Reason: message.FinishReasonEndTurn,
					Time:   time.Now().Unix(),
				},
			},
			Model:    a.summarizeProvider.Model().ID,
			Provider: a.summarizeProviderID,
		})
		if err != nil {
			event = AgentEvent{
				Type:  AgentEventTypeError,
				Error: fmt.Errorf("failed to create summary message: %w", err),
				Done:  true,
			}

			a.Publish(pubsub.CreatedEvent, event)
			return
		}
		oldSession.SummaryMessageID = msg.ID
		oldSession.CompletionTokens = finalResponse.Usage.OutputTokens
		oldSession.PromptTokens = 0
		model := a.summarizeProvider.Model()
		usage := finalResponse.Usage
		cost := model.CostPer1MInCached/1e6*float64(usage.CacheCreationTokens) +
			model.CostPer1MOutCached/1e6*float64(usage.CacheReadTokens) +
			model.CostPer1MIn/1e6*float64(usage.InputTokens) +
			model.CostPer1MOut/1e6*float64(usage.OutputTokens)
		oldSession.Cost += cost
		_, err = a.sessions.Save(summarizeCtx, oldSession)
		if err != nil {
			event = AgentEvent{
				Type:  AgentEventTypeError,
				Error: fmt.Errorf("failed to save session: %w", err),
				Done:  true,
			}
			a.Publish(pubsub.CreatedEvent, event)
		}

		event = AgentEvent{
			Type:      AgentEventTypeSummarize,
			SessionID: oldSession.ID,
			Progress:  "Summary complete",
			Done:      true,
		}
		a.Publish(pubsub.CreatedEvent, event)
		// Send final success event with the new session ID
	}()

	return nil
}

func (a *agent) ClearQueue(sessionID string) {
	if a.QueuedPrompts(sessionID) > 0 {
		slog.Info("Clearing queued prompts", "session_id", sessionID)
		a.promptQueue.Del(sessionID)
	}
}

func (a *agent) CancelAll() {
	if !a.IsBusy() {
		return
	}
	for key := range a.activeRequests.Seq2() {
		a.Cancel(key) // key is sessionID
	}

	for _, cleanup := range a.cleanupFuncs {
		if cleanup != nil {
			cleanup()
		}
	}

	timeout := time.After(5 * time.Second)
	for a.IsBusy() {
		select {
		case <-timeout:
			return
		default:
			time.Sleep(200 * time.Millisecond)
		}
	}
}

func (a *agent) UpdateModel() error {
	cfg := config.Get()

	// Get current provider configuration
	currentProviderCfg := cfg.GetProviderForModel(a.agentCfg.Model)
	if currentProviderCfg == nil || currentProviderCfg.ID == "" {
		return fmt.Errorf("provider for agent %s not found in config", a.agentCfg.Name)
	}

	// Check if provider has changed
	if string(currentProviderCfg.ID) != a.providerID {
		// Provider changed, need to recreate the main provider
		model := cfg.GetModelByType(a.agentCfg.Model)
		if model.ID == "" {
			return fmt.Errorf("model not found for agent %s", a.agentCfg.Name)
		}

		promptID := agentPromptMap[a.agentCfg.ID]
		if promptID == "" {
			promptID = prompt.PromptDefault
		}

		opts := []provider.ProviderClientOption{
			provider.WithModel(a.agentCfg.Model),
			provider.WithSystemMessage(prompt.GetPrompt(promptID, currentProviderCfg.ID, cfg.Options.ContextPaths...)),
		}

		newProvider, err := provider.NewProvider(*currentProviderCfg, opts...)
		if err != nil {
			return fmt.Errorf("failed to create new provider: %w", err)
		}

		// Update the provider and provider ID
		a.provider = newProvider
		a.providerID = string(currentProviderCfg.ID)
	}

	// Check if providers have changed for title (small) and summarize (large)
	smallModelCfg := cfg.Models[config.SelectedModelTypeSmall]
	var smallModelProviderCfg config.ProviderConfig
	for p := range cfg.Providers.Seq() {
		if p.ID == smallModelCfg.Provider {
			smallModelProviderCfg = p
			break
		}
	}
	if smallModelProviderCfg.ID == "" {
		return fmt.Errorf("provider %s not found in config", smallModelCfg.Provider)
	}

	largeModelCfg := cfg.Models[config.SelectedModelTypeLarge]
	var largeModelProviderCfg config.ProviderConfig
	for p := range cfg.Providers.Seq() {
		if p.ID == largeModelCfg.Provider {
			largeModelProviderCfg = p
			break
		}
	}
	if largeModelProviderCfg.ID == "" {
		return fmt.Errorf("provider %s not found in config", largeModelCfg.Provider)
	}

	var maxTitleTokens int64 = 40

	// if the max output is too low for the gemini provider it won't return anything
	if smallModelCfg.Provider == "gemini" {
		maxTitleTokens = 1000
	}
	// Recreate title provider
	titleOpts := []provider.ProviderClientOption{
		provider.WithModel(config.SelectedModelTypeSmall),
		provider.WithSystemMessage(prompt.GetPrompt(prompt.PromptTitle, smallModelProviderCfg.ID)),
		provider.WithMaxTokens(maxTitleTokens),
	}
	newTitleProvider, err := provider.NewProvider(smallModelProviderCfg, titleOpts...)
	if err != nil {
		return fmt.Errorf("failed to create new title provider: %w", err)
	}
	a.titleProvider = newTitleProvider

	// Recreate summarize provider if provider changed (now large model)
	if string(largeModelProviderCfg.ID) != a.summarizeProviderID {
		largeModel := cfg.GetModelByType(config.SelectedModelTypeLarge)
		if largeModel == nil {
			return fmt.Errorf("model %s not found in provider %s", largeModelCfg.Model, largeModelProviderCfg.ID)
		}
		summarizeOpts := []provider.ProviderClientOption{
			provider.WithModel(config.SelectedModelTypeLarge),
			provider.WithSystemMessage(prompt.GetPrompt(prompt.PromptSummarizer, largeModelProviderCfg.ID)),
		}
		newSummarizeProvider, err := provider.NewProvider(largeModelProviderCfg, summarizeOpts...)
		if err != nil {
			return fmt.Errorf("failed to create new summarize provider: %w", err)
		}
		a.summarizeProvider = newSummarizeProvider
		a.summarizeProviderID = string(largeModelProviderCfg.ID)
	}

	return nil
}

func (a *agent) setupEvents(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)

	go func() {
		subCh := SubscribeMCPEvents(ctx)

		for {
			select {
			case event, ok := <-subCh:
				if !ok {
					slog.Debug("MCPEvents subscription channel closed")
					return
				}
				switch event.Payload.Type {
				case MCPEventToolsListChanged:
					name := event.Payload.Name
					c, ok := mcpClients.Get(name)
					if !ok {
						slog.Warn("MCP client not found for tools update", "name", name)
						continue
					}
					cfg := config.Get()
					tools, err := getTools(ctx, name, a.permissions, c, cfg.WorkingDir())
					if err != nil {
						slog.Error("error listing tools", "error", err)
						updateMCPState(name, MCPStateError, err, nil, 0)
						_ = c.Close()
						continue
					}
					updateMcpTools(name, tools)
					a.mcpTools.Reset(maps.Collect(mcpTools.Seq2()))
					updateMCPState(name, MCPStateConnected, nil, c, a.mcpTools.Len())
				default:
					continue
				}
			case <-ctx.Done():
				slog.Debug("MCPEvents subscription cancelled")
				return
			}
		}
	}()

	a.cleanupFuncs = append(a.cleanupFuncs, cancel)
}
