package config

import (
	"context"
	"fmt"
	"sync"
	"time"

	"novaairouter/internal/gossip/message"
)

// ConfigCenter 配置中心接口
type ConfigCenter interface {
	// Start 启动配置中心
	Start() error
	// Shutdown 关闭配置中心
	Shutdown() error
	// GetConfig 获取当前配置
	GetConfig() *Config
	// UpdateConfig 更新配置
	UpdateConfig(cfg *Config) error
	// RegisterConfigListener 注册配置变更监听器
	RegisterConfigListener(listener ConfigListener)
	// UnregisterConfigListener 取消注册配置变更监听器
	UnregisterConfigListener(listener ConfigListener)
}

// ConfigListener 配置变更监听器
type ConfigListener interface {
	// OnConfigChange 配置变更回调
	OnConfigChange(oldConfig, newConfig *Config)
}

// GossipServerInterface Gossip服务器接口
type GossipServerInterface interface {
	BroadcastConfigUpdate(configUpdate *message.GossipMessage)
	GetLatestConfig() *Config
}

// ConfigCenterImpl 配置中心实现
type ConfigCenterImpl struct {
	config        *Config
	gossipServer  GossipServerInterface
	listeners     []ConfigListener
	listenersMutex sync.RWMutex
	ctx           context.Context
	cancel        context.CancelFunc
	updateCh      chan *Config
	shutdownOnce  sync.Once
}

// NewConfigCenter 创建配置中心实例
func NewConfigCenter(cfg *Config, gossipServer GossipServerInterface) ConfigCenter {
	ctx, cancel := context.WithCancel(context.Background())
	return &ConfigCenterImpl{
		config:       cfg,
		gossipServer: gossipServer,
		listeners:    make([]ConfigListener, 0),
		ctx:          ctx,
		cancel:       cancel,
		updateCh:     make(chan *Config, 10),
	}
}

// Start 启动配置中心
func (cc *ConfigCenterImpl) Start() error {
	// 启动配置同步协程
	go cc.syncConfig()
	return nil
}

// Shutdown 关闭配置中心
func (cc *ConfigCenterImpl) Shutdown() error {
	cc.shutdownOnce.Do(func() {
		cc.cancel()
		close(cc.updateCh)
	})
	return nil
}

// GetConfig 获取当前配置
func (cc *ConfigCenterImpl) GetConfig() *Config {
	return cc.config
}

// UpdateConfig 更新配置
func (cc *ConfigCenterImpl) UpdateConfig(cfg *Config) error {
	// 验证配置
	if err := ValidateConfig(cfg); err != nil {
		return err
	}

	// 广播配置更新
	cc.broadcastConfigUpdate(cfg)

	// 发送到更新通道
	select {
	case cc.updateCh <- cfg:
	default:
		return fmt.Errorf("config update channel full, update dropped")
	}

	return nil
}

// RegisterConfigListener 注册配置变更监听器
func (cc *ConfigCenterImpl) RegisterConfigListener(listener ConfigListener) {
	cc.listenersMutex.Lock()
	defer cc.listenersMutex.Unlock()
	cc.listeners = append(cc.listeners, listener)
}

// UnregisterConfigListener 取消注册配置变更监听器
func (cc *ConfigCenterImpl) UnregisterConfigListener(listener ConfigListener) {
	cc.listenersMutex.Lock()
	defer cc.listenersMutex.Unlock()
	for i, l := range cc.listeners {
		if l == listener {
			cc.listeners = append(cc.listeners[:i], cc.listeners[i+1:]...)
			break
		}
	}
}

// syncConfig 同步配置
func (cc *ConfigCenterImpl) syncConfig() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-cc.ctx.Done():
			return
		case newConfig := <-cc.updateCh:
			cc.applyConfigUpdate(newConfig)
		case <-ticker.C:
			// 定期同步配置
			cc.syncWithCluster()
		}
	}
}

// applyConfigUpdate 应用配置更新
func (cc *ConfigCenterImpl) applyConfigUpdate(newConfig *Config) {
	oldConfig := cc.config
	cc.config = newConfig

	// 通知监听器
	cc.notifyListeners(oldConfig, newConfig)
}

// broadcastConfigUpdate 广播配置更新
func (cc *ConfigCenterImpl) broadcastConfigUpdate(cfg *Config) {
	if cc.gossipServer == nil {
		return
	}

	// 构建配置更新消息
	configUpdate := &message.GossipMessage{
		Type:   "config_update",
		NodeID: cfg.NodeID,
		Config: cfg,
	}

	// 广播到集群
	cc.gossipServer.BroadcastConfigUpdate(configUpdate)
}

// syncWithCluster 与集群同步配置
func (cc *ConfigCenterImpl) syncWithCluster() {
	if cc.gossipServer == nil {
		return
	}

	// 从集群获取最新配置
	latestConfig := cc.gossipServer.GetLatestConfig()
	if latestConfig != nil {
		// 只有当配置真正发生变化时才应用
		if !cc.isConfigEqual(cc.config, latestConfig) {
			cc.applyConfigUpdate(latestConfig)
		}
	}
}

// isConfigEqual 比较两个配置是否相等
func (cc *ConfigCenterImpl) isConfigEqual(a, b *Config) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	// 简单比较：节点ID和监听地址相同则认为配置未变化
	return a.NodeID == b.NodeID && a.ListenAddr == b.ListenAddr
}

// notifyListeners 通知监听器
func (cc *ConfigCenterImpl) notifyListeners(oldConfig, newConfig *Config) {
	cc.listenersMutex.RLock()
	listeners := make([]ConfigListener, len(cc.listeners))
	copy(listeners, cc.listeners)
	cc.listenersMutex.RUnlock()

	for _, listener := range listeners {
		listener.OnConfigChange(oldConfig, newConfig)
	}
}
