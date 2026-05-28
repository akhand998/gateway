# Multi-Tenant API Gateway with Rate Limiting

A production-grade reverse proxy built from scratch in Go, featuring per-tenant rate limiting via Redis token buckets, dynamic routing, circuit breaking, and zero-downtime config reload.

---

## What This Project Demonstrates

- Distributed rate limiting with atomic Redis Lua scripts (no race conditions across instances)
- Per-tenant auth, routing, and upstream isolation
- Circuit breaker state machine (closed → open → half-open)
- Hot config reload using `atomic.Value` — no dropped connections, no restarts
- Full observability: Prometheus metrics + OpenTelemetry traces per tenant

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
│  [Rate Limiter] ◄──── Redis Cluster     │
│         │                               │
│         ▼                               │
│  [Router + Load Balancer]               │
│         │                               │
│         ▼                               │
│  [Circuit Breaker]                      │
│         │                               │
│  [Observability: Prometheus + OTEL]     │
│  [Config Watcher: hot reload]           │
└────────────┬────────────────────────────┘
             │
     ┌───────┼───────┐
     ▼       ▼       ▼
 Service A  Service B  gRPC Svc
```

---

## Tech Stack

| Layer | Technology |
|---|---|
| Language | Go 1.22+ |
| Rate limit state | Redis 7 (Lua scripts) |
| Config store | etcd (or Postgres) |
| Metrics | Prometheus + Grafana |
| Tracing | OpenTelemetry |
| Load testing | k6 |
| Containerisation | Docker + Docker Compose |

---

## Project Structure

```
gateway/
├── cmd/
│   └── gateway/
│       └── main.go             # Entry point, wiring
├── internal/
│   ├── auth/
│   │   └── resolver.go         # JWT / API key → tenant ID
│   ├── config/
│   │   ├── store.go            # atomic.Value wrapper
│   │   ├── watcher.go          # etcd/poll watcher goroutine
│   │   └── types.go            # GatewayConfig, TenantConfig
│   ├── ratelimit/
│   │   ├── limiter.go          # Token bucket interface
│   │   └── redis_lua.go        # Lua script + Redis EVAL
│   ├── router/
│   │   └── router.go           # Path match, rewrite, upstream select
│   ├── proxy/
│   │   └── proxy.go            # httputil.ReverseProxy wrapper
│   ├── circuitbreaker/
│   │   └── breaker.go          # Closed / Open / Half-open FSM
│   └── observability/
│       ├── metrics.go          # Prometheus counters + histograms
│       └── tracing.go          # OTEL span setup
├── deploy/
│   ├── docker-compose.yml      # Gateway + Redis + etcd + Grafana
│   └── dashboards/
│       └── gateway.json        # Grafana dashboard (importable)
├── loadtest/
│   └── ratelimit_test.js       # k6 script — proves limits hold
├── configs/
│   └── tenants.yaml            # Seed config for local dev
└── README.md
```

---

## Phase 1 Quickstart (Local)

Start two simple upstream echo services, then run the gateway:

```bash
# Terminal 1
go run ./cmd/echo --addr :9001 --name service-a

# Terminal 2
go run ./cmd/echo --addr :9002 --name service-b

# Terminal 3
go run ./cmd/gateway --addr :8080 --upstreams http://localhost:9001,http://localhost:9002
```

Verify the proxy and health endpoint:

```bash
curl http://localhost:8080/hello
curl http://localhost:8080/healthz
```

---

## Phase 2 Quickstart (Auth + Tenant Routing)

Start upstreams and the gateway with two tenants:

```bash
# Terminal 1
go run ./cmd/echo --addr :9001 --name service-a

# Terminal 2
go run ./cmd/echo --addr :9002 --name service-b

# Terminal 3
go run ./cmd/gateway \
    --addr :8080 \
    --tenant-a-upstreams http://localhost:9001 \
    --tenant-b-upstreams http://localhost:9002 \
    --api-keys api-key-a=tenant-a,api-key-b=tenant-b \
    --jwt-secret dev-secret
