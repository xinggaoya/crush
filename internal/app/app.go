package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea/v2"
	"github.com/xinggaoya/crush/internal/config"
	"github.com/xinggaoya/crush/internal/csync"
	"github.com/xinggaoya/crush/internal/db"
	"github.com/xinggaoya/crush/internal/format"
	"github.com/xinggaoya/crush/internal/history"
	"github.com/xinggaoya/crush/internal/llm/agent"
	"github.com/xinggaoya/crush/internal/log"
	"github.com/xinggaoya/crush/internal/lsp"
	"github.com/xinggaoya/crush/internal/message"
	"github.com/xinggaoya/crush/internal/permission"
	"github.com/xinggaoya/crush/internal/pubsub"
	"github.com/xinggaoya/crush/internal/session"
	"github.com/charmbracelet/x/ansi"
)

type App struct {
	Sessions    session.Service
	Messages    message.Service
	History     history.Service
	Permissions permission.Service

	CoderAgent agent.Service

	LSPClients *csync.Map[string, *lsp.Client]
	lspPool    *LSPConnectionPool

	config *config.Config

	serviceEventsWG *sync.WaitGroup
	eventsCtx       context.Context
	events          chan tea.Msg
	tuiWG           *sync.WaitGroup

	// global context and cleanup functions
	globalCtx    context.Context
	cleanupFuncs []func() error
}

// New initializes a new applcation instance.
func New(ctx context.Context, conn *sql.DB, cfg *config.Config) (*App, error) {
	q := db.New(conn)
	sessions := session.NewService(q)
	messages := message.NewService(q)
	files := history.NewService(q, conn)
	skipPermissionsRequests := cfg.Permissions != nil && cfg.Permissions.SkipRequests
	allowedTools := []string{}
	if cfg.Permissions != nil && cfg.Permissions.AllowedTools != nil {
		allowedTools = cfg.Permissions.AllowedTools
	}

	app := &App{
		Sessions:    sessions,
		Messages:    messages,
		History:     files,
		Permissions: permission.NewPermissionService(cfg.WorkingDir(), skipPermissionsRequests, allowedTools),
		LSPClients:  csync.NewMap[string, *lsp.Client](),

		globalCtx: ctx,

		config: cfg,

		events:          make(chan tea.Msg, 256), // Increased buffer size for better performance
		serviceEventsWG: &sync.WaitGroup{},
		tuiWG:           &sync.WaitGroup{},
	}

	app.setupEvents()

	// Initialize LSP clients in the background.
	app.initLSPClients(ctx)

	// cleanup database upon app shutdown
	app.cleanupFuncs = append(app.cleanupFuncs, conn.Close)

	// TODO: remove the concept of agent config, most likely.
	if cfg.IsConfigured() {
		if err := app.InitCoderAgent(); err != nil {
			return nil, fmt.Errorf("failed to initialize coder agent: %w", err)
		}
	} else {
		slog.Warn("No agent configuration found")
	}
	return app, nil
}

// Config returns the application configuration.
func (app *App) Config() *config.Config {
	return app.config
}

