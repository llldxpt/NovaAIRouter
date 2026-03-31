package config

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	ConfigFileName = "novaairouter.yaml"
)

var DefaultConfigs = map[string]ConfigItem{
	"node-id": {
		Key:         "node-id",
		DefaultVal:  "",
		Description: "Unique node identifier (auto-generated if empty)",
		Type:        "string",
	},
	"listen-addr": {
		Key:         "listen-addr",
		DefaultVal:  ":15050",
		Description: "Base listen address. Other ports auto-calculated: -1 for Admin API (15049), +1 for Gossip HTTP (15051), +2 for UDP Discovery (15052)",
		Type:        "string",
	},
	"admin-api-key": {
		Key:         "admin-api-key",
		DefaultVal:  "",
		Description: "Admin API key (required if authentication is enabled)",
		Type:        "string",
	},
	"disable-admin-auth": {
		Key:         "disable-admin-auth",
		DefaultVal:  false,
		Description: "Disable admin API key authentication",
		Type:        "bool",
	},
	"heartbeat-timeout": {
		Key:         "heartbeat-timeout",
		DefaultVal:  "15s",
		Description: "Backend heartbeat timeout",
		Type:        "duration",
	},
	"default-max-concurrency": {
		Key:         "default-max-concurrency",
		DefaultVal:  10,
		Description: "Default max concurrent requests per endpoint",
		Type:        "int",
	},
	"gossip-state-interval": {
		Key:         "gossip-state-interval",
		DefaultVal:  "3s",
		Description: "State broadcast interval",
		Type:        "duration",
	},
	"gossip-metadata-interval": {
		Key:         "gossip-metadata-interval",
		DefaultVal:  "1h",
		Description: "Metadata broadcast interval",
		Type:        "duration",
	},
	"gossip-probe-interval": {
		Key:         "gossip-probe-interval",
		DefaultVal:  "1s",
		Description: "Gossip probe interval",
		Type:        "duration",
	},
	"gossip-probe-timeout": {
		Key:         "gossip-probe-timeout",
		DefaultVal:  "500ms",
		Description: "Gossip probe timeout",
		Type:        "duration",
	},
	"gossip-suspicion-mult": {
		Key:         "gossip-suspicion-mult",
		DefaultVal:  4,
		Description: "Gossip suspicion multiplier",
		Type:        "int",
	},
	"gossip-retransmit-mult": {
		Key:         "gossip-retransmit-mult",
		DefaultVal:  3,
		Description: "Gossip retransmit multiplier",
		Type:        "int",
	},
	"gossip-secret-key": {
		Key:         "gossip-secret-key",
		DefaultVal:  "",
		Description: "Gossip encryption key",
		Type:        "string",
	},
	"log-level": {
		Key:         "log-level",
		DefaultVal:  "info",
		Description: "Log level (debug, info, warn, error)",
		Type:        "string",
	},
	"backend-timeout": {
		Key:         "backend-timeout",
		DefaultVal:  "300s",
		Description: "Backend call timeout",
		Type:        "duration",
	},
	"queue-timeout": {
		Key:         "queue-timeout",
		DefaultVal:  "30s",
		Description: "Queue wait timeout",
		Type:        "duration",
	},
	"forward-connect-timeout": {
		Key:         "forward-connect-timeout",
		DefaultVal:  "10s",
		Description: "Forward connection timeout",
		Type:        "duration",
	},
	"forward-read-timeout": {
		Key:         "forward-read-timeout",
		DefaultVal:  "300s",
		Description: "Forward read timeout",
		Type:        "duration",
	},
	"queue-capacity": {
		Key:         "queue-capacity",
		DefaultVal:  1000,
		Description: "Maximum number of requests that can wait in queue",
		Type:        "int",
	},
	"gossip-sync-interval": {
		Key:         "gossip-sync-interval",
		DefaultVal:  "10s",
		Description: "Gossip config sync interval",
		Type:        "duration",
	},
	"gossip-http-timeout": {
		Key:         "gossip-http-timeout",
		DefaultVal:  "2s",
		Description: "Gossip HTTP request timeout",
		Type:        "duration",
	},
	"discovery-retry-interval": {
		Key:         "discovery-retry-interval",
		DefaultVal:  "5s",
		Description: "Discovery retry interval",
		Type:        "duration",
	},
	"discovery-poll-interval": {
		Key:         "discovery-poll-interval",
		DefaultVal:  "100ms",
		Description: "Discovery poll interval",
		Type:        "duration",
	},
	"discovery-broadcast-interval": {
		Key:         "discovery-broadcast-interval",
		DefaultVal:  "30s",
		Description: "Discovery broadcast interval",
		Type:        "duration",
	},
	"discovery-node-timeout": {
		Key:         "discovery-node-timeout",
		DefaultVal:  "60s",
		Description: "Discovery node timeout",
		Type:        "duration",
	},
	"sync-peer-timeout": {
		Key:         "sync-peer-timeout",
		DefaultVal:  "30s",
		Description: "Sync peer timeout",
		Type:        "duration",
	},
}