```

Call with an API key (maps to a tenant):

```bash
curl -H "X-API-Key: api-key-a" http://localhost:8080/hello
curl -H "X-API-Key: api-key-b" http://localhost:8080/hello
```

JWTs are also accepted. Include a `tenant_id` claim and sign with `--jwt-secret`:

```bash
curl -H "Authorization: Bearer <jwt-with-tenant_id>" http://localhost:8080/hello
```

Generate a JWT locally with the helper CLI:

```bash
TOKEN=$(go run ./cmd/jwtgen --tenant tenant-a --secret dev-secret)
curl -H "Authorization: Bearer $TOKEN" http://localhost:8080/hello
```

---

## Phase 3 Quickstart (Redis Rate Limiting)

Start Redis (Docker required):

```bash
docker run --rm -p 6379:6379 --name gateway-redis redis:7
```

Run gateway with rate limits enabled:

```bash
go run ./cmd/gateway \
    --addr :8080 \
    --tenant-a-upstreams http://localhost:9001 \
    --tenant-b-upstreams http://localhost:9002 \
    --api-keys api-key-a=tenant-a,api-key-b=tenant-b \
    --jwt-secret dev-secret \
    --redis-addr localhost:6379 \
    --tenant-a-rate 5 --tenant-a-burst 10 \
    --tenant-b-rate 5 --tenant-b-burst 10
```

Then hammer the gateway and observe 429s when the bucket drains:

```bash
for i in {1..30}; do curl -s -o /dev/null -w "%{http_code}\n" -H "X-API-Key: api-key-a" http://localhost:8080/hello; done
```

---

## Build Plan (6 Weeks)

### Phase 1 — Dumb Proxy (Week 1)

**Goal:** forward any request to a hardcoded upstream and return the response.

- `net/http/httputil.ReverseProxy` wrapping two local upstreams
- Basic health check endpoint (`GET /healthz`)
- Docker Compose with two echo services as upstreams

**Done when:** `curl localhost:8080/anything` proxies to an upstream and returns a response.

---

### Phase 2 — Tenant Auth (Week 2)

**Goal:** every request must carry a valid JWT or API key; unknown tenants get 401.

- JWT parsing middleware (use `golang-jwt/jwt`)
- Extract `tenant_id` claim; attach to `context.Context`
- Hardcoded in-memory tenant map (routes, upstream targets)
- Return tenant-specific upstream based on config

**Done when:** requests without a valid token get 401; Tenant A routes to Service A, Tenant B to Service B.

---

### Phase 3 — Redis Rate Limiting (Weeks 3–4)

**Goal:** each tenant gets a token bucket; limits hold under concurrent load across multiple gateway instances.

- Token bucket in Redis using a Lua script (atomic, no races)
- `EVAL` the script on every request; return 429 if bucket empty
- Per-tenant config: `rate_per_second`, `burst_size`
- Run two gateway instances behind a local load balancer to prove shared state works
- Load test with k6: ramp to 10× the limit, assert 429 rate is correct

**Lua script (core of this phase):**
```lua
local key    = KEYS[1]
local rate   = tonumber(ARGV[1])  -- tokens/sec
local burst  = tonumber(ARGV[2])
local now    = tonumber(ARGV[3])  -- unix ms

local state  = redis.call("HMGET", key, "tokens", "last")
local tokens = tonumber(state[1]) or burst
local last   = tonumber(state[2]) or now

local elapsed = math.max(0, now - last) / 1000
tokens = math.min(burst, tokens + elapsed * rate)

if tokens >= 1 then
    tokens = tokens - 1
    redis.call("HMSET", key, "tokens", tokens, "last", now)
    redis.call("EXPIRE", key, math.ceil(burst / rate) + 1)
    return 1  -- allowed
else
    return 0  -- denied
