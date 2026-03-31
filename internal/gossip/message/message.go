package message

import (
	"novaairouter/internal/models"
)

// GossipMessage Gossip消息
type GossipMessage struct {
	Type         string              `json:"type"`
	NodeID       string              `json:"node_id"`
	Peer         *models.NodeInfo    `json:"peer,omitempty"`
	PathInfos    []PathInfo          `json:"path_infos,omitempty"`
	DeletedPaths []string            `json:"deleted_paths,omitempty"` // 已删除的端点路径
	DeletedNode  bool                `json:"deleted_node,omitempty"`  // 节点是否已下线
	Config       interface{}         `json:"config,omitempty"`
}

// PathInfo 路径信息
type PathInfo struct {
	ServiceID     string `json:"service_id"`
	ServicePath   string `json:"service_path"`
	NodePath      string `json:"node_path"`
	Active        int32  `json:"active"`
	QueueLen      int32  `json:"queue_len"`
	Healthy       bool   `json:"healthy"`
	MaxConcurrent int32  `json:"max_concurrent"`
	Plugin        bool   `json:"plugin"`
}
