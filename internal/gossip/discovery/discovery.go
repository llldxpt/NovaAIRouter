package discovery

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"time"

	"github.com/rs/zerolog"

	"novaairouter/internal/config"
)

const (
	DiscPortOffset    = 2
	DiscoveryPort     = 15052
	DiscoveryPortUsed = true
)

// Discovery 节点发现服务
type Discovery struct {
	config      *config.Config
	log         zerolog.Logger
	udpConn     *net.UDPConn
	candidates  []*NodeInfo
	candMu      chan struct{}
	joinCallback func(nodeID, address string)
	done         chan struct{}
}

// NodeInfo 节点信息
type NodeInfo struct {
	NodeID    string    `json:"node_id"`
	AdminAddr string    `json:"admin_addr"`
	DiscPort  int       `json:"disc_port"`
	LastSeen  time.Time `json:"last_seen"`
}

// OnJoinNode 设置节点加入回调函数
func (d *Discovery) OnJoinNode(callback func(nodeID, address string)) {
	d.joinCallback = callback
}

// New 创建新的节点发现服务
func New(cfg *config.Config, log zerolog.Logger) *Discovery {
	return &Discovery{
		config:     cfg,
		log:        log,
		candidates: make([]*NodeInfo, 0),
		candMu:     make(chan struct{}, 1),
		done:       make(chan struct{}),
	}
}

// Start 启动节点发现服务
func (d *Discovery) Start() error {
	d.log.Info().Msg("Starting node discovery service")

	go d.startUDPListener()
	go d.broadcastPresence()
	go d.startDiscovery()
	go d.periodicHealthCheck()

	return nil
}

// Stop 停止节点发现服务
func (d *Discovery) Stop() {
	close(d.done)
	if d.udpConn != nil {
		d.udpConn.Close()
	}
	d.log.Info().Msg("Node discovery service stopped")
}

// GetBroadcastPort 获取广播端口
func (d *Discovery) GetBroadcastPort() int {
	if DiscoveryPortUsed {
		bindPort := 0
		fmt.Sscanf(d.config.ListenAddr, ":%d", &bindPort)
		return bindPort + DiscPortOffset
	}
	return DiscoveryPort
}

// startUDPListener 启动UDP监听器
func (d *Discovery) startUDPListener() {
	for {
		select {
		case <-d.done:
			return
		default:
		}

		broadcastPort := d.GetBroadcastPort()
		addr, err := net.ResolveUDPAddr("udp4", fmt.Sprintf(":%d", broadcastPort))
		if err != nil {
			d.log.Error().Err(err).Msg("Failed to resolve UDP address")
			select {
			case <-d.done:
				return
			case <-time.After(5 * time.Second):
			}
			continue
		}

		conn, err := net.ListenUDP("udp4", addr)
		if err != nil {
			d.log.Warn().Int("port", broadcastPort).Err(err).Msg("Failed to listen on UDP, retrying in 5s")
			select {
			case <-d.done:
				return
			case <-time.After(5 * time.Second):
			}
			continue
		}

		d.udpConn = conn
		d.log.Info().Int("port", broadcastPort).Msg("UDP broadcast listener started")

		buf := make([]byte, 1024)
		for {
			n, srcAddr, err := conn.ReadFromUDP(buf)
			if err != nil {
				d.log.Warn().Err(err).Msg("UDP read error, restarting listener")
				conn.Close()
				break
			}

			var msg BroadcastMessage
			if err := json.Unmarshal(buf[:n], &msg); err != nil {
				continue
			}

			if msg.Type != "announce" {
				continue
			}

			srcIP := srcAddr.IP.String()

			if msg.NodeID == d.config.NodeID {
				continue
			}

			// Verify auth hash if API key is configured
			if d.config.APIKey != "" {
				if !verifyAuthHash(msg.NodeID, msg.Addr, msg.AuthHash, d.config.APIKey) {
					d.log.Warn().Str("node_id", msg.NodeID).Msg("Invalid auth hash, ignoring broadcast")
					continue
				}
			}

				d.log.Info().
				Str("node_id", msg.NodeID).
				Str("src_ip", srcIP).
				Int("base_port", msg.BasePort).
				Msg("Received broadcast from node")

			adminAddr := srcIP
			if msg.Addr != "" {
				adminAddr = msg.Addr
			}
			d.log.Info().Str("admin_addr", adminAddr).Msg("Using admin address for node")

			nodeInfo := &NodeInfo{
				NodeID:    msg.NodeID,
				AdminAddr: adminAddr,
				DiscPort:  msg.DiscPort,
				LastSeen:  time.Now(),
			}

			d.addCandidate(nodeInfo)
		}
	}
}

