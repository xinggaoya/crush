package config

import (
	"sync/atomic"
)

const (
	InitFlagFilename   = "init"
	ErrConfigNotLoaded = "config not loaded"
)

type ProjectInitFlag struct {
	Initialized bool `json:"initialized"`
}

// Global config manager instance - will be removed once migration is complete
var defaultManager = NewConfigManager()
var instance atomic.Pointer[Config]

// Init initializes the configuration using the manager
func Init(workingDir, dataDir string, debug bool) (*Config, error) {
	return defaultManager.InitConfig(workingDir, dataDir, debug)
}

// Get returns the current configuration using the manager
func Get() *Config {
	return defaultManager.GetConfig()
}

// Legacy functions for backward compatibility - will be removed after migration
func InitLegacy(workingDir, dataDir string, debug bool) (*Config, error) {
	cfg, err := Load(workingDir, dataDir, debug)
	if err != nil {
		return nil, err
	}
	instance.Store(cfg)
	return instance.Load(), nil
}

func GetLegacy() *Config {
	cfg := instance.Load()
	return cfg
}

func ProjectNeedsInitialization() (bool, error) {
	return defaultManager.ProjectNeedsInitialization()
}

func MarkProjectInitialized() error {
	return defaultManager.MarkProjectInitialized()
}

func HasInitialDataConfig() bool {
	return defaultManager.HasInitialDataConfig()
}
