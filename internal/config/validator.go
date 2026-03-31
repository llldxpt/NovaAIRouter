package config

import (
	"fmt"
)

// ValidateConfig 验证配置是否有效
func ValidateConfig(cfg *Config) error {
	if err := validateNodeID(cfg.NodeID); err != nil {
		return err
	}

	if err := validateListenAddr(cfg.ListenAddr); err != nil {
		return err
	}

	if err := validateGossipAddr(cfg.GossipAddr); err != nil {
		return err
	}

	if err := validateDiscoveryAddr(cfg.DiscoveryAddr); err != nil {
		return err
	}

	if err := validateAdminAPIKey(cfg.APIKey, cfg.DisableAdminAuth); err != nil {
		return err
	}

	if err := validateTimeouts(cfg); err != nil {
		return err
	}

	if err := validateConcurrency(cfg.DefaultMaxConcurrency); err != nil {
		return err
	}

	if err := validateGossipSettings(cfg); err != nil {
		return err
	}

	return nil
}

// validateNodeID 验证节点ID
func validateNodeID(nodeID string) error {
	if nodeID == "" {
		return fmt.Errorf("node ID cannot be empty")
	}
	return nil
}

// validateListenAddr 验证监听地址
func validateListenAddr(addr string) error {
	if addr == "" {
		return fmt.Errorf("listen address cannot be empty")
	}
	return nil
}

// validateGossipAddr 验证Gossip地址
func validateGossipAddr(addr string) error {
	if addr == "" {
		return fmt.Errorf("gossip address cannot be empty")
	}
	return nil
}

// validateDiscoveryAddr 验证发现地址
func validateDiscoveryAddr(addr string) error {
	if addr == "" {
		return fmt.Errorf("discovery address cannot be empty")
	}
	return nil
}

// validateAdminAPIKey 验证管理API密钥
func validateAdminAPIKey(apiKey string, disableAuth bool) error {
	if !disableAuth && apiKey == "" {
		return fmt.Errorf("admin API key is required when authentication is enabled")
	}
	return nil
}

// validateTimeouts 验证超时设置
func validateTimeouts(cfg *Config) error {
	if cfg.HeartbeatTimeout <= 0 {
		return fmt.Errorf("heartbeat timeout must be positive")
	}

	if cfg.BackendTimeout <= 0 {
		return fmt.Errorf("backend timeout must be positive")
	}

	if cfg.QueueTimeout <= 0 {
		return fmt.Errorf("queue timeout must be positive")
	}

	if cfg.ForwardConnectTimeout <= 0 {
		return fmt.Errorf("forward connect timeout must be positive")
	}

	if cfg.ForwardReadTimeout <= 0 {
		return fmt.Errorf("forward read timeout must be positive")
	}

	return nil
}

// validateConcurrency 验证并发设置
func validateConcurrency(maxConcurrency int) error {
	if maxConcurrency < 1 {
		return fmt.Errorf("max concurrency must be at least 1")
	}
	return nil
}

// validateGossipSettings 验证Gossip设置
func validateGossipSettings(cfg *Config) error {
	if cfg.GossipStateInterval <= 0 {
		return fmt.Errorf("gossip state interval must be positive")
	}

	if cfg.GossipMetadataInterval <= 0 {
		return fmt.Errorf("gossip metadata interval must be positive")
	}

	if cfg.GossipProbeInterval <= 0 {
		return fmt.Errorf("gossip probe interval must be positive")
	}

	if cfg.GossipProbeTimeout <= 0 {
		return fmt.Errorf("gossip probe timeout must be positive")
	}

	if cfg.GossipSuspicionMult < 1 {
		return fmt.Errorf("gossip suspicion multiplier must be at least 1")
	}

	if cfg.GossipRetransmitMult < 1 {
		return fmt.Errorf("gossip retransmit multiplier must be at least 1")
	}

	return nil
}