// broadcastPresence 广播节点存在
func (d *Discovery) broadcastPresence() {
	d.log.Info().Msg("Broadcasting presence")

	bindPort := 0
	fmt.Sscanf(d.config.ListenAddr, ":%d", &bindPort)
	broadcastPort := d.GetBroadcastPort()

	msg := BroadcastMessage{
		Type:     "announce",
		NodeID:   d.config.NodeID,
		Addr:     "",
		BasePort: bindPort,
		DiscPort: broadcastPort,
		AuthHash: computeAuthHash(d.config.NodeID, "", d.config.APIKey),
	}

	data, err := json.Marshal(msg)
	if err != nil {
		d.log.Error().Err(err).Msg("Failed to marshal broadcast message")
		return
	}

	broadcastAddr := fmt.Sprintf("255.255.255.255:%d", broadcastPort)
	udpAddr, err := net.ResolveUDPAddr("udp4", broadcastAddr)
	if err != nil {
		d.log.Error().Err(err).Msg("Failed to resolve broadcast address")
		return
	}

	conn, err := net.DialUDP("udp4", nil, udpAddr)
	if err != nil {
		d.log.Error().Err(err).Msg("Failed to create UDP connection")
		return
	}
	defer conn.Close()

	for i := 0; i < 3; i++ {
		_, err := conn.Write(data)
		if err != nil {
			d.log.Error().Err(err).Msg("Failed to write broadcast message")
			return
		}
		time.Sleep(100 * time.Millisecond)
	}

	d.log.Info().Msg("Broadcasted presence")
}

// startDiscovery 开始发现节点
func (d *Discovery) startDiscovery() {
	time.Sleep(1 * time.Second)

	candidates := d.getCandidates()
	for _, cand := range candidates {
		d.log.Info().Str("candidate_ip", cand.AdminAddr).Msg("Attempting to join node")
		if d.joinCallback != nil {
			d.joinCallback(cand.NodeID, cand.AdminAddr)
		}
	}

	go d.periodicBroadcast()
}

// periodicBroadcast 定期广播
func (d *Discovery) periodicBroadcast() {
	d.broadcastPresence()
	d.discoverNewNodes()

	// 减少广播频率，避免网络风暴
	broadcastTicker := time.NewTicker(30 * time.Second)
	discoveryTicker := time.NewTicker(15 * time.Second)
	defer broadcastTicker.Stop()
	defer discoveryTicker.Stop()

	for {
		select {
		case <-d.done:
			return
		case <-broadcastTicker.C:
			d.broadcastPresence()
		case <-discoveryTicker.C:
			d.discoverNewNodes()
		}
	}
}

// discoverNewNodes 发现新节点
func (d *Discovery) discoverNewNodes() {
	candidates := d.getCandidates()
	for _, cand := range candidates {
		if cand.NodeID != d.config.NodeID {
			d.log.Info().Str("candidate_ip", cand.AdminAddr).Msg("Discovering new node")
			// 这里需要调用gossip服务的joinNode方法
		}
	}
}

// periodicHealthCheck 定期健康检查
func (d *Discovery) periodicHealthCheck() {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-d.done:
			return
		case <-ticker.C:
			d.cleanupStaleCandidates()
		}
	}
}

// addCandidate 添加候选节点
func (d *Discovery) addCandidate(nodeInfo *NodeInfo) {
	d.candMu <- struct{}{}
	defer func() { <-d.candMu }()

	isNew := true
	for _, c := range d.candidates {
		if c.NodeID == nodeInfo.NodeID {
			c.LastSeen = time.Now()
			c.AdminAddr = nodeInfo.AdminAddr
			isNew = false
			break
		}
	}
	if isNew {
		d.candidates = append(d.candidates, nodeInfo)
		d.log.Info().Str("node_id", nodeInfo.NodeID).Str("addr", nodeInfo.AdminAddr).Msg("New candidate discovered, initiating join")
		if d.joinCallback != nil {
			go d.joinCallback(nodeInfo.NodeID, nodeInfo.AdminAddr)
		}
	}
}

// getCandidates 获取候选节点
func (d *Discovery) getCandidates() []*NodeInfo {
	d.candMu <- struct{}{}
	defer func() { <-d.candMu }()

	result := make([]*NodeInfo, len(d.candidates))
	copy(result, d.candidates)
	return result
}

// cleanupStaleCandidates 清理过期的候选节点
func (d *Discovery) cleanupStaleCandidates() {
	d.candMu <- struct{}{}
	defer func() { <-d.candMu }()

	now := time.Now()
	var validCandidates []*NodeInfo
	for _, c := range d.candidates {
		if now.Sub(c.LastSeen) > candidateTimeout {
			d.log.Info().Str("node_id", c.NodeID).Msg("Removing stale candidate")
			continue
		}
		validCandidates = append(validCandidates, c)
	}
	d.candidates = validCandidates
}

// BroadcastMessage 广播消息
type BroadcastMessage struct {
	Type     string `json:"type"`
	NodeID   string `json:"node_id"`
	Addr     string `json:"addr"`
	BasePort int    `json:"base_port"`
	DiscPort int    `json:"disc_port"`
	AuthHash string `json:"auth_hash,omitempty"`
}

const (
	candidateTimeout = 60 * time.Second
)

func computeAuthHash(nodeID, addr, apiKey string) string {
	if apiKey == "" {
		return ""
	}
	h := hmac.New(sha256.New, []byte(apiKey))
	h.Write([]byte(nodeID + addr))
	return hex.EncodeToString(h.Sum(nil))
}

func verifyAuthHash(nodeID, addr, authHash, apiKey string) bool {
	if apiKey == "" {
		return true
	}
	expected := computeAuthHash(nodeID, addr, apiKey)
	return hmac.Equal([]byte(authHash), []byte(expected))
}
