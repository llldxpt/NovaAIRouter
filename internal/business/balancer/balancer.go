package balancer

import (
	"math/rand"

	"novaairouter/internal/models"
)

// DefaultMaxConcurrent 默认最大并发数
const DefaultMaxConcurrent = 10

// Balancer 负载均衡器
type Balancer struct {
}

// New 创建新的负载均衡器
func New() *Balancer {
	return &Balancer{}
}

// SelectNode 选择节点
func (b *Balancer) SelectNode(nodes []*models.RemoteNode, path string) *models.RemoteNode {
	if len(nodes) == 0 {
		return nil
	}

	if len(nodes) == 1 {
		return nodes[0]
	}

	// 计算每个节点的综合负载分数
	type nodeScore struct {
		node  *models.RemoteNode
		score float64
	}

	scores := make([]nodeScore, 0, len(nodes))
	for _, node := range nodes {
		state := node.EndpointStates[path]
		if state == nil {
			state = &models.EndpointState{
				Active:        0,
				QueueLen:      0,
				MaxConcurrent: DefaultMaxConcurrent,
				ResponseTime:  0,
			}
		}

		// 1. 计算负载率 = (活跃连接数 + 队列长度) / 最大并发数
		maxConcurrent := float64(state.MaxConcurrent)
		if maxConcurrent == 0 {
			maxConcurrent = DefaultMaxConcurrent
		}
		load := float64(state.Active) + float64(state.QueueLen)
		loadRatio := load / maxConcurrent

		// 2. 计算响应时间因子（响应时间越长，分数越高）
		responseTimeFactor := 0.0
		if state.ResponseTime > 0 {
			// 响应时间超过100ms开始影响分数，最大影响因子为1.0
			responseTimeFactor = float64(state.ResponseTime) / 100.0
			if responseTimeFactor > 1.0 {
				responseTimeFactor = 1.0
			}
		}

		// 3. 加权计算综合分数
		// 权重：负载率(60%)、响应时间(40%)
		totalScore := loadRatio*0.6 + responseTimeFactor*0.4

		// 4. 添加一些随机性以避免总是选择同一个节点
		randomFactor := 1.0 + (rand.Float64() * 0.1 - 0.05)
		score := totalScore * randomFactor

		scores = append(scores, nodeScore{node: node, score: score})
	}

	// 选择负载最低的节点
	var bestNode *models.RemoteNode
	var bestScore float64 = -1

	for _, ns := range scores {
		if bestScore == -1 || ns.score < bestScore {
			bestScore = ns.score
			bestNode = ns.node
		}
	}

	return bestNode
}


