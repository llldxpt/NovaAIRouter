# NovaAirouter 服务注册指南

## 概述

NovaAirouter 是一个分布式服务网关，支持服务注册、自动路由、健康检测和负载均衡。

## 基础信息

| 配置项 | 值 |
|--------|-----|
| Admin API 端口 | 15049 |
| 业务路由端口 | 15050 |
| 节点发现协议 | Gossip |

## 端点注册

### 注册端点

**请求**

```http
POST http://<节点IP>:15049/v1/endpoints
Content-Type: application/json

[
    {
        "service_path": "端口号/后端路径",
        "node_path": "注册到网关的路径",
        "max_concurrent": 100,
        "plugin": false
    }
]
```

### service_path 和 node_path 格式说明

现在使用两个独立的字段来分别指定：

- **service_path**：后端服务的端口和路径（格式：`端口号/后端路径`）
- **node_path**：注册到网关的路径（用于路由匹配）

#### 核心规则

- **service_path** 和 **node_path** 的最后一个字符必须同步
- 如果 service_path 以 `/` 结尾，node_path 也必须以 `/` 结尾
- 如果 service_path 不以 `/` 结尾，node_path 也不能以 `/` 结尾
- 如果不匹配，注册时返回错误

#### 情况1：精确匹配（service_path 和 node_path 都不以 `/` 结尾）

| 字段            | 值       |
| ------------- | ------- |
| service_path | `18000` |
| node_path    | `/v1`   |

| 用户请求      | 是否匹配   | 转发到               |
| --------- | ------ | ----------------- |
| `/v1`     | ✓ 完全相等 | `localhost:18000` |
| `/v1/api` | ✗ 不相等  | 不转发               |

**示例：**

| 字段            | 值      |
| ------------- | ------ |
| service_path | `8080` |
| node_path    | `/api` |

| 用户请求        | 是否匹配   | 转发到              |
| ----------- | ------ | ---------------- |
| `/api`      | ✓ 完全相等 | `localhost:8080` |
| `/apix`     | ✗ 不相等  | 不转发              |
| `/api/user` | ✗ 不相等  | 不转发              |

#### 情况2：前缀匹配（service_path 和 node_path 都以 `/` 结尾）

| 字段            | 值        |
| ------------- | -------- |
| service_path | `18000/` |
| node_path    | `/v1/`   |

| 用户请求           | 去掉 /v1/ 后  | 转发到                        |
| -------------- | ---------- | -------------------------- |
| `/v1/test`     | `test`     | `localhost:18000/test`     |
| `/v1/api/user` | `api/user` | `localhost:18000/api/user` |
| `/v1/`         | `` (空)   | `localhost:18000/`         |
| `/v2/test`     | ✗ 不匹配      | 不转发                        |

**示例：service_path 有端口+路径**

| 字段            | 值           |
| ------------- | ----------- |
| service_path | `18000/v1/` |
| node_path    | `/v2/`      |

| 用户请求           | 去掉 /v2/ 后  | 转发到                           |
| -------------- | ---------- | ----------------------------- |
| `/v2`          | `` (空)   | `localhost:18000/v1`          |
| `/v2/`         | `` (空)   | `localhost:18000/v1/`         |
| `/v2/test`     | `test`     | `localhost:18000/v1/test`     |
| `/v2/api/user` | `api/user` | `localhost:18000/v1/api/user` |
| `/v3/test`     | ✗ 不匹配      | 不转发                           |

### 错误处理

注册时检查：

- 如果 service_path 最后一个字符是 `/`，但 node_path 最后一个字符不是 `/` → 返回错误
- 如果 service_path 最后一个字符不是 `/`，但 node_path 最后一个字符是 `/` → 返回错误

**错误示例1：**

| 字段            | 值        | 结果       |
| ------------- | -------- | -------- |
| service_path | `18000/` | 错误：结尾不匹配 |
| node_path    | `/v1`    | <br />   |

**错误示例2：**

| 字段            | 值       | 结果       |
| ------------- | ------- | -------- |
| service_path | `18000` | 错误：结尾不匹配 |
| node_path    | `/v1/`  | <br />   |

### 参数说明

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| service_path | string | 是 | 格式：`端口号/后端路径`，如 `18000` 或 `18000/v1/` |
| node_path | string | 是 | 注册到网关的路径，如 `/v1` 或 `/v1/` |
| max_concurrent | int | 否 | 最大并发数，默认 100，该端点同时处理的最大请求数量 |
| local_only | bool | 否 | 是否仅本地注册不同步到集群，默认为 false（会同步到其他节点） |
| description | string | 否 | 端点描述信息，用于记录端点的用途或说明 |
| plugin | bool | 否 | 是否为插件，默认为 false |

#### max_concurrent 参数说明

- `max_concurrent`：指定该端点同时处理的最大请求数量
- 默认值为 100
- 当并发请求数超过此值时，新的请求会被拒绝或排队等待
- 该值用于后端的负载均衡，确保请求不会超出后端服务的处理能力

#### local_only 参数说明

- `local_only: false`（默认）：端点会同步到集群所有节点，其他节点也能路由到这个端点
- `local_only: true`：端点仅注册在当前节点，不会同步到集群其他节点

### 示例

