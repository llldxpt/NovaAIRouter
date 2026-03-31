package config

import (
	"fmt"
	"math/rand"
	"net"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func getNodeIDFromMAC() string {
	hostname, err := os.Hostname()
	if err != nil {
		hostname = "unknown"
	}
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}

	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}

		mac := iface.HardwareAddr
		if len(mac) >= 6 {
			nodeID := fmt.Sprintf("%s-%02x%02x%02x%02x%02x%02x",
				hostname, mac[0], mac[1], mac[2], mac[3], mac[4], mac[5])
			return nodeID
		}
	}
	return ""
}

type Config struct {
	NodeID          string
	ListenAddr      string
	GossipAddr      string
	DiscoveryAddr   string
	APIKey          string
	DisableAdminAuth bool
	TLSEnabled      bool
	TLSCertFile     string
	TLSKeyFile      string
	AccessLogPath   string
	HeartbeatTimeout time.Duration
	DefaultMaxConcurrency int
	GossipStateInterval    time.Duration
	GossipMetadataInterval time.Duration
	GossipProbeInterval    time.Duration
	GossipProbeTimeout     time.Duration
	GossipSuspicionMult    int
	GossipRetransmitMult   int
	LogLevel         string
	BackendTimeout   time.Duration
	QueueTimeout     time.Duration
	ForwardConnectTimeout time.Duration
	ForwardReadTimeout    time.Duration
	QueueCapacity         int
	GossipSyncInterval     time.Duration
	GossipHTTPTimeout     time.Duration
	DiscoveryRetryInterval time.Duration
	DiscoveryPollInterval time.Duration
	DiscoveryBroadcastInterval time.Duration
	DiscoveryNodeTimeout  time.Duration
	DiscoveryMinBroadcastInterval time.Duration
	DiscoveryMaxBroadcastInterval time.Duration
	DiscoveryBackoffMultiplier float64
	DiscoveryNoResponseThreshold int
	SyncPeerTimeout       time.Duration
}

// GetNodeID 获取节点ID
func (c *Config) GetNodeID() string {
	return c.NodeID
}

// GetListenAddr 获取监听地址
func (c *Config) GetListenAddr() string {
	return c.ListenAddr
}

// GetSyncPeerTimeout 获取同步节点超时
func (c *Config) GetSyncPeerTimeout() time.Duration {
	return c.SyncPeerTimeout
}

func AddFlags(cmd *cobra.Command) *viper.Viper {
	v := viper.New()

	cmd.Flags().String("config", "", "Path to YAML configuration file")
	cmd.Flags().String("node-id", "", "Unique node identifier (auto-generated if empty)")
	cmd.Flags().String("listen-addr", ":15050", "Business API listen address (other ports will be auto-calculated)")
	cmd.Flags().String("discovery-addr", ":15052", "Gossip/Discovery listen address (fixed port for cluster)")
	cmd.Flags().String("api-key", "", "API key for authentication and cluster communication")
	cmd.Flags().Bool("disable-admin-auth", false, "Disable admin API key authentication")
	cmd.Flags().Bool("tls-enabled", false, "Enable TLS for business and admin servers")
	cmd.Flags().String("tls-cert-file", "", "Path to TLS certificate file")
	cmd.Flags().String("tls-key-file", "", "Path to TLS private key file")
	cmd.Flags().String("access-log-path", "", "Path to access log file (empty = disabled)")
	cmd.Flags().Duration("heartbeat-timeout", 15*time.Second, "Backend heartbeat timeout")
	cmd.Flags().Int("default-max-concurrency", 10, "Default max concurrent requests per endpoint")
	cmd.Flags().Duration("gossip-state-interval", 3*time.Second, "State broadcast interval")
	cmd.Flags().Duration("gossip-metadata-interval", 1*time.Hour, "Metadata broadcast interval")
	cmd.Flags().Duration("gossip-probe-interval", 1*time.Second, "Gossip probe interval")
	cmd.Flags().Duration("gossip-probe-timeout", 500*time.Millisecond, "Gossip probe timeout")
	cmd.Flags().Int("gossip-suspicion-mult", 4, "Gossip suspicion multiplier")
	cmd.Flags().Int("gossip-retransmit-mult", 3, "Gossip retransmit multiplier")
	cmd.Flags().String("log-level", "info", "Log level (debug, info, warn, error)")
	cmd.Flags().Duration("backend-timeout", 300*time.Second, "Backend call timeout")
	cmd.Flags().Duration("queue-timeout", 30*time.Second, "Queue wait timeout")
	cmd.Flags().Duration("forward-connect-timeout", 10*time.Second, "Forward connection timeout")
	cmd.Flags().Duration("forward-read-timeout", 300*time.Second, "Forward read timeout")
	cmd.Flags().Int("queue-capacity", 1000, "Maximum number of requests that can wait in queue")
	cmd.Flags().Duration("gossip-sync-interval", 10*time.Second, "Gossip config sync interval")
	cmd.Flags().Duration("gossip-http-timeout", 2*time.Second, "Gossip HTTP request timeout")
	cmd.Flags().Duration("discovery-retry-interval", 5*time.Second, "Discovery retry interval")
	cmd.Flags().Duration("discovery-poll-interval", 100*time.Millisecond, "Discovery poll interval")
	cmd.Flags().Duration("discovery-broadcast-interval", 30*time.Second, "Discovery broadcast interval")
	cmd.Flags().Duration("discovery-node-timeout", 60*time.Second, "Discovery node timeout")
	cmd.Flags().Duration("discovery-min-broadcast-interval", 1*time.Second, "Minimum discovery broadcast interval")
	cmd.Flags().Duration("discovery-max-broadcast-interval", 30*time.Minute, "Maximum discovery broadcast interval (adaptive)")
	cmd.Flags().Float64("discovery-backoff-multiplier", 1.5, "Discovery broadcast backoff multiplier")
	cmd.Flags().Int("discovery-no-response-threshold", 3, "Consecutive broadcasts without response before slowing down")
	cmd.Flags().Duration("sync-peer-timeout", 30*time.Second, "Sync peer timeout")

	_ = v.BindPFlags(cmd.Flags())

	return v
}

