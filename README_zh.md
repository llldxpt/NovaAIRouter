# NovaAI Gateway Router

Go: 1.20+ | 协议: Apache 2.0 | 版本: v1.0.0 | 版权所有 2026 firstarpc.com

NovaAI Gateway Router 是一个去中心化的分布式 AI 推理网关集群系统，专为 AI 推理服务设计。提供基于 Gossip 协议的服务自动发现、灵活的路径匹配路由、多负载均衡算法（轮询、最少连接、最快响应）以及基于心跳检测的自动故障转移。

## 特性

- **去中心化架构**：无单点故障，节点平等通信
- **Gossip 协议**：节点自动发现，状态自动同步
- **服务注册**：支持灵活的路径匹配规则
- **健康检测**：心跳监控，自动故障转移
- **负载均衡**：多种算法（轮询、最少连接、最快响应）
- **插件系统**：可扩展的插件架构（如 Model Router 插件）
- **Prometheus 指标**：内置 `/metrics` 端点监控
- **热更新**：配置变更无需停机

## 架构

```
+---------------------------------------------------------------------------------------+
|                           NovaAI Gateway Cluster                                      |
|                                                                                       |
|   +---------------------------------------+                                           |
|   |         Gossip Protocol               |                                           |
|   |    (Cluster Internal Communication)   |                                           |
|   +---------------------------------------+                                           |
|                    |                                                                   |
|    +--------------+--------------+---------------+                                   |
|    |              |              |               |                                   |
|    v              v              v               v                                    |
| +--------+    +--------+    +--------+     +--------+                               |
| | Node-A |    | Node-B |    | Node-C |     | Node-N |                               |
| | :15050 |    | :15050 |    | :15050 |     | :15050 |                               |
| +--------+    +--------+    +--------+     +--------+                               |
|      |              |              |              |                                   |
|      v              v              v              v                                    |
| +------------------------------------------+                                         |
| |          Backend Services                 |                                        |
| |  +--------+  +--------+  +--------+       |                                        |
| |  | GPT-4  |  |Claude |  | Gemini |       |                                        |
| |  | :18001 |  | :18002 |  | :18003 |       |                                        |
| |  +--------+  +--------+  +--------+       |                                        |
| +------------------------------------------+                                         |
+---------------------------------------------------------------------------------------+
```

## 快速开始

### 前置要求

- Go 1.20 或更高版本
- Windows / Linux / macOS

### 构建

```bash
# 克隆仓库
git clone https://github.com/llldxpt/NovaAIRouter.git
cd NovaAIRouter

# 构建二进制文件
go build -o novaairouter ./cmd/gateway
```

### 运行

```bash
# 启动单节点（默认端口）
./novaairouter

# 自定义节点 ID
./novaairouter --node-id=node-1

# 自定义端口
./novaairouter --listen-addr=:15050

# 强制运行（杀掉占用端口的进程）
./novaairouter --force
```

### Docker

```bash
# 构建 Docker 镜像
docker build -t novaairouter:latest .

# 运行容器
docker run -d -p 15049:15049 -p 15050:15050 novaairouter:latest
```

## 配置

### 命令行参数

| 参数 | 默认值 | 描述 |
|------|--------|------|
| `--node-id` | 随机 UUID | 唯一节点标识符 |
| `--listen-addr` | `:15050` | 业务服务器监听地址 |
| `--log-level` | `info` | 日志级别 (debug, info, warn, error) |
| `--default-max-concurrency` | `100` | 每个端点默认最大并发数 |
| `--queue-capacity` | `1000` | 请求队列容量 |
| `--backend-timeout` | `30s` | 后端请求超时时间 |
| `--heartbeat-timeout` | `15s` | 端点心跳超时时间 |
| `--gossip-interval` | `3s` | Gossip 同步间隔 |
| `--config` | - | 配置文件路径 |

### 端口配置

每个节点使用 4 个端口：

| 端口 | 服务 | 描述 |
|------|------|------|
| `N` | 业务 | 主路由服务（默认 15050） |
| `N-1` | 管理 | 管理 API（默认 15049） |
| `N+1` | Gossip | 节点间通信 |
| `N+2` | UDP 发现 | UDP 广播节点发现 |

## API 端点

### 管理 API（端口 15049）

