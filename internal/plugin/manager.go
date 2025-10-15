package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Plugin represents a crush plugin
type Plugin interface {
	ID() string
	Name() string
	Version() string
	Description() string
	Initialize(ctx context.Context, config map[string]interface{}) error
	Execute(ctx context.Context, input map[string]interface{}) (map[string]interface{}, error)
	Cleanup() error
	HealthCheck() error
}

// PluginManager manages plugin lifecycle
type PluginManager struct {
	plugins map[string]Plugin
	configs map[string]PluginConfig
	mu      sync.RWMutex
	hooks   map[string][]PluginHook
	dataDir string
}

// PluginConfig contains plugin configuration
type PluginConfig struct {
	ID          string                 `json:"id"`
	Name        string                 `json:"name"`
	Version     string                 `json:"version"`
	Enabled     bool                   `json:"enabled"`
	Config      map[string]interface{} `json:"config"`
	Permissions []string               `json:"permissions"`
	AutoStart   bool                   `json:"auto_start"`
	HealthCheck HealthCheckConfig      `json:"health_check"`
}

// HealthCheckConfig configures plugin health checks
type HealthCheckConfig struct {
	Enabled  bool          `json:"enabled"`
	Interval time.Duration `json:"interval"`
	Timeout  time.Duration `json:"timeout"`
	Endpoint string        `json:"endpoint"`
}

// PluginHook represents a plugin hook
type PluginHook struct {
	PluginID string
	Event    string
	Handler  func(context.Context, map[string]interface{}) error
}

// NewPluginManager creates a new plugin manager
func NewPluginManager(dataDir string) *PluginManager {
	return &PluginManager{
		plugins: make(map[string]Plugin),
		configs: make(map[string]PluginConfig),
		hooks:   make(map[string][]PluginHook),
		dataDir: dataDir,
	}
}

// LoadPlugins loads plugins from the plugin directory
func (pm *PluginManager) LoadPlugins(ctx context.Context) error {
	pluginDir := filepath.Join(pm.dataDir, "plugins")
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		return fmt.Errorf("failed to create plugin directory: %w", err)
	}

	entries, err := os.ReadDir(pluginDir)
	if err != nil {
		return fmt.Errorf("failed to read plugin directory: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			if err := pm.loadPlugin(ctx, filepath.Join(pluginDir, entry.Name())); err != nil {
				fmt.Printf("Failed to load plugin %s: %v\n", entry.Name(), err)
			}
		}
	}

	return nil
}

// loadPlugin loads a single plugin
func (pm *PluginManager) loadPlugin(ctx context.Context, pluginPath string) error {
	configFile := filepath.Join(pluginPath, "plugin.json")
	configData, err := os.ReadFile(configFile)
	if err != nil {
		return fmt.Errorf("failed to read plugin config: %w", err)
	}

	var config PluginConfig
	if err := json.Unmarshal(configData, &config); err != nil {
		return fmt.Errorf("failed to parse plugin config: %w", err)
	}

	if !config.Enabled {
		return nil
	}

	// Load plugin binary or library
	plugin, err := pm.loadPluginBinary(pluginPath, &config)
	if err != nil {
		return fmt.Errorf("failed to load plugin binary: %w", err)
	}

	// Initialize plugin
	if err := plugin.Initialize(ctx, config.Config); err != nil {
		return fmt.Errorf("failed to initialize plugin: %w", err)
	}

	pm.mu.Lock()
	pm.plugins[config.ID] = plugin
	pm.configs[config.ID] = config
	pm.mu.Unlock()

	// Start health check if configured
	if config.HealthCheck.Enabled {
		go pm.startHealthCheck(ctx, config.ID, config.HealthCheck)
	}

	return nil
}

// loadPluginBinary loads a plugin binary (placeholder implementation)
func (pm *PluginManager) loadPluginBinary(pluginPath string, config *PluginConfig) (Plugin, error) {
	// This is a placeholder - in a real implementation, this would:
	// 1. Load a Go plugin using the plugin package
	// 2. Or load a subprocess plugin
	// 3. Or load a web-based plugin

	// For now, return a mock plugin
	return &MockPlugin{
		id:          config.ID,
		name:        config.Name,
		version:     config.Version,
		description: config.Name + " plugin",
	}, nil
}

// RegisterPlugin registers a plugin
func (pm *PluginManager) RegisterPlugin(plugin Plugin, config PluginConfig) error {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	pm.plugins[config.ID] = plugin
	pm.configs[config.ID] = config
	return nil
}

// ExecutePlugin executes a plugin
func (pm *PluginManager) ExecutePlugin(ctx context.Context, pluginID string, input map[string]interface{}) (map[string]interface{}, error) {
	pm.mu.RLock()
	plugin, exists := pm.plugins[pluginID]
	config := pm.configs[pluginID]
	pm.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("plugin not found: %s", pluginID)
	}

	if !config.Enabled {
		return nil, fmt.Errorf("plugin is disabled: %s", pluginID)
	}

	// Check permissions
	if !pm.checkPermissions(pluginID, input) {
		return nil, fmt.Errorf("insufficient permissions for plugin: %s", pluginID)
	}

	return plugin.Execute(ctx, input)
}