// RunNonInteractive handles the execution flow when a prompt is provided via
// CLI flag.
func (app *App) RunNonInteractive(ctx context.Context, prompt string, quiet bool) error {
	slog.Info("Running in non-interactive mode")

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var spinner *format.Spinner
	if !quiet {
		spinner = format.NewSpinner(ctx, cancel, "Generating")
		spinner.Start()
	}

	// Helper function to stop spinner once.
	stopSpinner := func() {
		if !quiet && spinner != nil {
			spinner.Stop()
			spinner = nil
		}
	}
	defer stopSpinner()

	const maxPromptLengthForTitle = 100
	titlePrefix := "Non-interactive: "
	var titleSuffix string

	if len(prompt) > maxPromptLengthForTitle {
		titleSuffix = prompt[:maxPromptLengthForTitle] + "..."
	} else {
		titleSuffix = prompt
	}
	title := titlePrefix + titleSuffix

	sess, err := app.Sessions.Create(ctx, title)
	if err != nil {
		return fmt.Errorf("failed to create session for non-interactive mode: %w", err)
	}
	slog.Info("Created session for non-interactive run", "session_id", sess.ID)

	// Automatically approve all permission requests for this non-interactive session
	app.Permissions.AutoApproveSession(sess.ID)

	done, err := app.CoderAgent.Run(ctx, sess.ID, prompt)
	if err != nil {
		return fmt.Errorf("failed to start agent processing stream: %w", err)
	}

	messageEvents := app.Messages.Subscribe(ctx)
	messageReadBytes := make(map[string]int)

	defer fmt.Printf(ansi.ResetProgressBar)
	for {
		// HACK: add it again on every iteration so it doesn't get hidden by
		// the terminal due to inactivity.
		fmt.Printf(ansi.SetIndeterminateProgressBar)
		select {
		case result := <-done:
			stopSpinner()

			if result.Error != nil {
				if errors.Is(result.Error, context.Canceled) || errors.Is(result.Error, agent.ErrRequestCancelled) {
					slog.Info("Non-interactive: agent processing cancelled", "session_id", sess.ID)
					return nil
				}
				return fmt.Errorf("agent processing failed: %w", result.Error)
			}

			msgContent := result.Message.Content().String()
			readBts := messageReadBytes[result.Message.ID]

			if len(msgContent) < readBts {
				slog.Error("Non-interactive: message content is shorter than read bytes", "message_length", len(msgContent), "read_bytes", readBts)
				return fmt.Errorf("message content is shorter than read bytes: %d < %d", len(msgContent), readBts)
			}
			fmt.Println(msgContent[readBts:])
			messageReadBytes[result.Message.ID] = len(msgContent)

			slog.Info("Non-interactive: run completed", "session_id", sess.ID)
			return nil

		case event := <-messageEvents:
			msg := event.Payload
			if msg.SessionID == sess.ID && msg.Role == message.Assistant && len(msg.Parts) > 0 {
				stopSpinner()

				content := msg.Content().String()
				readBytes := messageReadBytes[msg.ID]

				if len(content) < readBytes {
					slog.Error("Non-interactive: message content is shorter than read bytes", "message_length", len(content), "read_bytes", readBytes)
					return fmt.Errorf("message content is shorter than read bytes: %d < %d", len(content), readBytes)
				}

				part := content[readBytes:]
				fmt.Print(part)
				messageReadBytes[msg.ID] = len(content)
			}

		case <-ctx.Done():
			stopSpinner()
			return ctx.Err()
		}
	}
}

func (app *App) UpdateAgentModel() error {
	return app.CoderAgent.UpdateModel()
}

func (app *App) setupEvents() {
	ctx, cancel := context.WithCancel(app.globalCtx)
	app.eventsCtx = ctx
	setupSubscriber(ctx, app.serviceEventsWG, "sessions", app.Sessions.Subscribe, app.events)
	setupSubscriber(ctx, app.serviceEventsWG, "messages", app.Messages.Subscribe, app.events)
	setupSubscriber(ctx, app.serviceEventsWG, "permissions", app.Permissions.Subscribe, app.events)
	setupSubscriber(ctx, app.serviceEventsWG, "permissions-notifications", app.Permissions.SubscribeNotifications, app.events)
	setupSubscriber(ctx, app.serviceEventsWG, "history", app.History.Subscribe, app.events)
	setupSubscriber(ctx, app.serviceEventsWG, "mcp", agent.SubscribeMCPEvents, app.events)
	setupSubscriber(ctx, app.serviceEventsWG, "lsp", SubscribeLSPEvents, app.events)
	cleanupFunc := func() error {
		cancel()
		app.serviceEventsWG.Wait()
		return nil
	}
	app.cleanupFuncs = append(app.cleanupFuncs, cleanupFunc)
}

