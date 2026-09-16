package main

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

type Config struct {
	RouterAddr          string `json:"router_addr"`
	PluginAddr          string `json:"plugin_addr"`
	ServiceID           string `json:"service_id"`
	LBAlgorithm         string `json:"lb_algorithm"`

	sessionTimeout      int
	healthCheckInterval int
	electionTimeout     int
	heartbeatInterval   int
	syncInterval        int
	heartbeatToRouter   int
}

func (c *Config) SessionTimeout() time.Duration {
	if c.sessionTimeout == 0 {
		return 10 * time.Minute
	}
	return time.Duration(c.sessionTimeout) * time.Second
}

func (c *Config) HealthCheckInterval() time.Duration {
	if c.healthCheckInterval == 0 {
		return 10 * time.Second
	}
	return time.Duration(c.healthCheckInterval) * time.Second
}

func (c *Config) ElectionTimeout() time.Duration {
	if c.electionTimeout == 0 {
		return 3 * time.Second
	}
	return time.Duration(c.electionTimeout) * time.Second
}

func (c *Config) HeartbeatInterval() time.Duration {
	if c.heartbeatInterval == 0 {
		return 1 * time.Second
	}
	return time.Duration(c.heartbeatInterval) * time.Second
}

func (c *Config) SyncInterval() time.Duration {
	if c.syncInterval == 0 {
		return 30 * time.Second
	}
	return time.Duration(c.syncInterval) * time.Second
}

func (c *Config) HeartbeatToRouter() time.Duration {
	if c.heartbeatToRouter == 0 {
		return 5 * time.Second
	}
	return time.Duration(c.heartbeatToRouter) * time.Second
}

func LoadConfig(path string) (*Config, error) {
	if path == "" {
		return DefaultConfig(), nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config file: %w", err)
	}

	var rawCfg struct {
		RouterAddr          string `json:"router_addr"`
		PluginAddr          string `json:"plugin_addr"`
		ServiceID           string `json:"service_id"`
		SessionTimeout      int    `json:"session_timeout"`
		LBAlgorithm         string `json:"lb_algorithm"`
		HealthCheckInterval int    `json:"health_check_interval"`
		ElectionTimeout     int    `json:"election_timeout"`
		HeartbeatInterval   int    `json:"heartbeat_interval"`
		SyncInterval        int    `json:"sync_interval"`
		HeartbeatToRouter   int    `json:"heartbeat_to_router"`
	}

	if err := json.Unmarshal(data, &rawCfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	cfg := &Config{
		RouterAddr:          rawCfg.RouterAddr,
		PluginAddr:          rawCfg.PluginAddr,
		ServiceID:           rawCfg.ServiceID,
		LBAlgorithm:         rawCfg.LBAlgorithm,
		sessionTimeout:      rawCfg.SessionTimeout,
		healthCheckInterval: rawCfg.HealthCheckInterval,
		electionTimeout:     rawCfg.ElectionTimeout,
		heartbeatInterval:   rawCfg.HeartbeatInterval,
		syncInterval:        rawCfg.SyncInterval,
		heartbeatToRouter:   rawCfg.HeartbeatToRouter,
	}

	return cfg, nil
}

func DefaultConfig() *Config {
	return &Config{
		RouterAddr:          "localhost:15049",
		PluginAddr:          ":18011",
		LBAlgorithm:         "least_conn",
		sessionTimeout:      600,
		healthCheckInterval: 10,
		electionTimeout:     3,
		heartbeatInterval:   1,
		syncInterval:        30,
		heartbeatToRouter:   5,
	}
}

func (c *Config) Validate() error {
	if c.RouterAddr == "" {
		return fmt.Errorf("router_addr is required")
	}
	if c.PluginAddr == "" {
		return fmt.Errorf("plugin_addr is required")
	}
	if c.LBAlgorithm != "round_robin" && c.LBAlgorithm != "least_conn" && c.LBAlgorithm != "fastest" {
		return fmt.Errorf("invalid lb_algorithm: %s", c.LBAlgorithm)
	}
	return nil
}
