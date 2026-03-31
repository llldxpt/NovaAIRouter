package models

import (
	"encoding/json"
	"time"
)

type MessageType string

const (
	MessageTypeState    MessageType = "state"
	MessageTypeMetadata MessageType = "metadata"
)

type GossipMessage struct {
	NodeID      string                   `json:"node_id"`
	Timestamp   time.Time                `json:"timestamp"`
	Type        MessageType              `json:"type"`
	Endpoints   map[string]*EndpointState `json:"endpoints,omitempty"`
	Descriptions map[string]string       `json:"descriptions,omitempty"`
}

type StateMessage struct {
	NodeID    string                   `json:"node_id"`
	Timestamp time.Time                `json:"timestamp"`
	Type      MessageType              `json:"type"`
	Endpoints map[string]*EndpointState `json:"endpoints"`
}

func NewStateMessage(nodeID string, endpoints map[string]*EndpointState) *StateMessage {
	return &StateMessage{
		NodeID:    nodeID,
		Timestamp: time.Now(),
		Type:      MessageTypeState,
		Endpoints: endpoints,
	}
}

func (m *StateMessage) ToGossipMessage() *GossipMessage {
	return &GossipMessage{
		NodeID:    m.NodeID,
		Timestamp: m.Timestamp,
		Type:      m.Type,
		Endpoints: m.Endpoints,
	}
}

func (m *StateMessage) Serialize() ([]byte, error) {
	return json.Marshal(m)
}

func DeserializeStateMessage(data []byte) (*StateMessage, error) {
	var msg StateMessage
	err := json.Unmarshal(data, &msg)
	if err != nil {
		return nil, err
	}
	return &msg, nil
}

type MetadataMessage struct {
	NodeID       string            `json:"node_id"`
	Timestamp    time.Time         `json:"timestamp"`
	Type         MessageType       `json:"type"`
	Descriptions map[string]string `json:"descriptions"`
}

func NewMetadataMessage(nodeID string, descriptions map[string]string) *MetadataMessage {
	return &MetadataMessage{
		NodeID:       nodeID,
		Timestamp:    time.Now(),
		Type:         MessageTypeMetadata,
		Descriptions: descriptions,
	}
}

func (m *MetadataMessage) ToGossipMessage() *GossipMessage {
	return &GossipMessage{
		NodeID:       m.NodeID,
		Timestamp:    m.Timestamp,
		Type:         m.Type,
		Descriptions: m.Descriptions,
	}
}

func (m *MetadataMessage) Serialize() ([]byte, error) {
	return json.Marshal(m)
}

func DeserializeMetadataMessage(data []byte) (*MetadataMessage, error) {
	var msg MetadataMessage
	err := json.Unmarshal(data, &msg)
	if err != nil {
		return nil, err
	}
	return &msg, nil
}

type EndpointInfoRequest struct {
	ServicePath  string `json:"service_path"`
	NodePath     string `json:"node_path"`
	MaxConcurrent  int    `json:"max_concurrent"`
	Description    string `json:"description"`
	LocalOnly      bool   `json:"local_only"`
	Plugin         bool   `json:"plugin"`
}

type EndpointRegistrationResponse struct {
	ServiceID string   `json:"service_id"`
	Endpoints []string `json:"endpoints"`
}

type EndpointInfoResponse struct {
	Path           string `json:"path"`
	TargetURL      string `json:"target_url"`
	Description    string `json:"description"`
	LocalOnly      bool   `json:"local_only"`
	Plugin         bool   `json:"plugin"`
}

type HeartbeatRequest struct {
	ServiceID string `json:"service_id"`
	Healthy   bool   `json:"healthy"`
}

type EndpointsResponse struct {
	Endpoints  map[string]EndpointDetail `json:"endpoints"`
	Timestamp  time.Time                  `json:"timestamp"`
}

type EndpointDetail struct {
	Description string           `json:"description"`
	Nodes       []EndpointNode   `json:"nodes"`
}

type EndpointNode struct {
	NodeID   string `json:"node_id"`
	Address  string `json:"address"`
	Healthy  bool   `json:"healthy"`
	Active   int32  `json:"active"`
	QueueLen int32  `json:"queue_len"`
	Plugin   bool   `json:"plugin"`
	MaxConcurrent int32 `json:"max_concurrent"`
}

type NodeEndpointInfo struct {
	Path          string `json:"path"`
	Active        int32  `json:"active"`
	QueueLen      int32  `json:"queue_len"`
	Healthy       bool   `json:"healthy"`
	MaxConcurrent int32  `json:"max_concurrent"`
	Plugin        bool   `json:"plugin"`
}

type NodeInfo struct {
	NodeID    string            `json:"node_id"`
	Address   string            `json:"address"`
	ServicePort int             `json:"service_port"`
	Healthy   bool              `json:"healthy"`
	Endpoints []NodeEndpointInfo `json:"endpoints"`
}

type NodesResponse struct {
	Nodes []NodeInfo `json:"nodes"`
}
