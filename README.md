# Multi-Tenant API Gateway

A production-grade reverse proxy built from scratch in Go, featuring per-tenant rate limiting via Redis token buckets, dynamic routing, circuit breaking, and zero-downtime config reload.

---

## Features

- Distributed rate limiting with atomic Redis Lua scripts (no race conditions across instances)
- Per-tenant auth (API keys + JWT), routing, and upstream isolation
- Circuit breaker state machine (closed → open → half-open)
- Hot config reload using `atomic.Value` — no dropped connections, no restarts
- Prometheus metrics with cardinality-safe path normalization
- Round-robin load balancing across upstream targets
- Graceful shutdown with connection draining
- TLS support (optional)
- Request body size limits and upstream timeouts

---

## Architecture

```
Clients (Tenant A/B/C)
        │
        ▼
┌─────────────────────────────────────────┐
│              Gateway Core               │
│                                         │
│  [Auth & Tenant Resolve]                │
│         │                               │
│         ▼                               │
│  [Rate Limiter] ◄──── Redis             │
│         │                               │
│         ▼                               │
│  [Router + Load Balancer]               │
│         │                               │
│         ▼                               │
│  [Circuit Breaker]                      │
│         │                               │
│  [Observability: Prometheus]            │
│  [Config Watcher: hot reload]           │
└────────────┬────────────────────────────┘
             │
     ┌───────┼───────┐
     ▼       ▼       ▼
 Service A  Service B  ...
```

---

## Tech Stack

| Layer            | Technology              |
| ---------------- | ----------------------- |
| Language         | Go 1.22+                |
| Rate limit state | Redis 7 (Lua scripts)   |
| Metrics          | Prometheus              |

---

## Quick Start (Fork → Clone → Run)

### Prerequisites