type ConfigItem struct {
	Key         string      `yaml:"key"`
	DefaultVal  interface{} `yaml:"default"`
	Description string      `yaml:"description"`
	Type        string      `yaml:"type"`
	Value       interface{} `yaml:"value,omitempty"`
}

type ConfigFile struct {
	Version     string                   `yaml:"version"`
	GeneratedAt string                   `yaml:"generated_at"`
	Configs     map[string]ConfigItem    `yaml:"configs"`
	Errors      []string                 `yaml:"errors,omitempty"`
}

type ConfigManager struct {
	configPath string
	configFile *ConfigFile
}

func NewConfigManager(configPath string) *ConfigManager {
	if configPath == "" {
		configPath = ConfigFileName
	}
	return &ConfigManager{
		configPath: configPath,
	}
}

func (cm *ConfigManager) LoadOrCreate() error {
	if _, err := os.Stat(cm.configPath); os.IsNotExist(err) {
		fmt.Printf("[Config] Config file not found, creating default config at: %s\n", cm.configPath)
		if err := cm.CreateDefaultConfig(); err != nil {
			return fmt.Errorf("failed to create default config: %w", err)
		}
	}

	if err := cm.LoadConfig(); err != nil {
		return err
	}

	if err := cm.ValidateConfig(); err != nil {
		return err
	}

	return nil
}

func (cm *ConfigManager) LoadConfig() error {
	data, err := os.ReadFile(cm.configPath)
	if err != nil {
		return fmt.Errorf("failed to read config file: %w", err)
	}

	var configFile ConfigFile
	if err := yaml.Unmarshal(data, &configFile); err != nil {
		return fmt.Errorf("failed to parse config file (YAML format error): %w", err)
	}

	cm.configFile = &configFile
	return nil
}

func (cm *ConfigManager) ValidateConfig() error {
	var errors []string

	for key := range DefaultConfigs {
		if item, exists := cm.configFile.Configs[key]; exists {
			if err := cm.validateItem(key, item); err != nil {
				errors = append(errors, err.Error())
			}
		} else {
			errors = append(errors, fmt.Sprintf("missing required config: %s", key))
		}
	}

	if len(errors) > 0 {
		fmt.Printf("\n[Config] Configuration errors found:\n")
		for _, err := range errors {
			fmt.Printf("  - %s\n", err)
		}
		fmt.Printf("\n[Config] Please fix the above errors in config file: %s\n", cm.configPath)
		fmt.Println("[Config] Or delete the config file to regenerate with defaults")
		return fmt.Errorf("config validation failed: %d error(s) found", len(errors))
	}

	return nil
}