| 端点 | 方法 | 描述 |
|------|------|------|
| `/v1/endpoints` | POST | 注册端点 |
| `/v1/endpoints` | GET | 列出本地端点 |
| `/v1/endpoints` | DELETE | 删除端点 |
| `/v1/heartbeat` | POST | 发送心跳 |
| `/v1/nodes` | GET | 列出集群节点 |
| `/v1/local` | GET | 获取本地节点信息 |
| `/v1/global` | GET | 获取所有节点信息 |
| `/v1/node/{id}` | GET | 获取指定节点信息 |
| `/v1/plugin/peers` | POST | 获取插件节点 |
| `/v1/plugin/send` | POST | 发送消息到插件 |
| `/v1/plugin/broadcast` | POST | 广播到插件 |
| `/metrics` | GET | Prometheus 指标 |
| `/health` | GET | 健康检查 |

### 业务 API（端口 15050）

根据路径匹配规则将请求转发到已注册的后端服务。

## 服务注册

### 注册端点

```bash
curl -X POST http://127.0.0.1:15049/v1/endpoints \
  -H "Content-Type: application/json" \
  -d '[
    {
      "service_path": "18000/v1/",
      "node_path": "/v1/",
      "max_concurrent": 100,
      "description": "OpenAI API"
    }
  ]'
```

### 发送心跳

```bash
curl -X POST http://127.0.0.1:15049/v1/heartbeat \
  -H "Content-Type: application/json" \
  -d '{
    "service_id": "your-service-id",
    "healthy": true
  }'
```

### 路径匹配规则

- **精确匹配**：`node_path` 不带尾部斜杠 - 精确匹配
- **前缀匹配**：`node_path` 带尾部斜杠 - 匹配所有子路径

## 插件系统

NovaAI Gateway 支持插件扩展功能。参见 [MODEL_ROUTER_PLUGIN.md](docs/MODEL_ROUTER_PLUGIN.md) 了解 Model Router 插件实现。

### 插件注册

```bash
curl -X POST http://127.0.0.1:15049/v1/endpoints \
  -H "Content-Type: application/json" \
  -d '[
    {
      "node_path": "/v1/chat/completions",
      "service_path": ":18011/v1/chat/completions",
      "plugin": true,
      "description": "router"
    }
  ]'
```

## 负载均衡

支持多种负载均衡算法：

- **轮询 (Round Robin)**：顺序分配
- **最少连接 (Least Connections)**：路由到活动连接数最少的服务器
- **最快响应 (Fastest Response)**：路由到延迟最低的服务器

## 指标

Prometheus 指标暴露在 `/metrics`：

```bash
# 查看指标
curl http://127.0.0.1:15049/metrics
```

主要指标：
- `gateway_requests_total`：已处理的请求总数
- `gateway_request_duration_seconds`：请求延迟
- `gateway_endpoint_active`：活跃端点
- `gateway_cluster_nodes`：集群节点数
- `gateway_pool_connections`：连接池状态

## 开发

### 项目结构

```
novaairouter/
├── cmd/
│   └── gateway/           # 主程序入口
├── internal/
│   ├── admin/            # 管理 API 服务器
│   ├── business/         # 业务逻辑（路由、负载均衡、转发）
│   ├── config/           # 配置管理
│   ├── gossip/           # Gossip 协议实现
│   ├── metrics/          # Prometheus 指标
│   ├── pool/             # 连接池
│   ├── registry/         # 服务注册表
│   └── models/           # 数据模型
├── docs/                 # 文档
└── tests/                # 测试文件
```

### 运行测试

```bash
# 运行所有测试
go test ./...

# 运行单元测试
go test ./tests/unit/...

# 运行集成测试
go test ./tests/integration/...
```

## 文档

- [服务注册指南](docs/REGISTRATION.md)
- [Model Router 插件](docs/MODEL_ROUTER_PLUGIN.md)

## 许可证

Apache License 2.0 - 参见 [LICENSE](LICENSE) 了解详情。

## 致谢

- [memberlist](https://github.com/hashicorp/memberlist) - Gossip 协议实现
- [zerolog](https://github.com/rs/zerolog) - 日志
- [cobra](https://github.com/spf13/cobra) - CLI 框架
- [viper](https://github.com/spf13/viper) - 配置管理
- [prometheus](https://github.com/prometheus/client_golang) - 指标
