# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Commands

```bash
# Build
go build -o novaairouter ./cmd/gateway

# Run (single node, default ports)
./novaairouter

# Run with options
./novaairouter --node-id=node-1 --listen-addr=:15050 --log-level=debug

# Force start (kills processes holding required ports)
./novaairouter --force

# Tests
go test ./...
go test ./tests/unit/...
go test ./tests/integration/...

# Single test
go test ./internal/gossip/... -run TestGossipSync -v
```

## Architecture

This is a **decentralized AI gateway cluster** — no central coordinator. Each node is equal and communicates via gossip protocol.

### Port Layout (default base: 15050)
| Port  | Purpose              |
|-------|----------------------|
| 15050 | Business API (proxy) |
| 15049 | Admin API + metrics  |
| 15051 | Gossip HTTP (inter-node) |
| 15052 | UDP discovery        |

### Core Subsystems

**`internal/gossip/`** — Cluster membership and state sync via `hashicorp/memberlist`. Nodes discover each other through UDP broadcast and maintain eventual consistency of the endpoint registry across the cluster.

**`internal/registry/`** — In-memory service registry. Backends register themselves via Admin API (`POST /v1/endpoints`) with path matching rules. The registry is replicated to all nodes via gossip.

**`internal/business/`** — Request forwarding pipeline:
- `router/` — matches incoming path to registered backend
- `balancer/` — selects backend instance (Round Robin / Least Connections / Fastest Response)
- `forwarder/` — proxies the HTTP request to the selected backend
- `pool/` — manages persistent connection pools per backend

**`internal/admin/`** — Management API (port 15049). Handles endpoint registration, heartbeats, node queries, plugin messaging, and Prometheus metrics at `/metrics`.

**`internal/config/`** — Viper-based config with hot reload. Config can come from YAML file, CLI flags, or env vars. `config/center.go` manages runtime config updates without restart.

**`internal/gossip/sync/`** — Handles delta sync of registry state between nodes when a new node joins or a state divergence is detected.

### Plugin System

Plugins register via the Admin API and communicate through the gossip layer. See `docs/MODEL_ROUTER_PLUGIN.md` for the Model Router plugin which enables model-name-based routing on top of path routing.

### Health & Failover

Backends send periodic heartbeats to `POST /v1/heartbeat`. If a heartbeat is missed beyond `--heartbeat-timeout`, the endpoint is marked unhealthy and removed from the balancer pool. The registry change propagates via gossip.

## Configuration

See `example-config.yaml` for all options. Key flags:

```
--node-id                  Unique node ID (auto-generated if omitted)
--listen-addr              Business server address (default :15050)
--default-max-concurrency  Max concurrent requests per endpoint
--queue-capacity           Request queue size
--backend-timeout          Upstream request timeout
--heartbeat-timeout        Time before unhealthy endpoint is removed
--gossip-interval          How often nodes sync state
--config                   Path to YAML config file
```
