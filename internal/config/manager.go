package config

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
)

// ConfigManager manages configuration instances without using global state
type ConfigManager struct {
	mu     sync.RWMutex
	config atomic.Pointer[Config]
}

// NewConfigManager creates a new configuration manager
func NewConfigManager() *ConfigManager {
	return &ConfigManager{}
}

// SetConfig sets the configuration atomically
func (cm *ConfigManager) SetConfig(cfg *Config) {
	cm.config.Store(cfg)
}

// GetConfig returns the current configuration
func (cm *ConfigManager) GetConfig() *Config {
	return cm.config.Load()
}

// InitConfig initializes and sets the configuration
func (cm *ConfigManager) InitConfig(workingDir, dataDir string, debug bool) (*Config, error) {
	cfg, err := Load(workingDir, dataDir, debug)
	if err != nil {
		return nil, err
	}
	cm.SetConfig(cfg)
	return cfg, nil
}

// ProjectNeedsInitialization checks if the project needs initialization
func (cm *ConfigManager) ProjectNeedsInitialization() (bool, error) {
	cfg := cm.GetConfig()
	if cfg == nil {
		return false, fmt.Errorf(ErrConfigNotLoaded)
	}

	flagFilePath := filepath.Join(cfg.Options.DataDirectory, InitFlagFilename)

	_, err := os.Stat(flagFilePath)
	if err == nil {
		return false, nil
	}

	if !os.IsNotExist(err) {
		return false, fmt.Errorf("failed to check init flag file: %w", err)
	}

	someContextFileExists, err := contextPathsExist(cfg.WorkingDir())
	if err != nil {
		return false, fmt.Errorf("failed to check for context files: %w", err)
	}
	if someContextFileExists {
		return false, nil
	}

	return true, nil
}

// MarkProjectInitialized marks the project as initialized
func (cm *ConfigManager) MarkProjectInitialized() error {
	cfg := cm.GetConfig()
	if cfg == nil {
		return fmt.Errorf(ErrConfigNotLoaded)
	}
	flagFilePath := filepath.Join(cfg.Options.DataDirectory, InitFlagFilename)

	file, err := os.Create(flagFilePath)
	if err != nil {
		return fmt.Errorf("failed to create init flag file: %w", err)
	}
	defer file.Close()

	return nil
}

// HasInitialDataConfig checks if there's initial data configuration
func (cm *ConfigManager) HasInitialDataConfig() bool {
	cfg := cm.GetConfig()
	if cfg == nil {
		return false
	}

	cfgPath := GlobalConfigData()
	if _, err := os.Stat(cfgPath); err != nil {
		return false
	}
	return cfg.IsConfigured()
}

// Reset clears the configuration (useful for testing)
func (cm *ConfigManager) Reset() {
	cm.config.Store(nil)
}

// contextPathsExist checks if any default context paths exist in the directory
func contextPathsExist(dir string) (bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false, err
	}

	// Create a slice of lowercase filenames for lookup with slices.Contains
	var files []string
	for _, entry := range entries {
		if !entry.IsDir() {
			files = append(files, strings.ToLower(entry.Name()))
		}
	}

	// Check if any of the default context paths exist in the directory
	for _, path := range defaultContextPaths {
		// Extract just the filename from the path
		_, filename := filepath.Split(path)
		filename = strings.ToLower(filename)

		if slices.Contains(files, filename) {
			return true, nil
		}
	}

	return false, nil
}