end
```

**Done when:** k6 load test shows rate limit holds within ±2% of configured threshold across two gateway instances.

---

### Phase 4 — Circuit Breaker + Observability (Week 5)

**Goal:** protect upstreams from cascade failure; make the system debuggable.

**Circuit breaker states:**
- `Closed` — requests pass through; track consecutive failures
- `Open` — after N failures, fail-fast with 503 for T seconds
- `Half-open` — let one probe request through; if it succeeds, close; if not, reopen

**Prometheus metrics to emit:**
```
gateway_requests_total{tenant, route, status_code}
gateway_rate_limit_hits_total{tenant}
gateway_upstream_latency_seconds{tenant, upstream}
gateway_circuit_breaker_state{tenant, upstream}  # 0=closed 1=open 2=half-open
```

**Done when:** Grafana dashboard shows per-tenant RPS, p99 latency, and rate limit hit rate. Killing an upstream trips the circuit breaker and you can watch it recover.

---

### Phase 5 — Hot Config Reload (Week 6)

**Goal:** change a tenant's rate limit while traffic is running; new limit takes effect within seconds; no dropped connections.

- Replace hardcoded config map with etcd or Postgres as config source
- `ConfigStore` wraps `atomic.Value`; watcher goroutine polls/watches for changes
- On change: build new `*GatewayConfig` snapshot, call `store.Store(newCfg)`
- In-flight requests finish on old config; new requests pick up new config immediately
- `server.Shutdown(ctx)` on SIGTERM drains in-flight requests before exit

**Done when:** you update a tenant's rate limit in etcd while k6 is running and observe the limit change in Grafana within 5 seconds, with zero 500 errors.

---

## Key Design Decisions (Interview Talking Points)

| Decision | Why |
|---|---|
| Lua script for rate limiting | Atomic on Redis server side — no round-trip race conditions possible with WATCH/MULTI/EXEC |
| `atomic.Value` for config | Lock-free reads; readers see either old or new config atomically, never partial state |
| Immutable config snapshots | Eliminates data races on shared maps; GC frees old snapshot when last ref drops |
| Separate connection pools per tenant | One tenant's slow upstream can't exhaust connections for other tenants |
| Redis unavailable → fail-open policy | Gateway allows requests rather than taking down all tenants; decision is logged and alerted |

---

## Running Locally

```bash
# Start dependencies
docker compose up -d redis etcd grafana

# Seed initial config
go run ./cmd/seed --config configs/tenants.yaml

# Run gateway (two instances to test distributed rate limiting)
go run ./cmd/gateway --addr :8080 --instance 1 &
go run ./cmd/gateway --addr :8081 --instance 2 &

# Hit the gateway
curl -H "Authorization: Bearer <tenant-a-jwt>" http://localhost:8080/api/hello

# Run load test
k6 run loadtest/ratelimit_test.js
```

---

## Load Test Results (target)

```
Scenario: 2 gateway instances, Tenant A limit = 100 req/s

✓ status 200 rate: 99.8% (within limit)
✓ status 429 rate: 0.2% (burst absorbed correctly)
✗ status 500 rate: 0.0%

Actual RPS allowed: 100.4  (±0.4% of configured 100)
p99 latency: 12ms
```

---

## Observability

Import `deploy/dashboards/gateway.json` into Grafana (pointed at your Prometheus instance) to get:

- Per-tenant RPS, error rate, and p99 latency
- Rate limit hit rate per tenant
- Circuit breaker state per upstream
- Config reload events as annotations

---

## What's Not Included (intentional scope limits)

- TLS termination (use a sidecar like Envoy or nginx upstream of the gateway)
- Persistent audit logging (add a middleware writing to Kafka if needed)
- Admin API for config changes (etcd CLI or a simple REST endpoint is enough for now)

---

## Resources

- [Token bucket algorithm](https://en.wikipedia.org/wiki/Token_bucket)
- [Go `sync/atomic` docs](https://pkg.go.dev/sync/atomic)
- [Redis EVAL + Lua](https://redis.io/docs/manual/programmability/eval-intro/)
- [OpenTelemetry Go SDK](https://opentelemetry.io/docs/instrumentation/go/)
- [k6 load testing](https://k6.io/docs/)