func Load(cmd *cobra.Command) (*Config, error) {
	flags := cmd.Flags()

	configPath, err := flags.GetString("config")
	if err != nil {
		return nil, fmt.Errorf("failed to get config: %w", err)
	}

	v := viper.New()

	if configPath != "" {
		v.SetConfigFile(configPath)
		v.SetConfigType("yaml")
		if err := v.ReadInConfig(); err != nil {
			return nil, fmt.Errorf("failed to read config file: %w", err)
		}
	}

	v.BindPFlags(flags)

	nodeID := v.GetString("node-id")
	if nodeID == "" {
		if macID := getNodeIDFromMAC(); macID != "" {
			nodeID = macID
		} else {
			hostname, err := os.Hostname()
			if err != nil {
				hostname = "node"
			}
			rand.Seed(time.Now().UnixNano())
			nodeID = fmt.Sprintf("%s-%04x", hostname, rand.Intn(65536))
		}
	}

	apiKey := v.GetString("api-key")
	disableAdminAuth := v.GetBool("disable-admin-auth")
	tlsEnabled := v.GetBool("tls-enabled")
	tlsCertFile := v.GetString("tls-cert-file")
	tlsKeyFile := v.GetString("tls-key-file")
	accessLogPath := v.GetString("access-log-path")

	if !disableAdminAuth && apiKey == "" {
		return nil, fmt.Errorf("--api-key is required when authentication is enabled")
	}

	listenAddr := v.GetString("listen-addr")
	
	var basePort int
	fmt.Sscanf(listenAddr, ":%d", &basePort)
	if basePort == 0 {
		basePort = 15050
	}
	
	gossipAddr := fmt.Sprintf(":%d", basePort+1)
	discoveryAddrStr := v.GetString("discovery-addr")
	var discoveryAddr string
	if discoveryAddrStr != "" {
		discoveryAddr = discoveryAddrStr
	} else {
		discoveryAddr = fmt.Sprintf(":%d", basePort+2)
	}
	heartbeatTimeout := v.GetDuration("heartbeat-timeout")
	maxConcurrency := v.GetInt("default-max-concurrency")
	gossipStateInterval := v.GetDuration("gossip-state-interval")
	gossipMetadataInterval := v.GetDuration("gossip-metadata-interval")
	gossipProbeInterval := v.GetDuration("gossip-probe-interval")
	gossipProbeTimeout := v.GetDuration("gossip-probe-timeout")
	gossipSuspicionMult := v.GetInt("gossip-suspicion-mult")
	gossipRetransmitMult := v.GetInt("gossip-retransmit-mult")
	logLevel := v.GetString("log-level")
	backendTimeout := v.GetDuration("backend-timeout")
	queueTimeout := v.GetDuration("queue-timeout")
	forwardConnectTimeout := v.GetDuration("forward-connect-timeout")
	forwardReadTimeout := v.GetDuration("forward-read-timeout")
	queueCapacity := v.GetInt("queue-capacity")
	gossipSyncInterval := v.GetDuration("gossip-sync-interval")
	gossipHTTPTimeout := v.GetDuration("gossip-http-timeout")
	discoveryRetryInterval := v.GetDuration("discovery-retry-interval")
	discoveryPollInterval := v.GetDuration("discovery-poll-interval")
	discoveryBroadcastInterval := v.GetDuration("discovery-broadcast-interval")
	discoveryNodeTimeout := v.GetDuration("discovery-node-timeout")
	discoveryMinBroadcastInterval := v.GetDuration("discovery-min-broadcast-interval")
	discoveryMaxBroadcastInterval := v.GetDuration("discovery-max-broadcast-interval")
	discoveryBackoffMultiplier := v.GetFloat64("discovery-backoff-multiplier")
	discoveryNoResponseThreshold := v.GetInt("discovery-no-response-threshold")
	syncPeerTimeout := v.GetDuration("sync-peer-timeout")

	cfg := &Config{
		NodeID:          nodeID,
		ListenAddr:      listenAddr,
		GossipAddr:      gossipAddr,
		DiscoveryAddr:   discoveryAddr,
		APIKey:          apiKey,
		DisableAdminAuth: disableAdminAuth,
		TLSEnabled:      tlsEnabled,
		TLSCertFile:     tlsCertFile,
		TLSKeyFile:      tlsKeyFile,
		AccessLogPath:   accessLogPath,
		HeartbeatTimeout: heartbeatTimeout,
		DefaultMaxConcurrency: maxConcurrency,
		GossipStateInterval:    gossipStateInterval,
		GossipMetadataInterval: gossipMetadataInterval,
		GossipProbeInterval:    gossipProbeInterval,
		GossipProbeTimeout:     gossipProbeTimeout,
		GossipSuspicionMult:    gossipSuspicionMult,
		GossipRetransmitMult:   gossipRetransmitMult,
		LogLevel:         logLevel,
		BackendTimeout:   backendTimeout,
		QueueTimeout:     queueTimeout,
		ForwardConnectTimeout: forwardConnectTimeout,
		ForwardReadTimeout:    forwardReadTimeout,
		QueueCapacity:         queueCapacity,
		GossipSyncInterval:    gossipSyncInterval,
		GossipHTTPTimeout:     gossipHTTPTimeout,
		DiscoveryRetryInterval: discoveryRetryInterval,
		DiscoveryPollInterval: discoveryPollInterval,
		DiscoveryBroadcastInterval: discoveryBroadcastInterval,
		DiscoveryNodeTimeout:  discoveryNodeTimeout,
		DiscoveryMinBroadcastInterval: discoveryMinBroadcastInterval,
		DiscoveryMaxBroadcastInterval: discoveryMaxBroadcastInterval,
		DiscoveryBackoffMultiplier: discoveryBackoffMultiplier,
		DiscoveryNoResponseThreshold: discoveryNoResponseThreshold,
		SyncPeerTimeout:       syncPeerTimeout,
	}

	if err := ValidateConfig(cfg); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	return cfg, nil
}

