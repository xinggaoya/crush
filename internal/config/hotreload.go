package config

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// HotReloader provides configuration hot-reloading capabilities
type HotReloader struct {
	mu          sync.RWMutex
	config      *Config
	configPath  string
	watcher     *fsnotify.Watcher
	callbacks   []func(*Config) error
	ctx         context.Context
	cancel      context.CancelFunc
	debounceMap map[string]time.Time
}

// HotReloaderCallback is called when configuration is reloaded
type HotReloaderCallback func(*Config) error

// NewHotReloader creates a new configuration hot reloader
func NewHotReloader(configPath string) (*HotReloader, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(context.Background())

	hr := &HotReloader{
		configPath:  configPath,
		watcher:     watcher,
		callbacks:   make([]func(*Config) error, 0),
		ctx:         ctx,
		cancel:      cancel,
		debounceMap: make(map[string]time.Time),
	}

	return hr, nil
}

// Start begins watching for configuration changes
func (hr *HotReloader) Start() error {
	// Watch the config file and its directory
	configDir := filepath.Dir(hr.configPath)

	if err := hr.watcher.Add(configDir); err != nil {
		return err
	}

	// Also watch the specific file if it exists
	if _, err := os.Stat(hr.configPath); err == nil {
		if err := hr.watcher.Add(hr.configPath); err != nil {
			slog.Warn("Failed to watch config file directly", "error", err)
		}
	}

	go hr.watchLoop()
	slog.Info("Configuration hot reloader started", "path", hr.configPath)
	return nil
}

// AddCallback adds a callback to be called when configuration changes
func (hr *HotReloader) AddCallback(callback HotReloaderCallback) {
	hr.mu.Lock()
	defer hr.mu.Unlock()
	hr.callbacks = append(hr.callbacks, callback)
}

// SetConfig sets the current configuration
func (hr *HotReloader) SetConfig(config *Config) {
	hr.mu.Lock()
	defer hr.mu.Unlock()
	hr.config = config
}

// GetConfig returns the current configuration
func (hr *HotReloader) GetConfig() *Config {
	hr.mu.RLock()
	defer hr.mu.RUnlock()
	return hr.config
}

// watchLoop watches for file system events
func (hr *HotReloader) watchLoop() {
	const debounceTime = 500 * time.Millisecond

	for {
		select {
		case <-hr.ctx.Done():
			return
		case event, ok := <-hr.watcher.Events:
			if !ok {
				return
			}

			// Only handle events for our config file
			if !hr.isConfigFile(event.Name) {
				continue
			}

			// Debounce rapid events
			now := time.Now()
			if last, exists := hr.debounceMap[event.Name]; exists && now.Sub(last) < debounceTime {
				continue
			}
			hr.debounceMap[event.Name] = now

			if event.Op&fsnotify.Write == fsnotify.Write || event.Op&fsnotify.Create == fsnotify.Create {
				slog.Debug("Configuration file changed, reloading", "file", event.Name)
				if err := hr.reloadConfig(); err != nil {
					slog.Error("Failed to reload configuration", "error", err)
				}
			}

		case err, ok := <-hr.watcher.Errors:
			if !ok {
				return
			}
			slog.Error("File watcher error", "error", err)
		}
	}
}

// isConfigFile checks if the event is for our config file
func (hr *HotReloader) isConfigFile(filename string) bool {
	return filepath.Clean(filename) == filepath.Clean(hr.configPath)
}

// reloadConfig reloads the configuration from disk
func (hr *HotReloader) reloadConfig() error {
	// Load new configuration
	newConfig, err := Load(filepath.Dir(hr.configPath), "", false) // Use existing dataDir
	if err != nil {
		return err
	}

	hr.mu.Lock()
	oldConfig := hr.config
	hr.config = newConfig
	hr.mu.Unlock()

	// Call callbacks
	hr.mu.RLock()
	callbacks := make([]func(*Config) error, len(hr.callbacks))
	copy(callbacks, hr.callbacks)
	hr.mu.RUnlock()

	for i, callback := range callbacks {
		if err := callback(newConfig); err != nil {
			slog.Error("Configuration reload callback failed", "callback", i, "error", err)
			// Rollback on first error
			hr.mu.Lock()
			hr.config = oldConfig
			hr.mu.Unlock()
			return err
		}
	}

	slog.Info("Configuration reloaded successfully")
	return nil
}

// Stop stops the hot reloader
func (hr *HotReloader) Stop() error {
	hr.cancel()
	if hr.watcher != nil {
		return hr.watcher.Close()
	}
	return nil
}

// ReloadCallbacks provides common reload callbacks
type ReloadCallbacks struct{}

// OnLSPConfigChanged returns a callback for LSP configuration changes
func (rc *ReloadCallbacks) OnLSPConfigChanged(lspService interface{}) HotReloaderCallback {
	return func(config *Config) error {
		// This would restart LSP clients with new configuration
		// Implementation depends on the LSP service interface
		slog.Info("LSP configuration changed", "providers", len(config.LSP))
		return nil
	}
}

// OnProviderConfigChanged returns a callback for provider configuration changes
func (rc *ReloadCallbacks) OnProviderConfigChanged(providerService interface{}) HotReloaderCallback {
	return func(config *Config) error {
		// This would update provider configurations
		providerCount := 0
		config.Providers.Seq2()(func(key string, value ProviderConfig) bool {
			providerCount++
			return true
		})
		slog.Info("Provider configuration changed", "providers", providerCount)
		return nil
	}
}

// OnPermissionsChanged returns a callback for permissions configuration changes
func (rc *ReloadCallbacks) OnPermissionsChanged(permissionService interface{}) HotReloaderCallback {
	return func(config *Config) error {
		// This would update permission settings
		if config.Permissions != nil {
			slog.Info("Permissions configuration changed",
				"skip_requests", config.Permissions.SkipRequests,
				"allowed_tools", len(config.Permissions.AllowedTools))
		}
		return nil
	}
}
