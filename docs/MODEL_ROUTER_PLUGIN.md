# Nova AI Router Model Router 插件设计文档

## 1. 概述

Model Router 插件是一个运行在 Nova AI Router 集群中的智能路由服务，主要功能包括：

- **模型路由**：根据请求中的 `model` 参数将 Chat 请求路由到对应的后端服务
- **模型目录**：聚合集群中所有后端服务的 `/v1/models` 接口，返回统一的模型列表
- **负载均衡**：支持多种负载均衡算法（轮询、最少连接、最快响应）
- **故障转移**：当后端服务不可用时，自动切换到其他健康的后端
- **会话亲和性**：支持会话绑定，确保同一会话的请求路由到同一后端以利用上下文缓存
- **主从选举**：多插件实例运行时，自动选举出一个主节点负责处理请求

## 2. 部署架构

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                           Nova AI Router 集群                                │
│                                                                             │
│   ┌─────────────────────────────────────────────────────────────────┐       │
│   │                    Gossip 协议 (集群内部通讯)                     │       │
│   └─────────────────────────────────────────────────────────────────┘       │
│                                    │                                        │
│        ┌──────────────────────────┼──────────────────────────┐            │
│        │                          │                          │            │
│        ▼                          ▼                          ▼            │
│  ┌──────────┐            ┌──────────┐            ┌──────────┐        │
│  │ Node-A   │            │ Node-B   │            │ Node-C   │        │
│  │  :15050  │            │  :15050  │            │  :15050  │        │
│  └────┬─────┘            └────┬─────┘            └────┬─────┘        │
│       │                        │                        │                │
│       │  /v1/chat (plugin)   │  /v1/chat (plugin)   │  /v1/chat     │
│       ▼                       ▼                       ▼                │
│  ┌─────────────────────────────────────────────────────────────────┐     │
│  │              Model Router Plugin Service (:18011)                 │     │
│  │  - 主节点 (Master)                                              │     │
│  │  - 维护 model → backend 映射                                    │     │
│  │  - 处理所有 Chat/Models 请求                                    │     │
│  │  - 与其他插件通过 /v1/plugin/* 通讯                             │     │
│  └─────────────────────────────────────────────────────────────────┘     │
│                                    │                                        │
│                                    │ 转发请求                               │
│                                    ▼                                        │
│        ┌──────────────────────────┬──────────────────────────┐            │
│        │                          │                          │            │
│        ▼                          ▼                          ▼            │
│  ┌──────────┐            ┌──────────┐            ┌──────────┐        │
│  │ Backend-A │            │ Backend-B │            │ Backend-C │        │
│  │  :18001  │            │  :18002  │            │  :18003  │        │
│  │ gpt-4o   │            │ gpt-4o   │            │ claude-3  │        │
│  └──────────┘            └──────────┘            └──────────┘        │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

## 3. 插件注册

### 3.1 注册端点

插件需要向 Nova AI Router 注册两个端点：

```bash
curl -X POST http://<router_addr>:15049/v1/endpoints \
  -H "Content-Type: application/json" \
  -d '[
    {
      "node_path": "/v1/chat/completions",
      "service_path": ":18011/v1/chat/completions",
      "plugin": true,
      "description": "router"
    },
    {
      "node_path": "/v1/models",
      "service_path": ":18011/v1/models",
      "plugin": true,
      "description": "router"
    }
  ]'
```

**参数说明：**

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| node_path | string | 是 | 注册到网关的路径，用于路由匹配 |
| service_path | string | 是 | 插件服务监听地址，格式：`:端口/路径` |
| plugin | bool | 是 | 必须设置为 `true`，标识为插件 |
| description | string | 是 | 描述信息，用于标识插件类型（所有相同类型的插件使用相同描述） |

### 3.2 注册响应

```json
{
  "service_id": "abc123def456",
  "endpoints": [
    "/v1/chat/completions",
    "/v1/models"
  ]
}
```

**重要：** `service_id` 用于后续的插件间通讯，必须保存。

### 3.3 发送心跳

插件需要定期发送心跳保持健康状态：

```bash
curl -X POST http://<router_addr>:15049/v1/heartbeat \
  -H "Content-Type: application/json" \
  -d '{
    "service_id": "abc123def456",
    "healthy": true
  }'
```

心跳间隔建议为 5 秒。

## 4. 插件间通讯 API

Nova AI Router 提供了三个插件通讯端点，插件利用这些 API 进行主从选举和数据同步。

### 4.1 获取同类型插件列表

```http
POST /v1/plugin/peers

Request Body:
{
  "service_id": "abc123def456"
}

Response:
{
  "service_id": "abc123def456",
  "node_path": "/v1/chat/completions",
  "peers": [
    {"service_id": "def456ghi789"},
    {"service_id": "ghi789jkl012"}
  ]
}
```

**说明：**
- 传入自身 `service_id`
- Router 根据 `service_id` 找到对应的 `node_path`（如 `/v1/chat/completions`）
- 返回所有注册了相同 `node_path` 的其他插件的 `service_id`

### 4.2 单发消息

```http
POST /v1/plugin/send

Request Body:
{
  "to_service_id": "def456ghi789",
  "message": {
    "type": "election",
    "action": "vote",
    "term": 5,
    "from": "abc123def456"
  }
}

Response:
{
  "status": "ok"
}
```

### 4.3 广播消息

```http
POST /v1/plugin/broadcast

Request Body:
{
  "message": {
    "type": "election",
    "action": "vote",
    "term": 5,
    "from": "abc123def456"
  }
}

Response:
{
  "status": "ok",
  "recipients": 3
}
```

## 5. 数据结构设计

### 5.1 后端服务信息

```go
// Backend 代表一个后端服务实例
type Backend struct {
    Address      string    // 后端服务地址 (如 "192.168.1.100")
    Port         int       // 后端服务端口 (如 18001)
    NodePath     string    // 路由路径 (如 "/v1/chat/completions")
    Healthy      bool      // 健康状态
    ActiveConn   int32     // 当前活跃连接数
    AvgLatency   int64     // 平均延迟 (毫秒)
    LastSuccess  time.Time // 最后成功时间
}
```

### 5.2 模型后端映射

```go
// ModelBackend 代表一个模型对应的所有后端
type ModelBackend struct {
    Model    string     // 模型名称 (如 "gpt-4o")
    Backends []*Backend // 提供该模型的后端列表
}
```

### 5.3 会话绑定信息

```go
// SessionInfo 代表一个会话的绑定信息
type SessionInfo struct {
    SessionID    string    // 会话 ID
    BackendAddr  string    // 绑定的后端地址
    BackendPort  int       // 绑定的后端端口
    LastActivity time.Time // 最后活动时间
}
```

### 5.4 插件配置

```go
// Config 插件配置
type Config struct {
    RouterAddr        string        // Nova Router 地址 (如 "localhost:15049")
    PluginAddr       string        // 插件监听地址 (如 "localhost:18011")
    SyncPort         int           // 同步服务端口 (如 18020)
    ServiceID        string        // 注册后获得的 service_id

    // 会话配置
    SessionTimeout   time.Duration // 会话超时时间 (默认 10 分钟)

    // 负载均衡配置
    LBAlgorithm     string        // 负载均衡算法: "round_robin", "least_conn", "fastest"

    // 健康检查配置
    HealthCheckInterval time.Duration // 健康检查间隔 (默认 10 秒)

    // 选举配置
    ElectionTimeout  time.Duration // 选举超时 (默认 3 秒)
    HeartbeatInterval time.Duration // 主节点心跳间隔 (默认 1 秒)
}
```

### 5.5 插件主结构

```go
// Plugin Model Router 插件主结构
type Plugin struct {
    config *Config

    // 节点信息
    isMaster     bool           // 是否为主节点
    masterID     string         // 主节点 service_id
    term         int64          // 选举任期

    // 数据存储
    modelToBackends map[string]*ModelBackend  // model -> 后端列表
    sessionToBackend map[string]*SessionInfo   // session_id -> 绑定信息

    // 负载均衡
    rrIndex int32  // 轮询索引

    // 同步
    mu sync.RWMutex
}
```

## 6. 核心功能实现

### 6.1 集群信息同步

插件启动时，需要从 Nova Router 获取集群中所有节点的信息，构建 model → backend 映射。

#### 6.1.1 获取全局节点信息

```bash
curl http://<router_addr>:15049/v1/global
```

响应：
```json
{
  "nodes": [
    {
      "node_id": "node-a",
      "healthy": true,
      "path_infos": [
        {
          "node_path": "/v1/chat/completions",
          "description": "gpt-4o,gpt-4o-mini",
          "plugin": true
        }
      ]
    }
  ]
}
```

#### 6.1.2 获取节点地址信息

```bash
curl http://<router_addr>:15049/v1/node/node-a
```

响应：
```json
{
  "node_id": "node-a",
  "address": "192.168.1.100",
  "service_port": 18001
}
```

#### 6.1.3 同步逻辑

```
┌─────────────────────────────────────────────────────────────────────────┐
│                         同步流程                                         │
├─────────────────────────────────────────────────────────────────────────┤
│                                                                         │
│  1. 调用 GET /v1/global                                                │
│     → 获取所有节点及其 path_infos                                     │
│     → 从 description 字段解析模型列表                                   │
│                                                                         │
│  2. 对每个节点调用 GET /v1/node/{node_id}                             │
│     → 获取 address 和 service_port                                     │
│                                                                         │
│  3. 构建 modelToBackends 映射                                          │
│     {                                                                  │
│       "gpt-4o": [                                                     │
│         {Address: "192.168.1.100", Port: 18001, NodePath: "/v1/chat"},│
│         {Address: "192.168.1.101", Port: 18002, NodePath: "/v1/chat"} │
│       ],                                                               │
│       "gpt-4o-mini": [                                                │
│         {Address: "192.168.1.100", Port: 18001, NodePath: "/v1/chat"} │
│       ]                                                                │
│     }                                                                  │
│                                                                         │
└─────────────────────────────────────────────────────────────────────────┘
```

### 6.2 请求处理

#### 6.2.1 Chat 请求处理

```go
// POST /v1/chat/completions
func (p *Plugin) handleChat(w http.ResponseWriter, r *http.Request) {
    // 1. 解析请求体
    var req struct {
        Model     string `json:"model"`
        SessionID string `json:"session_id"`
    }
    json.NewDecoder(r.Body).Decode(&req)

    // 2. 选择后端
    backend, err := p.selectBackend(req.Model, req.SessionID)
    if err != nil {
        http.Error(w, err.Error(), http.StatusNotFound)
        return
    }

    // 3. 增加连接计数
    atomic.AddInt32(&backend.ActiveConn, 1)
    defer atomic.AddInt32(&backend.ActiveConn, -1)

    // 4. 转发请求到后端
    url := fmt.Sprintf("http://%s:%d%s", backend.Address, backend.Port, backend.NodePath)
    resp, err := p.forwardRequest(url, r)

    if err != nil {
        // 故障转移逻辑
        if p.tryFailover(req.Model, req.SessionID, backend) {
            return
        }
        http.Error(w, "backend error", http.StatusBadGateway)
        return
    }

    // 5. 更新会话活动时间
    if req.SessionID != "" {
        p.updateSessionActivity(req.SessionID)
    }

    // 6. 返回响应
    p.writeResponse(w, resp)
}
```

#### 6.2.2 Models 请求处理

```go
// GET /v1/models
func (p *Plugin) handleModels(w http.ResponseWriter, r *http.Request) {
    p.mu.RLock()
    defer p.mu.RUnlock()

    // 聚合所有后端的 /v1/models
    allModels := make([]ModelInfo, 0)
    seen := make(map[string]bool)

    for _, mb := range p.modelToBackends {
        for _, backend := range mb.Backends {
            models := p.fetchBackendModels(backend)
            for _, m := range models {
                if !seen[m.ID] {
                    seen[m.ID] = true
                    allModels = append(allModels, m)
                }
            }
        }
    }

    // 返回聚合结果
    json.NewEncoder(w).Encode(ModelsResponse{
        Data: allModels,
    })
}
```

### 6.3 负载均衡

#### 6.3.1 选择后端

```go
func (p *Plugin) selectBackend(model, sessionID string) (*Backend, error) {
    mb, ok := p.modelToBackends[model]
    if !ok {
        return nil, fmt.Errorf("model %s not found", model)
    }

    // 1. 有 session_id，尝试使用绑定后端
    if sessionID != "" {
        if session, bound := p.getSessionBackend(sessionID); bound {
            if time.Since(session.LastActivity) <= p.config.SessionTimeout {
                // 查找对应的后端
                backend := p.findBackend(session.BackendAddr, session.BackendPort, mb)
                if backend != nil && backend.Healthy {
                    return backend, nil
                }
                // 后端不健康，解绑
                p.unbindSession(sessionID)
            } else {
                // 超时，解绑
                p.unbindSession(sessionID)
            }
        }
    }

    // 2. 执行负载均衡
    backend := p.loadBalance(mb)
    if backend == nil {
        return nil, fmt.Errorf("no healthy backend")
    }

    // 3. 绑定会话
    if sessionID != "" {
        p.bindSession(sessionID, backend)
    }

    return backend, nil
}
```

#### 6.3.2 负载均衡算法

```go
// 轮询
func (p *Plugin) selectRoundRobin(backends []*Backend) *Backend {
    idx := atomic.AddInt32(&p.rrIndex, 1)
    return backends[idx%int32(len(backends))]
}

// 最少连接
func (p *Plugin) selectLeastConn(backends []*Backend) *Backend {
    var selected *Backend
    minConn := int32(1<<31 - 1)
    for _, b := range backends {
        if b.Healthy && b.ActiveConn < minConn {
            selected = b
            minConn = b.ActiveConn
        }
    }
    return selected
}

// 最快响应
func (p *Plugin) selectFastest(backends []*Backend) *Backend {
    var selected *Backend
    minLatency := int64(1<<63 - 1)
    for _, b := range backends {
        if b.Healthy && b.AvgLatency > 0 && b.AvgLatency < minLatency {
            selected = b
            minLatency = b.AvgLatency
        }
    }
    if selected == nil {
        return p.selectRoundRobin(backends)
    }
    return selected
}
```

### 6.4 会话亲和性

#### 6.4.1 绑定会话

```go
func (p *Plugin) bindSession(sessionID string, backend *Backend) {
    p.mu.Lock()
    defer p.mu.Unlock()

    // 绑定时占用 1 个连接配额
    atomic.AddInt32(&backend.ActiveConn, 1)

    p.sessionToBackend[sessionID] = &SessionInfo{
        SessionID:    sessionID,
        BackendAddr: backend.Address,
        BackendPort: backend.Port,
        LastActivity: time.Now(),
    }
}
```

#### 6.4.2 解绑会话

```go
func (p *Plugin) unbindSession(sessionID string) {
    p.mu.Lock()
    defer p.mu.Unlock()

    if session, ok := p.sessionToBackend[sessionID]; ok {
        // 释放连接配额
        for _, mb := range p.modelToBackends {
            for _, b := range mb.Backends {
                if b.Address == session.BackendAddr && b.Port == session.BackendPort {
                    atomic.AddInt32(&b.ActiveConn, -1)
                    break
                }
            }
        }
    }
    delete(p.sessionToBackend, sessionID)
}
```

#### 6.4.3 超时清理

```go
func (p *Plugin) cleanupExpiredSessions() {
    ticker := time.NewTicker(1 * time.Minute)
    defer ticker.Stop()

    for range ticker.C {
        now := time.Now()
        p.mu.Lock()
        for sessionID, session := range p.sessionToBackend {
            if now.Sub(session.LastActivity) > p.config.SessionTimeout {
                // 释放连接配额
                for _, mb := range p.modelToBackends {
                    for _, b := range mb.Backends {
                        if b.Address == session.BackendAddr && b.Port == session.BackendPort {
                            atomic.AddInt32(&b.ActiveConn, -1)
                            break
                        }
                    }
                }
                delete(p.sessionToBackend, sessionID)
            }
        }
        p.mu.Unlock()
    }
}
```

### 6.5 故障转移

```go
func (p *Plugin) tryFailover(model, sessionID string, failedBackend *Backend) bool {
    mb, ok := p.modelToBackends[model]
    if !ok {
        return false
    }

    // 过滤掉失败的后端
    candidates := make([]*Backend, 0)
    for _, b := range mb.Backends {
        if b != failedBackend && b.Healthy {
            candidates = append(candidates, b)
        }
    }

    if len(candidates) == 0 {
        return false
    }

    // 选择新后端
    newBackend := p.loadBalanceFromList(candidates)

    // 如果有 session_id，重新绑定
    if sessionID != "" {
        p.unbindSession(sessionID)
        p.bindSession(sessionID, newBackend)
    }

    // 重试请求
    return p.retryRequest(newBackend, model, sessionID)
}
```

## 7. 主从选举

### 7.1 选举机制

采用类 Raft 算法的简化版选举：

```
┌─────────────────────────────────────────────────────────────────────────┐
│                           选举状态机                                     │
├─────────────────────────────────────────────────────────────────────────┤
│                                                                         │
│   ┌─────────┐   启动/主节点下线   ┌─────────┐                        │
│   │ Follower │ ─────────────────▶ │ Candidate │                       │
│   └─────────┘                    └────┬────┘                        │
│        ▲                               │                              │
│        │                               │ 发起选举                    │
│        │ 收到主节点心跳                ▼                              │
│        │                        ┌─────────┐                        │
│        └───────────────────────│  Leader  │                        │
│             投票给对方         └─────────┘                        │
│                                                                         │
└─────────────────────────────────────────────────────────────────────────┘
```

### 7.2 选举消息格式

```json
{
  "type": "election",
  "action": "vote",
  "term": 5,
  "candidate_id": "abc123def456",
  "from": "def456ghi789"
}
```

```json
{
  "type": "election",
  "action": "victory",
  "term": 5,
  "master_id": "abc123def456"
}
```

```json
{
  "type": "heartbeat",
  "master_id": "abc123def456",
  "timestamp": 1234567890
}
```

### 7.3 选举流程

```go
func (p *Plugin) startElection() {
    p.mu.Lock()
    p.term++
    p.isCandidate = true
    candidateID := p.config.ServiceID
    p.mu.Unlock()

    // 获取所有插件
    peers := p.getPeers()
    voteCount := 1  // 自己的一票

    // 向所有插件发送选举请求
    for _, peer := range peers {
        if peer == p.config.ServiceID {
            continue
        }

        response := p.sendVoteRequest(peer, p.term, candidateID)
        if response.Voted {
            voteCount++
        }
    }

    // 获得多数票，成为主节点
    if voteCount > len(peers)/2 {
        p.becomeMaster()
    } else {
        // 选举失败，等待随机时间后重试
        p.isCandidate = false
        time.Sleep(time.Duration(rand.Intn(1000)) * time.Millisecond)
        go p.startElection()
    }
}

func (p *Plugin) becomeMaster() {
    p.mu.Lock()
    p.isMaster = true
    p.masterID = p.config.ServiceID
    p.isCandidate = false
    p.mu.Unlock()

    // 广播胜利消息
    p.broadcast(map[string]interface{}{
        "type":      "election",
        "action":    "victory",
        "term":      p.term,
        "master_id": p.config.ServiceID,
    })

    // 启动心跳
    go p.sendHeartbeat()
}
```

### 7.4 心跳机制

```go
func (p *Plugin) sendHeartbeat() {
    ticker := time.NewTicker(p.config.HeartbeatInterval)
    defer ticker.Stop()

    for {
        select {
        case <-ticker.C:
            if !p.isMaster {
                return
            }

            // 广播心跳
            p.broadcast(map[string]interface{}{
                "type":       "heartbeat",
                "master_id":  p.config.ServiceID,
                "timestamp":  time.Now().Unix(),
            })
        }
    }
}

func (p *Plugin) monitorHeartbeat() {
    lastHeartbeat := time.Now()

    ticker := time.NewTicker(p.config.ElectionTimeout)
    defer ticker.Stop()

    for {
        select {
        case <-ticker.C:
            p.mu.RLock()
            isMaster := p.isMaster
            p.mu.RUnlock()

            if isMaster {
                lastHeartbeat = time.Now()
            } else {
                // 检查主节点心跳超时
                if time.Since(lastHeartbeat) > p.config.ElectionTimeout {
                    // 发起新选举
                    go p.startElection()
                }
            }
        }
    }
}
```

### 7.5 消息处理

```go
func (p *Plugin) handleMessage(msg map[string]interface{}) {
    msgType, _ := msg["type"].(string)

    switch msgType {
    case "election":
        action, _ := msg["action"].(string)
        switch action {
        case "vote":
            p.handleVoteRequest(msg)
        case "victory":
            p.handleVictory(msg)
        }
    case "heartbeat":
        p.handleHeartbeat(msg)
    case "session_sync":
        p.handleSessionSync(msg)
    }
}

func (p *Plugin) handleVoteRequest(msg map[string]interface{}) {
    term, _ := msg["term"].(int64)
    candidateID, _ := msg["candidate_id"].(string)

    p.mu.Lock()
    defer p.mu.Unlock()

    if term > p.term {
        p.term = term

        // 投票给请求者
        p.sendTo(candidateID, map[string]interface{}{
            "type":     "election",
            "action":   "vote_response",
            "term":     p.term,
            "voted":    true,
        })
    }
}

func (p *Plugin) handleVictory(msg map[string]interface{}) {
    masterID, _ := msg["master_id"].(string)
    term, _ := msg["term"].(int64)

    p.mu.Lock()
    defer p.mu.Unlock()

    if term >= p.term {
        p.isMaster = false
        p.masterID = masterID
        p.term = term
    }
}

func (p *Plugin) handleHeartbeat(msg map[string]interface{}) {
    masterID, _ := msg["master_id"].(string)

    p.mu.Lock()
    p.masterID = masterID
    p.mu.Unlock()
}
```

## 8. 主从数据同步

### 8.1 同步方式

主节点处理请求后，需要将会话绑定信息同步到从节点：

```go
func (p *Plugin) syncSession(sessionID string, backend *Backend) {
    if !p.isMaster {
        return
    }

    // 广播会话绑定信息
    p.broadcast(map[string]interface{}{
        "type": "session_sync",
        "action": "bind",
        "session": map[string]interface{}{
            "session_id":    sessionID,
            "backend_addr":  backend.Address,
            "backend_port":  backend.Port,
            "last_activity": time.Now().Unix(),
        },
    })
}
```

### 8.2 从节点接收同步

```go
func (p *Plugin) handleSessionSync(msg map[string]interface{}) {
    action, _ := msg["action"].(string)
    session, _ := msg["session"].(map[string]interface{})

    p.mu.Lock()
    defer p.mu.Unlock()

    switch action {
    case "bind":
        sessionID, _ := session["session_id"].(string)
        addr, _ := session["backend_addr"].(string)
        port, _ := session["backend_port"].(int)
        lastAct, _ := session["last_activity"].(int64)

        p.sessionToBackend[sessionID] = &SessionInfo{
            SessionID:    sessionID,
            BackendAddr:  addr,
            BackendPort:  port,
            LastActivity: time.Unix(lastAct, 0),
        }

    case "unbind":
        sessionID, _ := session["session_id"].(string)
        delete(p.sessionToBackend, sessionID)
    }
}
```

## 9. API 端点

### 9.1 业务端点

| 端点 | 方法 | 说明 |
|------|------|------|
| `/v1/chat/completions` | POST | Chat 路由 |
| `/v1/models` | GET | 模型列表聚合 |
| `/v1/plugin/receive` | POST | 接收其他插件的消息（内部使用） |

### 9.2 管理端点

| 端点 | 方法 | 说明 |
|------|------|------|
| `/health` | GET | 健康检查 |
| `/v1/status` | GET | 获取插件状态（主从、session 数等） |

## 10. 配置参数

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `--router-addr` | `localhost:15049` | Nova Router 地址 |
| `--listen-addr` | `:18011` | 插件监听地址 |
| `--session-timeout` | `10m` | 会话超时时间 |
| `--lb-algorithm` | `least_conn` | 负载均衡算法 |
| `--health-check-interval` | `10s` | 健康检查间隔 |
| `--election-timeout` | `3s` | 选举超时 |
| `--heartbeat-interval` | `1s` | 主节点心跳间隔 |
| `--sync-interval` | `30s` | 集群信息同步间隔 |

## 11. 完整工作流程

```
┌─────────────────────────────────────────────────────────────────────────┐
│                         插件启动流程                                     │
├─────────────────────────────────────────────────────────────────────────┤
│                                                                         │
│  1. 加载配置                                                           │
│                                                                         │
│  2. 注册端点到 Nova Router                                             │
│     POST /v1/endpoints                                                 │
│     → 获取 service_id                                                  │
│                                                                         │
│  3. 启动 HTTP 服务                                                     │
│     - 监听 /v1/chat/completions                                       │
│     - 监听 /v1/models                                                 │
│     - 监听 /v1/plugin/receive                                         │
│                                                                         │
│  4. 同步集群信息                                                       │
│     GET /v1/global                                                     │
│     GET /v1/node/{node_id}                                            │
│     → 构建 modelToBackends                                            │
│                                                                         │
│  5. 获取同类型插件                                                     │
│     POST /v1/plugin/peers                                              │
│     → 获取 peers 列表                                                  │
│                                                                         │
│  6. 参与选举                                                           │
│     - 如果没有主节点，发起选举                                          │
│     - 否则，等待主节点心跳                                             │
│                                                                         │
│  7. 启动后台任务                                                       │
│     - 心跳发送                                                         │
│     - 心跳监控                                                         │
│     - 会话清理                                                         │
│     - 集群信息同步                                                    │
│                                                                         │
└─────────────────────────────────────────────────────────────────────────┘
```

## 12. 请求流程

```
┌─────────────────────────────────────────────────────────────────────────┐
│                       Chat 请求流程                                      │
├─────────────────────────────────────────────────────────────────────────┤
│                                                                         │
│  客户端                                                                │
│     │                                                                  │
│     │ POST /v1/chat/completions                                      │
│     ▼                                                                  │
│  Nova Router                                                           │
│     │                                                                  │
│     │ 匹配到插件端点 (plugin: true)                                  │
│     ▼                                                                  │
│  Model Router 插件 (主节点)                                           │
│     │                                                                  │
│     ├─ 1. 解析 model, session_id                                     │
│     │                                                                  │
│     ├─ 2. 选择后端                                                    │
│     │     ├─ 有 session_id? → 使用绑定后端                           │
│     │     └─ 无 session_id? → 负载均衡选择                            │
│     │                                                                  │
│     ├─ 3. 转发请求                                                    │
│     │     POST http://backend:port/v1/chat/completions              │
│     │                                                                  │
│     ├─ 4. 处理响应                                                     │
│     │     ├─ 成功 → 更新会话活动时间                                  │
│     │     └─ 失败 → 故障转移                                         │
│     │                                                                  │
│     └─ 5. 同步会话绑定到从节点                                        │
│           POST /v1/plugin/broadcast                                  │
│                                                                         │
│     ▼                                                                  │
│  客户端 (AI 响应)                                                      │
│                                                                         │
└─────────────────────────────────────────────────────────────────────────┘
```

## 13. 注意事项

1. **插件标识**：所有 Model Router 插件使用相同的 `description`（如 "router"），这样 `/v1/plugin/peers` 才能找到同类型的插件

2. **主从职责**：
   - **主节点**：处理所有请求，维护完整数据，广播会话绑定
   - **从节点**：接收请求时转发到主节点，接收同步数据

3. **服务发现**：插件通过 Nova Router 的 Gossip 协议间接发现后端服务，不需要直接感知后端服务的注册

4. **向后兼容**：即使没有其他插件实例，插件也能独立工作（自动成为主节点）

5. **错误处理**：
   - 后端不可用时尝试故障转移
   - 所有后端不可用时返回 503
   - 主节点不可用时从节点发起选举

## 14. 示例请求

### 14.1 Chat 请求

```bash
curl -X POST http://localhost:15050/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-4o",
    "messages": [{"role": "user", "content": "Hello"}],
    "session_id": "user-123-conversation-456"
  }'
```

### 14.2 Models 请求

```bash
curl http://localhost:15050/v1/models
```

响应：
```json
{
  "data": [
    {
      "id": "gpt-4o",
      "object": "model",
      "created": 1677610602,
      "owned_by": "node-a,node-b"
    },
    {
      "id": "gpt-4o-mini",
      "object": "model",
      "created": 1677610602,
      "owned_by": "node-a"
    },
    {
      "id": "claude-3-opus",
      "object": "model",
      "created": 1699900000,
      "owned_by": "node-c"
    }
  ]
}
```

## 15. 总结

Model Router 插件是一个完整的分布式路由解决方案，具有以下特点：

- **智能化路由**：根据 model 参数精确路由
- **高可用性**：主从选举 + 故障转移
- **性能优化**：负载均衡 + 会话亲和性（利用上下文缓存）
- **易于扩展**：基于 Nova Router 的插件规范，可无缝集成