// Reload 重新加载配置文件
func Reload(cfg *Config, configCenter ConfigCenter) error {
	// 使用默认配置文件路径
	configPath := "novaairouter.yaml"

	// 加载配置文件
	cm := NewConfigManager(configPath)
	if err := cm.LoadConfig(); err != nil {
		return fmt.Errorf("failed to load config file: %w", err)
	}

	// 验证配置
	if err := cm.ValidateConfig(); err != nil {
		return fmt.Errorf("invalid config: %w", err)
	}

	// 从配置文件中读取值构建新配置
	newCfg := &Config{
		NodeID:          cfg.NodeID, // 保持节点ID不变
		ListenAddr:      getConfigString(cm, "listen-addr", ":15050"),
		APIKey:          getConfigString(cm, "admin-api-key", cfg.APIKey),
		DisableAdminAuth: getConfigBool(cm, "disable-admin-auth", false),
		HeartbeatTimeout: getConfigDuration(cm, "heartbeat-timeout", 15*time.Second),
		DefaultMaxConcurrency: getConfigInt(cm, "default-max-concurrency", 10),
		GossipStateInterval:   getConfigDuration(cm, "gossip-state-interval", 3*time.Second),
		GossipMetadataInterval: getConfigDuration(cm, "gossip-metadata-interval", time.Hour),
		GossipProbeInterval:    getConfigDuration(cm, "gossip-probe-interval", time.Second),
		GossipProbeTimeout:     getConfigDuration(cm, "gossip-probe-timeout", 500*time.Millisecond),
		GossipSuspicionMult:    getConfigInt(cm, "gossip-suspicion-mult", 4),
		GossipRetransmitMult:   getConfigInt(cm, "gossip-retransmit-mult", 3),
		LogLevel:         getConfigString(cm, "log-level", "info"),
		BackendTimeout:   getConfigDuration(cm, "backend-timeout", 300*time.Second),
		QueueTimeout:     getConfigDuration(cm, "queue-timeout", 30*time.Second),
		ForwardConnectTimeout: getConfigDuration(cm, "forward-connect-timeout", 10*time.Second),
		ForwardReadTimeout:    getConfigDuration(cm, "forward-read-timeout", 300*time.Second),
		QueueCapacity:         getConfigInt(cm, "queue-capacity", 1000),
		GossipSyncInterval:    getConfigDuration(cm, "gossip-sync-interval", 10*time.Second),
		GossipHTTPTimeout:     getConfigDuration(cm, "gossip-http-timeout", 2*time.Second),
		DiscoveryRetryInterval: getConfigDuration(cm, "discovery-retry-interval", 1*time.Second),
		DiscoveryPollInterval: getConfigDuration(cm, "discovery-poll-interval", 100*time.Millisecond),
		DiscoveryBroadcastInterval: getConfigDuration(cm, "discovery-broadcast-interval", 30*time.Second),
		DiscoveryNodeTimeout:  getConfigDuration(cm, "discovery-node-timeout", 60*time.Second),
		DiscoveryMinBroadcastInterval: getConfigDuration(cm, "discovery-min-broadcast-interval", 1*time.Second),
		DiscoveryMaxBroadcastInterval: getConfigDuration(cm, "discovery-max-broadcast-interval", 30*time.Minute),
		DiscoveryBackoffMultiplier: getConfigFloat64(cm, "discovery-backoff-multiplier", 1.5),
		DiscoveryNoResponseThreshold: getConfigInt(cm, "discovery-no-response-threshold", 3),
		SyncPeerTimeout:       getConfigDuration(cm, "sync-peer-timeout", 30*time.Second),
	}

	// 计算派生地址
	listenAddr := newCfg.ListenAddr
	var basePort int
	fmt.Sscanf(listenAddr, ":%d", &basePort)
	if basePort == 0 {
		basePort = 15050
	}
	newCfg.GossipAddr = fmt.Sprintf(":%d", basePort+1)
	newCfg.DiscoveryAddr = fmt.Sprintf(":%d", basePort+2)

	// 更新全局配置
	*cfg = *newCfg

	// 通知配置中心广播配置变更
	if configCenter != nil {
		if err := configCenter.UpdateConfig(cfg); err != nil {
			return fmt.Errorf("failed to broadcast config update: %w", err)
		}
	}

	return nil
}