func (cm *ConfigManager) validateItem(key string, item ConfigItem) error {
	defaultItem := DefaultConfigs[key]

	switch defaultItem.Type {
	case "duration":
		if _, ok := item.Value.(string); !ok {
			return fmt.Errorf("config '%s' must be a duration string (e.g., '5s', '1m')", key)
		}
		if _, err := time.ParseDuration(item.Value.(string)); err != nil {
			return fmt.Errorf("config '%s' has invalid duration format: %v", key, err)
		}
	case "int":
		if _, ok := item.Value.(int); !ok {
			if _, ok := item.Value.(int64); !ok {
				return fmt.Errorf("config '%s' must be an integer", key)
			}
		}
	case "bool":
		if _, ok := item.Value.(bool); !ok {
			return fmt.Errorf("config '%s' must be a boolean (true/false)", key)
		}
	case "string":
		if _, ok := item.Value.(string); !ok {
			return fmt.Errorf("config '%s' must be a string", key)
		}
	}

	return nil
}

func (cm *ConfigManager) CreateDefaultConfig() error {
	dir := filepath.Dir(cm.configPath)
	if dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("failed to create config directory: %w", err)
		}
	}

	configs := make(map[string]ConfigItem)
	for key, item := range DefaultConfigs {
		configs[key] = ConfigItem{
			Key:         item.Key,
			DefaultVal:  item.DefaultVal,
			Description: item.Description,
			Type:        item.Type,
			Value:       item.DefaultVal,
		}
	}

	configFile := ConfigFile{
		Version:     "1.0",
		GeneratedAt: time.Now().Format(time.RFC3339),
		Configs:     configs,
	}

	data, err := yaml.Marshal(configFile)
	if err != nil {
		return fmt.Errorf("failed to marshal default config: %w", err)
	}

	if err := os.WriteFile(cm.configPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}

	fmt.Printf("[Config] Default config file created at: %s\n", cm.configPath)
	fmt.Println("[Config] Please edit the config file to customize your settings")
	return nil
}

func (cm *ConfigManager) GetConfigFile() *ConfigFile {
	return cm.configFile
}

func (cm *ConfigManager) GetValue(key string) (interface{}, error) {
	if cm.configFile == nil {
		return nil, fmt.Errorf("config not loaded")
	}

	if item, exists := cm.configFile.Configs[key]; exists {
		return item.Value, nil
	}

	if defaultItem, exists := DefaultConfigs[key]; exists {
		return defaultItem.DefaultVal, nil
	}

	return nil, fmt.Errorf("config key not found: %s", key)
}

func (cm *ConfigManager) GetString(key string) (string, error) {
	val, err := cm.GetValue(key)
	if err != nil {
		return "", err
	}
	if str, ok := val.(string); ok {
		return str, nil
	}
	return "", fmt.Errorf("config '%s' is not a string", key)
}

func (cm *ConfigManager) GetInt(key string) (int, error) {
	val, err := cm.GetValue(key)
	if err != nil {
		return 0, err
	}
	if n, ok := val.(int); ok {
		return n, nil
	}
	if n64, ok := val.(int64); ok {
		return int(n64), nil
	}
	return 0, fmt.Errorf("config '%s' is not an int", key)
}

func (cm *ConfigManager) GetBool(key string) (bool, error) {
	val, err := cm.GetValue(key)
	if err != nil {
		return false, err
	}
	if b, ok := val.(bool); ok {
		return b, nil
	}
	return false, fmt.Errorf("config '%s' is not a bool", key)
}

func (cm *ConfigManager) GetFloat(key string) (float64, error) {
	val, err := cm.GetValue(key)
	if err != nil {
		return 0, err
	}
	if f, ok := val.(float64); ok {
		return f, nil
	}
	if n, ok := val.(int); ok {
		return float64(n), nil
	}
	if n64, ok := val.(int64); ok {
		return float64(n64), nil
	}
	return 0, fmt.Errorf("config '%s' is not a float", key)
}

func (cm *ConfigManager) GetDuration(key string) (time.Duration, error) {
	val, err := cm.GetValue(key)
	if err != nil {
		return 0, err
	}
	if str, ok := val.(string); ok {
		return time.ParseDuration(str)
	}
	return 0, fmt.Errorf("config '%s' is not a duration", key)
}

func (cm *ConfigManager) ResetToDefaults() error {
	fmt.Printf("[Config] Resetting config to defaults: %s\n", cm.configPath)
	return cm.CreateDefaultConfig()
}