func setupSubscriber[T any](
	ctx context.Context,
	wg *sync.WaitGroup,
	name string,
	subscriber func(context.Context) <-chan pubsub.Event[T],
	outputCh chan<- tea.Msg,
) {
	wg.Go(func() {
		subCh := subscriber(ctx)
		for {
			select {
			case event, ok := <-subCh:
				if !ok {
					slog.Debug("subscription channel closed", "name", name)
					return
				}
				var msg tea.Msg = event

				// Implement better backpressure control with retry mechanism
				const maxRetries = 3
				retryDelay := 100 * time.Millisecond
				messageSent := false

				for attempt := 0; attempt < maxRetries && !messageSent; attempt++ {
					select {
					case outputCh <- msg:
						// Message sent successfully
						messageSent = true
					case <-time.After(retryDelay):
						// Exponential backoff
						retryDelay *= 2
						if attempt == maxRetries-1 {
							slog.Warn("message dropped after retries", "name", name, "attempts", maxRetries)
							messageSent = true // Break out to avoid infinite loop
						} else {
							slog.Debug("retrying message send", "name", name, "attempt", attempt+1)
						}
					case <-ctx.Done():
						slog.Debug("subscription cancelled", "name", name)
						return
					}
				}
			case <-ctx.Done():
				slog.Debug("subscription cancelled", "name", name)
				return
			}
		}
	})
}

func (app *App) InitCoderAgent() error {
	coderAgentCfg := app.config.Agents["coder"]
	if coderAgentCfg.ID == "" {
		return fmt.Errorf("coder agent configuration is missing")
	}
	var err error
	app.CoderAgent, err = agent.NewAgent(
		app.globalCtx,
		coderAgentCfg,
		app.Permissions,
		app.Sessions,
		app.Messages,
		app.History,
		app.LSPClients,
	)
	if err != nil {
		slog.Error("Failed to create coder agent", "err", err)
		return err
	}

	// Add MCP client cleanup to shutdown process
	app.cleanupFuncs = append(app.cleanupFuncs, agent.CloseMCPClients)

	setupSubscriber(app.eventsCtx, app.serviceEventsWG, "coderAgent", app.CoderAgent.Subscribe, app.events)
	return nil
}

// Subscribe sends events to the TUI as tea.Msgs.
func (app *App) Subscribe(program *tea.Program) {
	defer log.RecoverPanic("app.Subscribe", func() {
		slog.Info("TUI subscription panic: attempting graceful shutdown")
		program.Quit()
	})

	app.tuiWG.Add(1)
	tuiCtx, tuiCancel := context.WithCancel(app.globalCtx)
	app.cleanupFuncs = append(app.cleanupFuncs, func() error {
		slog.Debug("Cancelling TUI message handler")
		tuiCancel()
		app.tuiWG.Wait()
		return nil
	})
	defer app.tuiWG.Done()

	for {
		select {
		case <-tuiCtx.Done():
			slog.Debug("TUI message handler shutting down")
			return
		case msg, ok := <-app.events:
			if !ok {
				slog.Debug("TUI message channel closed")
				return
			}
			program.Send(msg)
		}
	}
}

// Shutdown performs a graceful shutdown of the application.
func (app *App) Shutdown() {
	if app.CoderAgent != nil {
		app.CoderAgent.CancelAll()
	}

	// Shutdown LSP connection pool
	if app.lspPool != nil {
		app.lspPool.Shutdown(app.globalCtx)
	}

	// Shutdown any remaining LSP clients that aren't in the pool
	for name, client := range app.LSPClients.Seq2() {
		shutdownCtx, cancel := context.WithTimeout(app.globalCtx, 5*time.Second)
		if err := client.Close(shutdownCtx); err != nil {
			slog.Error("Failed to shutdown LSP client", "name", name, "error", err)
		}
		cancel()
	}

	// Call call cleanup functions.
	for _, cleanup := range app.cleanupFuncs {
		if cleanup != nil {
			if err := cleanup(); err != nil {
				slog.Error("Failed to cleanup app properly on shutdown", "error", err)
			}
		}
	}
}
