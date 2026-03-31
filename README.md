# NovaAI Gateway Router

Go: 1.20+ | License: MIT | Version: v1.0.0

A decentralized, distributed AI Gateway cluster system designed for AI inference services. NovaAI Gateway provides service registration, automatic routing, health detection, load balancing, and distributed node synchronization via the Gossip protocol.

## Features

- **Decentralized Architecture**: No single point of failure with peer-to-peer node communication
- **Gossip Protocol**: Automatic node discovery and state synchronization across the cluster
- **Service Registration**: Register backend services with flexible path matching rules
- **Health Detection**: Automatic heartbeat monitoring with automatic failover
- **Load Balancing**: Multiple algorithms (Round Robin, Least Connections, Fastest Response)
- **Plugin System**: Extensible plugin architecture (e.g., Model Router plugin)
- **Prometheus Metrics**: Built-in observability with `/metrics` endpoint
- **Hot Reload**: Configuration changes without downtime

## Architecture

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
|      |              |              |              |                                   |
|      v              v              v              v                                    |
| +------------------------------------------+                                         |
| |          Backend Services                 |                                        |
| |  +--------+  +--------+  +--------+       |                                        |
|  |  | GPT-4  |  |Claude |  | Gemini |       |                                        |
|  |  | :18001 |  | :18002 |  | :18003 |       |                                        |
|  |  +--------+  +--------+  +--------+       |                                        |
|  +------------------------------------------+                                         |
+---------------------------------------------------------------------------------------+
```

## Quick Start

### Prerequisites

- Go 1.20 or later
- Windows / Linux / macOS

### Build

```bash
# Clone the repository
git clone https://github.com/yourusername/novaairouter.git
cd novaairouter

# Build the binary
go build -o novaairouter ./cmd/gateway
```

### Run

```bash
# Start a single node (default ports)
./novaairouter

# Start with custom node ID
./novaairouter --node-id=node-1

# Start with custom ports
./novaairouter --listen-addr=:15050

# Force run (kill processes using required ports)
./novaairouter --force
```

### Docker

```bash
# Build Docker image
docker build -t novaairouter:latest .

# Run container
docker run -d -p 15049:15049 -p 15050:15050 novaairouter:latest
```

## Configuration

### Command Line Options

| Flag | Default | Description |
|------|---------|-------------|
| `--node-id` | Random UUID | Unique node identifier |
| `--listen-addr` | `:15050` | Business server listen address |
| `--log-level` | `info` | Log level (debug, info, warn, error) |
| `--default-max-concurrency` | `100` | Default max concurrent requests per endpoint |
| `--queue-capacity` | `1000` | Request queue capacity |
| `--backend-timeout` | `30s` | Backend request timeout |
| `--heartbeat-timeout` | `15s` | Endpoint heartbeat timeout |
| `--gossip-interval` | `3s` | Gossip sync interval |
| `--config` | - | Path to config file |

### Port Configuration

Each node uses 4 ports:

| Port | Service | Description |
|------|---------|-------------|
| `N` | Business | Main routing service (default 15050) |
| `N-1` | Admin | Admin API and management (default 15049) |
| `N+1` | Gossip | Node-to-node communication |
| `N+2` | UDP Discovery | UDP broadcast for node discovery |

## API Endpoints

### Admin API (Port 15049)

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/v1/endpoints` | POST | Register endpoints |
| `/v1/endpoints` | GET | List local endpoints |
| `/v1/endpoints` | DELETE | Remove endpoints |
| `/v1/heartbeat` | POST | Send heartbeat |
| `/v1/nodes` | GET | List cluster nodes |
| `/v1/local` | GET | Get local node info |
| `/v1/global` | GET | Get all nodes info |
| `/v1/node/{id}` | GET | Get specific node info |
| `/v1/plugin/peers` | POST | Get plugin peers |
| `/v1/plugin/send` | POST | Send message to plugin |
| `/v1/plugin/broadcast` | POST | Broadcast to plugins |
| `/metrics` | GET | Prometheus metrics |
| `/health` | GET | Health check |

### Business API (Port 15050)

Forward requests to registered backend services based on path matching rules.

## Service Registration

### Register an Endpoint

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

### Send Heartbeat

```bash
curl -X POST http://127.0.0.1:15049/v1/heartbeat \
  -H "Content-Type: application/json" \
  -d '{
    "service_id": "your-service-id",
    "healthy": true
  }'
```

### Path Matching Rules

- **Exact Match**: `node_path` without trailing `/` - matches exactly
- **Prefix Match**: `node_path` with trailing `/` - matches all subpaths

## Plugin System

NovaAI Gateway supports plugins for extended functionality. See [MODEL_ROUTER_PLUGIN.md](docs/MODEL_ROUTER_PLUGIN.md) for the Model Router plugin implementation.

### Plugin Registration

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

## Load Balancing

Supports multiple load balancing algorithms:

- **Round Robin**: Sequential distribution
- **Least Connections**: Route to server with fewest active connections
- **Fastest Response**: Route to server with lowest latency

## Metrics

Prometheus metrics are exposed at `/metrics`:

```bash
# View metrics
curl http://127.0.0.1:15049/metrics
```

Key metrics:
- `gateway_requests_total`: Total requests processed
- `gateway_request_duration_seconds`: Request latency
- `gateway_endpoint_active`: Active endpoints
- `gateway_cluster_nodes`: Cluster node count
- `gateway_pool_connections`: Connection pool status

## Development

### Project Structure

```
novaairouter/
├── cmd/
│   └── gateway/           # Main application entry
├── internal/
│   ├── admin/            # Admin API server
│   ├── business/         # Business logic (router, balancer, forwarder)
│   ├── config/           # Configuration management
│   ├── gossip/           # Gossip protocol implementation
│   ├── metrics/          # Prometheus metrics
│   ├── pool/             # Connection pool
│   ├── registry/         # Service registry
│   └── models/           # Data models
├── docs/                 # Documentation
└── tests/                # Test files
```

### Running Tests

```bash
# Run all tests
go test ./...

# Run unit tests
go test ./tests/unit/...

# Run integration tests
go test ./tests/integration/...
```

## Documentation

- [Service Registration Guide](docs/REGISTRATION.md)
- [Model Router Plugin](docs/MODEL_ROUTER_PLUGIN.md)

## License

MIT License - see [LICENSE](LICENSE) for details.

## Contributing

Contributions are welcome! Please read our [Contributing Guidelines](CONTRIBUTING.md) before submitting PRs.

## Acknowledgments

- [memberlist](https://github.com/hashicorp/memberlist) - Gossip protocol implementation
- [zerolog](https://github.com/rs/zerolog) - Logging
- [cobra](https://github.com/spf13/cobra) - CLI framework
- [viper](https://github.com/spf13/viper) - Configuration management
- [prometheus](https://github.com/prometheus/client_golang) - Metrics