- **Go 1.22+** — [install](https://go.dev/dl/)
- **Redis 7** — [install](https://redis.io/docs/getting-started/) or use Docker

### 1. Clone the repo

```bash
git clone https://github.com/<your-username>/gateway.git
cd gateway
```

### 2. Install Go dependencies

```bash
go mod download
```

### 3. Start Redis

Using Docker:

```bash
docker run --rm -p 6379:6379 --name gateway-redis redis:7
```

Or if Redis is installed locally:

```bash
redis-server
```

### 4. Start the upstream echo services

Open two terminals:

```bash
# Terminal 1
go run ./cmd/echo --addr :9001 --name service-a

# Terminal 2
go run ./cmd/echo --addr :9002 --name service-b
```

### 5. Start the gateway

```bash
# Terminal 3
GATEWAY_JWT_SECRET=dev-secret go run ./cmd/gateway \
    --addr :8080 \
    --config configs/tenants.yaml \
    --redis-addr localhost:6379
```

### 6. Test it

```bash
# Health check
curl http://localhost:8080/healthz

# Route to service-a via API key
curl -H "X-API-Key: api-key-a" http://localhost:8080/hello

# Route to service-b via API key
curl -H "X-API-Key: api-key-b" http://localhost:8080/hello

# No auth → 401
curl http://localhost:8080/hello

# JWT auth
TOKEN=$(go run ./cmd/jwtgen --tenant tenant-a --secret dev-secret)
curl -H "Authorization: Bearer $TOKEN" http://localhost:8080/hello

# Trigger rate limiting (burst=10, sending 15 requests)
for i in $(seq 1 15); do
  printf "req %2d: " $i
  curl -s -o /dev/null -w "%{http_code}" -H "X-API-Key: api-key-a" http://localhost:8080/hello
  echo ""
done

# View Prometheus metrics
curl http://localhost:8080/metrics | grep gateway_
```

---

## Configuration

Tenants are defined in `configs/tenants.yaml`:

```yaml
tenants:
  - id: tenant-a
    upstreams:
      - http://localhost:9001
    rate_per_second: 5
    burst: 10
    api_keys:
      - api-key-a
    breaker_max_failures: 2
    breaker_open_seconds: 5

  - id: tenant-b
    upstreams:
      - http://localhost:9002
    rate_per_second: 5
    burst: 10
    api_keys:
      - api-key-b
    breaker_max_failures: 2
    breaker_open_seconds: 5
```

The gateway watches this file and **hot-reloads** changes without restart. Edit the file while traffic is running and the new config takes effect within seconds.

---

## CLI Flags

| Flag                   | Default                  | Description                                     |
| ---------------------- | ------------------------ | ----------------------------------------------- |
| `--addr`               | `:8080`                  | Listen address                                  |
| `--config`             | `configs/tenants.yaml`   | Path to tenant config file                      |
| `--config-poll`        | `2s`                     | How often to check the config file for changes   |
| `--redis-addr`         | `localhost:6379`         | Redis address                                   |
| `--redis-db`           | `0`                      | Redis database number                           |
| `--jwt-secret`         | (none)                   | HMAC secret (prefer `GATEWAY_JWT_SECRET` env var)|
| `--breaker-max-failures`| `3`                     | Consecutive failures before circuit opens        |
| `--breaker-open-seconds`| `5`                     | Seconds to keep circuit open                     |
| `--read-timeout`       | `15s`                    | HTTP server read timeout                         |
| `--write-timeout`      | `30s`                    | HTTP server write timeout                        |
| `--idle-timeout`       | `60s`                    | HTTP server idle timeout                         |
| `--upstream-timeout`   | `30s`                    | Timeout for upstream requests                    |
| `--max-body-bytes`     | `1048576` (1MB)          | Maximum request body size                        |
| `--tls-cert`           | (none)                   | TLS certificate file (enables HTTPS)             |
| `--tls-key`            | (none)                   | TLS key file (enables HTTPS)                     |
| `--drain-timeout`      | `10s`                    | Graceful shutdown drain timeout                  |

---

## Running Tests

```bash
# Unit tests (no Redis needed)
go test ./internal/auth/... ./internal/circuitbreaker/... ./internal/config/... ./internal/proxy/... ./internal/router/... ./internal/observability/...

# Rate limiter integration tests (requires Redis on localhost:6379)
go test ./internal/ratelimit/...

# All tests
go test ./...

# With verbose output
go test -v ./...
```

---

## Project Structure

```
gateway/
├── cmd/
│   ├── gateway/
│   │   └── main.go              # Entry point — wiring, middleware, graceful shutdown
│   ├── echo/
│   │   └── main.go              # Simple echo server (test upstream)
│   └── jwtgen/
│       └── main.go              # JWT generator CLI for testing
├── internal/
│   ├── auth/
│   │   ├── resolver.go          # JWT / API key → tenant ID
│   │   └── resolver_test.go
│   ├── config/
│   │   ├── types.go             # GatewayConfig, TenantConfig, RateConfig
│   │   ├── loader.go            # YAML parsing + validation
│   │   ├── store.go             # atomic.Value wrapper for lock-free reads
│   │   ├── store_test.go
│   │   ├── watcher.go           # File polling goroutine for hot reload
│   │   └── watcher_test.go
│   ├── ratelimit/
│   │   ├── limiter.go           # Limiter interface
│   │   ├── redis_lua.go         # Token bucket via Redis Lua script
│   │   └── redis_lua_test.go
│   ├── router/
│   │   └── router.go            # Tenant → upstream routing table
│   ├── proxy/
│   │   ├── proxy.go             # Round-robin load balancer
│   │   └── proxy_test.go
│   ├── circuitbreaker/
│   │   ├── breaker.go           # Closed / Open / Half-open FSM
│   │   └── breaker_test.go
│   └── observability/
│       ├── metrics.go           # Prometheus counters + histograms + path normalization
│       ├── metrics_test.go
│       └── tracing.go           # OpenTelemetry placeholder

├── configs/
│   └── tenants.yaml             # Tenant configuration
├── go.mod
├── go.sum
└── README.md
```

---

## Key Design Decisions

| Decision                              | Why                                                                                                    |
| ------------------------------------- | ------------------------------------------------------------------------------------------------------ |
| Lua script for rate limiting          | Atomic on Redis server side — no race conditions possible with WATCH/MULTI/EXEC                        |
| `atomic.Value` for config             | Lock-free reads; readers see either old or new config atomically, never partial state                  |
| Immutable config snapshots            | Eliminates data races on shared maps; GC frees old snapshot when last ref drops                        |
| Per-tenant `http.Transport`           | Connection pooling per tenant — one tenant's slow upstream can't exhaust connections for others         |
| Redis unavailable → fail-open         | Gateway allows requests rather than taking down all tenants; logged for alerting                        |
| Path normalization in metrics         | Prevents cardinality explosion from attacker-generated paths crashing Prometheus                       |
| Circuit breaker state preserved       | Config reloads don't reset accumulated failure counts for unchanged upstream targets                    |

---

## Prometheus Metrics

| Metric                                  | Type      | Labels                      |
| --------------------------------------- | --------- | --------------------------- |
| `gateway_requests_total`                | Counter   | `tenant`, `route`, `status_code` |
| `gateway_rate_limit_hits_total`         | Counter   | `tenant`                    |
| `gateway_upstream_latency_seconds`      | Histogram | `tenant`, `upstream`        |
| `gateway_circuit_breaker_state`         | Gauge     | `tenant`, `upstream`        |

Breaker state values: `0` = closed, `1` = open, `2` = half-open.

---

## License

MIT