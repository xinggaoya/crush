package app

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/xinggaoya/crush/internal/config"
	"github.com/xinggaoya/crush/internal/lsp"
)

// LSPConnectionPool manages LSP client connections with pooling and health checks
type LSPConnectionPool struct {
	clients map[string]*lsp.Client
	mu      sync.RWMutex
	health  map[string]time.Time
}

// NewLSPConnectionPool creates a new LSP connection pool
func NewLSPConnectionPool() *LSPConnectionPool {
	return &LSPConnectionPool{
		clients: make(map[string]*lsp.Client),
		health:  make(map[string]time.Time),
	}
}

// GetClient returns a client from the pool, creating it if necessary
func (pool *LSPConnectionPool) GetClient(ctx context.Context, name string, config config.LSPConfig, cfg *config.Config) (*lsp.Client, error) {
	pool.mu.RLock()
	client, exists := pool.clients[name]
	lastHealth, healthy := pool.health[name]
	pool.mu.RUnlock()

	// Check if client exists and is healthy (within last 30 seconds)
	if exists && healthy && time.Since(lastHealth) < 30*time.Second {
		return client, nil
	}

	pool.mu.Lock()
	defer pool.mu.Unlock()

	// Double-check after acquiring write lock
	if client, exists := pool.clients[name]; exists {
		if lastHealth, healthy := pool.health[name]; healthy && time.Since(lastHealth) < 30*time.Second {
			return client, nil
		}
	}

	// Create new client
	newClient, err := lsp.New(ctx, name, config, cfg.Resolver())
	if err != nil {
		return nil, err
	}

	pool.clients[name] = newClient
	pool.health[name] = time.Now()
	return newClient, nil
}

// UpdateHealth updates the health status of a client
func (pool *LSPConnectionPool) UpdateHealth(name string) {
	pool.mu.Lock()
	defer pool.mu.Unlock()
	pool.health[name] = time.Now()
}

// RemoveClient removes a client from the pool
func (pool *LSPConnectionPool) RemoveClient(name string) {
	pool.mu.Lock()
	defer pool.mu.Unlock()
	if client, exists := pool.clients[name]; exists {
		client.Close(context.Background())
		delete(pool.clients, name)
		delete(pool.health, name)
	}
}

// Shutdown shuts down all clients in the pool
func (pool *LSPConnectionPool) Shutdown(ctx context.Context) {
	pool.mu.Lock()
	defer pool.mu.Unlock()

	for name, client := range pool.clients {
		if err := client.Close(ctx); err != nil {
			slog.Error("Failed to close LSP client", "name", name, "error", err)
		}
	}
	pool.clients = make(map[string]*lsp.Client)
	pool.health = make(map[string]time.Time)
}

// initLSPClients initializes LSP clients with connection pooling.
func (app *App) initLSPClients(ctx context.Context) {
	app.lspPool = NewLSPConnectionPool()

	for name, clientConfig := range app.config.LSP {
		if clientConfig.Disabled {
			slog.Info("Skipping disabled LSP client", "name", name)
			continue
		}
		go app.createAndStartLSPClient(ctx, name, clientConfig)
	}
	slog.Info("LSP clients initialization started in background with connection pooling")
}

// createAndStartLSPClient creates a new LSP client, initializes it, and starts its workspace watcher
func (app *App) createAndStartLSPClient(ctx context.Context, name string, config config.LSPConfig) {
	slog.Info("Creating LSP client", "name", name, "command", config.Command, "fileTypes", config.FileTypes, "args", config.Args)

	// Check if any root markers exist in the working directory (config now has defaults)
	if !lsp.HasRootMarkers(app.config.WorkingDir(), config.RootMarkers) {
		slog.Info("Skipping LSP client - no root markers found", "name", name, "rootMarkers", config.RootMarkers)
		updateLSPState(name, lsp.StateDisabled, nil, nil, 0)
		return
	}

	// Update state to starting
	updateLSPState(name, lsp.StateStarting, nil, nil, 0)

	// Create LSP client.
	lspClient, err := lsp.New(ctx, name, config, app.config.Resolver())
	if err != nil {
		slog.Error("Failed to create LSP client for", name, err)
		updateLSPState(name, lsp.StateError, err, nil, 0)
		return
	}

	// Set diagnostics callback
	lspClient.SetDiagnosticsCallback(updateLSPDiagnostics)

	// Increase initialization timeout as some servers take more time to start.
	initCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	// Initialize LSP client.
	_, err = lspClient.Initialize(initCtx, app.config.WorkingDir())
	if err != nil {
		slog.Error("Initialize failed", "name", name, "error", err)
		updateLSPState(name, lsp.StateError, err, lspClient, 0)
		lspClient.Close(ctx)
		return
	}

	// Wait for the server to be ready.
	if err := lspClient.WaitForServerReady(initCtx); err != nil {
		slog.Error("Server failed to become ready", "name", name, "error", err)
		// Server never reached a ready state, but let's continue anyway, as
		// some functionality might still work.
		lspClient.SetServerState(lsp.StateError)
		updateLSPState(name, lsp.StateError, err, lspClient, 0)
	} else {
		// Server reached a ready state scuccessfully.
		slog.Info("LSP server is ready", "name", name)
		lspClient.SetServerState(lsp.StateReady)
		updateLSPState(name, lsp.StateReady, nil, lspClient, 0)
	}

	slog.Info("LSP client initialized", "name", name)

	// Add to map with mutex protection before starting goroutine
	app.LSPClients.Set(name, lspClient)
}