// getConfigString 从配置管理器获取字符串值
func getConfigString(cm *ConfigManager, key, defaultVal string) string {
	if val, err := cm.GetString(key); err == nil {
		return val
	}
	return defaultVal
}

// getConfigStringSlice 从配置管理器获取字符串切片
func getConfigStringSlice(cm *ConfigManager, key string) []string {
	if val, err := cm.GetString(key); err == nil && val != "" {
		return strings.Split(val, ",")
	}
	return nil
}

// getConfigBool 从配置管理器获取布尔值
func getConfigBool(cm *ConfigManager, key string, defaultVal bool) bool {
	if val, err := cm.GetBool(key); err == nil {
		return val
	}
	return defaultVal
}

// getConfigInt 从配置管理器获取整数值
func getConfigInt(cm *ConfigManager, key string, defaultVal int) int {
	if val, err := cm.GetInt(key); err == nil {
		return val
	}
	return defaultVal
}

// getConfigFloat64 从配置管理器获取浮点数值
func getConfigFloat64(cm *ConfigManager, key string, defaultVal float64) float64 {
	if val, err := cm.GetFloat(key); err == nil {
		return val
	}
	return defaultVal
}

// getConfigDuration 从配置管理器获取时间间隔值
func getConfigDuration(cm *ConfigManager, key string, defaultVal time.Duration) time.Duration {
	if val, err := cm.GetDuration(key); err == nil {
		return val
	}
	return defaultVal
}
