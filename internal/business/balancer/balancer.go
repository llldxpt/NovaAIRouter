package balancer

import (
	"math/rand"

	"novaairouter/internal/models"
)

// weightedRandomIndex 在权重列表中按权重随机选一个下标
func weightedRandomIndex(weights []int32) int {
	total := int32(0)
	for _, w := range weights {
		total += w
	}
	if total <= 0 {
		return rand.Intn(len(weights))
	}
	r := rand.Int31n(total)
	for i, w := range weights {
		r -= w
		if r < 0 {
			return i
		}
	}
	return len(weights) - 1
}

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
		// EndpointStates key is nodePath, not request path — find the matching state
		var state *models.EndpointState
		for _, s := range node.EndpointStates {
			state = s
			break
		}
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

	// 选出最低 score 的节点集合，在其中按 maxConcurrent 加权随机选一个
	bestScore := scores[0].score
	for _, ns := range scores[1:] {
		if ns.score < bestScore {
			bestScore = ns.score
		}
	}

	// 收集所有 score 在 bestScore ±5% 范围内的节点（容忍随机扰动误差）
	var candidates []nodeScore
	for _, ns := range scores {
		if ns.score <= bestScore*1.05+0.001 {
			candidates = append(candidates, ns)
		}
	}

	if len(candidates) == 1 {
		return candidates[0].node
	}

	// 按 maxConcurrent 加权随机
	weights := make([]int32, len(candidates))
	for i, ns := range candidates {
		var state *models.EndpointState
		for _, s := range ns.node.EndpointStates {
			state = s
			break
		}
		if state != nil && state.MaxConcurrent > 0 {
			weights[i] = state.MaxConcurrent
		} else {
			weights[i] = int32(DefaultMaxConcurrent)
		}
	}
	return candidates[weightedRandomIndex(weights)].node
}