```bash
# 注册端点（精确匹配）
curl -X POST http://127.0.0.1:15049/v1/endpoints \
  -H "Content-Type: application/json" \
  -d '[{"service_path": "18000", "node_path": "/v1", "max_concurrent": 100}]'

# 注册端点（前缀匹配）
curl -X POST http://127.0.0.1:15049/v1/endpoints \
  -H "Content-Type: application/json" \
  -d '[{"service_path": "18000/v1/", "node_path": "/v2/", "max_concurrent": 100}]'

# 注册仅本地端点（不同步到集群）
curl -X POST http://127.0.0.1:15049/v1/endpoints \
  -H "Content-Type: application/json" \
  -d '[{"service_path": "18000", "node_path": "/local-only", "local_only": true}]'

# 批量注册
curl -X POST http://127.0.0.1:15049/v1/endpoints \
  -H "Content-Type: application/json" \
  -d '[
    {"service_path": "18000", "node_path": "/user", "max_concurrent": 100},
    {"service_path": "18001", "node_path": "/order", "max_concurrent": 50},
    {"service_path": "18002/", "node_path": "/product/", "max_concurrent": 200}
  ]'
```

## 心跳检测

### 发送心跳

服务需要定期发送心跳以保持健康状态：

```bash
curl -X POST http://127.0.0.1:15049/v1/heartbeat \
  -H "Content-Type: application/json" \
  -d '{
    "service_id": "注册时返回的service_id",
    "healthy": true
  }'
```

**注意：** 需要在注册端点后的 **5秒内** 发送第一次心跳，否则端点会被标记为不健康。

### 取消注册

如果服务下线，可以删除端点（必须使用 service_id）：

```bash
# 删除该服务注册的所有端点
curl -X DELETE "http://127.0.0.1:15049/v1/endpoints?service_id=<your_service_id>"

# 删除该服务的单个指定端点（path 为注册时的 node_path）
curl -X DELETE "http://127.0.0.1:15049/v1/endpoints?service_id=<your_service_id>&path=/v1"
```

### 修改已注册的端点

如果需要修改已注册服务的端点（例如增加端点），请使用**删除后重新注册**的方式：

```bash
# 1. 先删除该服务注册的所有端点
curl -X DELETE "http://127.0.0.1:15049/v1/endpoints?service_id=<your_service_id>"

# 2. 重新注册服务（包含新的端点列表）
curl -X POST http://127.0.0.1:15049/v1/endpoints \
  -H "Content-Type: application/json" \
  -d '[
    {"service_path": "18000", "node_path": "/v1", "max_concurrent": 100},
    {"service_path": "18001", "node_path": "/v2", "max_concurrent": 50},
    {"service_path": "18002", "node_path": "/v3", "max_concurrent": 30}
  ]'
```

**注意：** 删除端点后，重新注册应立即发送心跳（间隔不超过5秒），否则端点将变为不健康。

## 查询接口

### 查询本节点端点

```bash
curl http://127.0.0.1:15049/v1/endpoints
```

### 查询全局端点（所有节点）

```bash
curl http://127.0.0.1:15049/v1/global
```

### 查询集群节点

```bash
curl http://127.0.0.1:15049/v1/nodes
```

## 路由请求

服务注册后，客户端通过业务端口访问：

```bash
# 请求示例（假设注册了 service_path=18000/v1/, node_path=/v1/）
curl http://<任意节点IP>:15050/v1/user/123

# 转发到后端
# localhost:18000/v1/user/123
```

**负载均衡：** 当多个节点注册了相同路径时，网关会自动进行负载均衡。

## 完整示例

### 1. 启动后端服务

```python
# Python 示例
from http.server import HTTPServer, BaseHTTPRequestHandler

class Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        self.send_response(200)
        self.send_header('Content-Type', 'text/plain')
        self.wfile.write(f"Hello from {self.path}".encode())

server = HTTPServer(('0.0.0.0', 18000), Handler)
server.serve_forever()
```

### 2. 注册端点

```bash
# 精确匹配示例：service_path=18000, node_path=/api
curl -X POST http://127.0.0.1:15049/v1/endpoints \
  -H "Content-Type: application/json" \
  -d '[{"service_path": "18000", "node_path": "/api", "max_concurrent": 100}]'
```

响应：
```json
{
  "service_id": "abc123...",
  "message": "Endpoints registered successfully"
}
```

### 3. 发送心跳

```bash
curl -X POST http://127.0.0.1:15049/v1/heartbeat \
  -H "Content-Type: application/json" \
  -d '{"service_id": "abc123...", "healthy": true}'
```

### 4. 客户端访问

```bash
# 通过网关访问（精确匹配：/api -> localhost:18000/api）
curl http://127.0.0.1:15050/api

# 或通过其他节点访问（分布式路由）
curl http://192.168.30.217:15050/api
```

## 注意事项

1. **注册来源**：端点只能在**本地节点**注册，不能远程注册其他节点的端点
2. **路径格式**：
   - `service_path`：后端服务端口和路径
   - `node_path`：注册到网关的路径
3. **心跳超时**：
   - 超过 15 秒未收到心跳，端点会被标记为 **不健康（unhealthy）**
   - 标记为不健康后再超过 15 秒（共 30 秒），端点会被**自动删除**
4. **分布式同步**：端点会自动同步到集群所有节点
5. **健康检查**：网关不会将请求转发到不健康的端点
6. **路径匹配规则**：
   - 精确匹配：node_path 不以 `/` 结尾，必须完全匹配才转发
   - 前缀匹配：node_path 以 `/` 结尾，匹配该路径下的所有子路径