// checkPermissions checks if the plugin has required permissions
func (pm *PluginManager) checkPermissions(pluginID string, input map[string]interface{}) bool {
	pm.mu.RLock()
	config := pm.configs[pluginID]
	pm.mu.RUnlock()

	// Simple permission check - in real implementation this would be more sophisticated
	if len(config.Permissions) == 0 {
		return true
	}

	// Check if input requires any special permissions
	if action, ok := input["action"].(string); ok {
		for _, perm := range config.Permissions {
			if perm == action {
				return true
			}
		}
	}

	return false
}

// RegisterHook registers a plugin hook
func (pm *PluginManager) RegisterHook(pluginID, event string, handler func(context.Context, map[string]interface{}) error) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	hook := PluginHook{
		PluginID: pluginID,
		Event:    event,
		Handler:  handler,
	}

	pm.hooks[event] = append(pm.hooks[event], hook)
}

// TriggerHooks triggers hooks for an event
func (pm *PluginManager) TriggerHooks(ctx context.Context, event string, data map[string]interface{}) error {
	pm.mu.RLock()
	hooks := pm.hooks[event]
	pm.mu.RUnlock()

	var errors []error
	for _, hook := range hooks {
		if err := hook.Handler(ctx, data); err != nil {
			errors = append(errors, err)
		}
	}

	if len(errors) > 0 {
		return fmt.Errorf("hook errors: %v", errors)
	}

	return nil
}

// startHealthCheck starts periodic health checks for a plugin
func (pm *PluginManager) startHealthCheck(ctx context.Context, pluginID string, config HealthCheckConfig) {
	ticker := time.NewTicker(config.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			pm.performHealthCheck(ctx, pluginID, config)
		}
	}
}

// performHealthCheck performs a health check for a plugin
func (pm *PluginManager) performHealthCheck(ctx context.Context, pluginID string, config HealthCheckConfig) {
	pm.mu.RLock()
	plugin, exists := pm.plugins[pluginID]
	pm.mu.RUnlock()

	if !exists {
		return
	}

	ctx, cancel := context.WithTimeout(ctx, config.Timeout)
	defer cancel()

	if err := plugin.HealthCheck(); err != nil {
		fmt.Printf("Plugin %s health check failed: %v\n", pluginID, err)
		// In a real implementation, this might trigger auto-restart or alerting
	}
}

// GetPlugin returns a plugin by ID
func (pm *PluginManager) GetPlugin(pluginID string) (Plugin, error) {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	plugin, exists := pm.plugins[pluginID]
	if !exists {
		return nil, fmt.Errorf("plugin not found: %s", pluginID)
	}

	return plugin, nil
}

// ListPlugins returns all plugins
func (pm *PluginManager) ListPlugins() map[string]PluginConfig {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	result := make(map[string]PluginConfig)
	for id, config := range pm.configs {
		result[id] = config
	}

	return result
}

// UnloadPlugin unloads a plugin
func (pm *PluginManager) UnloadPlugin(pluginID string) error {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	plugin, exists := pm.plugins[pluginID]
	if !exists {
		return fmt.Errorf("plugin not found: %s", pluginID)
	}

	if err := plugin.Cleanup(); err != nil {
		return fmt.Errorf("failed to cleanup plugin: %w", err)
	}

	delete(pm.plugins, pluginID)
	delete(pm.configs, pluginID)

	// Remove hooks
	for event, hooks := range pm.hooks {
		var filtered []PluginHook
		for _, hook := range hooks {
			if hook.PluginID != pluginID {
				filtered = append(filtered, hook)
			}
		}
		pm.hooks[event] = filtered
	}

	return nil
}

// Shutdown shuts down all plugins
func (pm *PluginManager) Shutdown(ctx context.Context) error {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	var errors []error
	for id, plugin := range pm.plugins {
		if err := plugin.Cleanup(); err != nil {
			errors = append(errors, fmt.Errorf("failed to cleanup plugin %s: %w", id, err))
		}
	}

	if len(errors) > 0 {
		return fmt.Errorf("shutdown errors: %v", errors)
	}

	return nil
}

// MockPlugin is a mock implementation for testing
type MockPlugin struct {
	id, name, version, description string
	initialized                    bool
}

func (mp *MockPlugin) ID() string          { return mp.id }
func (mp *MockPlugin) Name() string        { return mp.name }
func (mp *MockPlugin) Version() string     { return mp.version }
func (mp *MockPlugin) Description() string { return mp.description }

func (mp *MockPlugin) Initialize(ctx context.Context, config map[string]interface{}) error {
	mp.initialized = true
	return nil
}

func (mp *MockPlugin) Execute(ctx context.Context, input map[string]interface{}) (map[string]interface{}, error) {
	if !mp.initialized {
		return nil, fmt.Errorf("plugin not initialized")
	}
	return map[string]interface{}{
		"result": fmt.Sprintf("Mock plugin %s executed successfully", mp.name),
	}, nil
}

func (mp *MockPlugin) Cleanup() error {
	mp.initialized = false
	return nil
}

func (mp *MockPlugin) HealthCheck() error {
	if !mp.initialized {
		return fmt.Errorf("plugin not initialized")
	}
	return nil
}
