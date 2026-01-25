# Docker Compose Deployment Guide

Complete guide to deploying connect-plugin-go with Docker Compose using the URL Shortener example.

## Overview

The `examples/docker-compose/` directory contains a production-like deployment demonstrating:

- **Model B (self-registering)** plugins
- Plugin-to-plugin communication via host-mediated routing
- Service discovery across containers
- Health-based readiness and graceful degradation
- Docker Compose ignorant of plugin dependencies

## Quick Start

```bash
cd examples/docker-compose

# 1. Build all images
./setup.sh

# 2. Start services
./run.sh

# 3. Test the URL shortener
./test.sh

# 4. Stop and cleanup
./cleanup.sh
```

## Architecture

```
┌──────────────┐
│    Client    │ (CLI tool)
│  (container) │
└──────┬───────┘
       │ HTTP :8083
       ↓
┌──────────────────────────────────────────────┐
│           API Plugin (:8083)                 │
│  Provides: api service                       │
│  Requires: storage service                   │
└──────┬───────────────────────────────────────┘
       │ Discovers storage from host registry
       │ Calls via: /services/storage/{id}/Store
       ↓
┌──────────────────────────────────────────────┐
│         Storage Plugin (:8082)               │
│  Provides: storage service                   │
│  Requires: logger service                    │
└──────┬───────────────────────────────────────┘
       │ Discovers logger from host registry
       │ Calls via: /services/logger/{id}/Log
       ↓
┌──────────────────────────────────────────────┐
│         Logger Plugin (:8081)                │
│  Provides: logger service                    │
│  Requires: nothing                           │
└──────────────────────────────────────────────┘
       │
       │ All discovery and routing through:
       ↓
┌──────────────────────────────────────────────┐
│         Host Platform (:8080)                │
│  - HandshakeService (assigns runtime_id)     │
│  - ServiceRegistry (discovery/registration)  │
│  - PluginLifecycle (health tracking)         │
│  - ServiceRouter (/services/...)             │
└──────────────────────────────────────────────┘
```

## docker-compose.yml Structure

```yaml
services:
  host:
    # Starts first, exposes Phase 2 services

  logger:
    depends_on: [host]  # Only knows about host!
    # NOT depends_on: []  - has no plugin dependencies

  storage:
    depends_on: [host]  # Only knows about host!
    # NOT depends_on: [logger]  - Docker Compose doesn't know!

  api:
    depends_on: [host]  # Only knows about host!
    # NOT depends_on: [storage]  - Docker Compose doesn't know!
```

**Key Insight:** Compose only expresses plugin→host dependencies. The host's **dependency graph** manages plugin→plugin dependencies via service discovery and health tracking.

## How It Works

### 1. Startup Sequence

```
t=0s:  docker-compose up
t=1s:  host starts, becomes healthy
t=2s:  logger, storage, api all start in parallel
t=3s:  logger registers, reports healthy (no deps)
t=3.5s: storage registers, discovers logger, reports healthy
t=4s:  api registers, discovers storage, reports healthy
```

### 2. Service Registration

Each plugin on startup:

```go
1. Connect to host: Handshake(self_id)
2. Host responds: {runtime_id, token}
3. Plugin registers: RegisterService(type, version, endpoint, metadata)
   - Includes base_url in metadata: "http://storage:8082"
4. Plugin discovers dependencies: DiscoverService(type, min_version)
5. Plugin reports health: ReportHealth(state, reason, unavailable_deps)
```

### 3. Plugin-to-Plugin Calls

API calls Storage via host router:

```
1. API: DiscoverService("storage")
   → Host: {endpoint: "/services/storage/storage-plugin-abc123"}

2. API: POST http://host:8080/services/storage/storage-plugin-abc123/Store?code=x&url=y
   Headers:
     X-Plugin-Runtime-ID: api-plugin-xyz789
     Authorization: Bearer <api-token>

3. Host Router:
   - Validates api-plugin-xyz789 token
   - Looks up storage-plugin-abc123 provider
   - Checks storage health (must be healthy/degraded)
   - Proxies to: http://storage:8082/storage.v1.Storage/Store?code=x&url=y
   - Logs: [ROUTER] api-plugin-xyz789 → storage-plugin-abc123 /Store

4. Storage handles request, calls Logger (same pattern)

5. Returns response through host back to API
```

## Use Case: URL Shortener

### Shorten a URL

```bash
docker-compose run --rm client shorten https://github.com/masegraye/connect-plugin-go
```

**Flow:**
1. Client calls API: `POST /api.v1.API/Shorten?url=https://...`
2. API generates short code: `abc123`
3. API discovers storage from registry
4. API calls storage via host: `/services/storage/{id}/Store?code=abc123&url=https://...`
5. Storage stores mapping
6. Storage discovers logger from registry
7. Storage calls logger via host: `/services/logger/{id}/Log?message=STORE%20abc123...`
8. Logger logs the operation
9. Storage returns success to API
10. API returns short code to client

**Output:**
```
✓ Shortened URL
  Original: https://github.com/masegraye/connect-plugin-go
  Code:     abc123
```

### Resolve Short Code

```bash
docker-compose run --rm client resolve abc123
```

**Flow:**
1. Client calls API: `GET /api.v1.API/Resolve?code=abc123`
2. API discovers storage
3. API calls storage: `/services/storage/{id}/Get?code=abc123`
4. Storage retrieves URL
5. Storage logs access
6. Returns original URL

**Output:**
```
✓ Resolved short code: abc123
  URL: https://github.com/masegraye/connect-plugin-go
```

## Automation Scripts

### setup.sh - Build Images

```bash
./setup.sh
```

- Builds all Docker images in parallel
- Uses multi-stage builds for small images
- Shows next steps after completion

### run.sh - Start Services

```bash
./run.sh
```

- Starts services: `docker-compose up -d`
- Waits for host health check (30s timeout)
- Gives plugins time to register (5s)
- Shows service status and log commands

### test.sh - Validate End-to-End

```bash
./test.sh
```

**Runs 3 tests:**
1. Shorten URL (extracts short code)
2. Resolve short code (verifies original URL)
3. Verify plugin→plugin routing in host logs

**Output:**
```
✅ URL shortened successfully
✅ Short code resolved successfully
✅ Found API → Storage routing in host logs
✅ Found Storage → Logger routing in host logs

🎉 All tests passed!
```

### cleanup.sh - Stop and Remove

```bash
./cleanup.sh
```

- Stops all containers
- Removes containers and volumes
- Cleans up networks
- Optional: Remove images (commented out)

## Observability

### View All Logs

```bash
docker-compose logs -f
```

### View Plugin Logs

```bash
docker-compose logs -f logger
docker-compose logs -f storage
docker-compose logs -f api
docker-compose logs -f host
```

### View Plugin-to-Plugin Routing

```bash
docker-compose logs host | grep ROUTER
```

**Example output:**
```
[ROUTER] api-plugin-xyz → storage-plugin-abc /Store (service: storage)
[ROUTER] api-plugin-xyz → storage-plugin-abc /Store 200 (duration: 1.2ms)
[ROUTER] storage-plugin-abc → logger-plugin-def /Log (service: logger)
[ROUTER] storage-plugin-abc → logger-plugin-def /Log 200 (duration: 455µs)
```

### View Plugin Health

```bash
# Check which plugins registered
docker-compose logs host | grep "Registered service"

# Check plugin health transitions
docker-compose logs storage | grep -E "(degraded|healthy)"
docker-compose logs api | grep -E "(degraded|healthy)"
```

## Key Features Demonstrated

### ✅ Model B Deployment

Plugins are **self-registering** - they connect to the host independently:

- Docker Compose starts containers
- Plugins connect to host via `HOST_URL` environment variable
- Plugins call host's `Handshake()` to get `runtime_id` and token
- Plugins register their services
- Plugins report health

### ✅ Compose Ignorant of Plugin Dependencies

```yaml
# Compose only knows: all plugins need host
storage:
  depends_on: [host]  # ✓
  # NOT [host, logger]  # ✗ Compose doesn't know storage needs logger

api:
  depends_on: [host]  # ✓
  # NOT [host, storage]  # ✗ Compose doesn't know API needs storage
```

**Why it works:**
- Plugins start in parallel (after host is healthy)
- Plugins report **degraded** if dependencies not available
- When dependencies appear, plugins discover them and report **healthy**
- Host's dependency graph knows the real dependencies: logger→storage→api

### ✅ Plugin-to-Plugin Communication

All plugin→plugin calls route through the host:

```
API Plugin → Host Router → Storage Plugin
Storage Plugin → Host Router → Logger Plugin
```

**Host Router provides:**
- Authentication (validates runtime_id and token)
- Authorization (checks caller permissions)
- Health checks (only routes to healthy/degraded plugins)
- Observability (logs all calls with duration)
- Service discovery (plugins find each other via registry)

### ✅ Graceful Degradation

If logger starts late:

```
t=0s: storage starts
t=1s: storage registers
t=2s: storage tries to discover logger → not found
t=3s: storage reports DEGRADED ("logger unavailable")
      storage still serves requests (writes to local storage)
t=10s: logger starts and registers
t=11s: storage discovers logger
t=12s: storage reports HEALTHY
      storage now logs all operations
```

**Result:** No failures, automatic recovery when dependencies appear!

## Deployment Patterns

### Production Deployment

For production, you'd add:

```yaml
# docker-compose.yml
services:
  storage:
    deploy:
      replicas: 3  # Scale storage
      restart_policy:
        condition: on-failure
    healthcheck:
      test: ["CMD", "wget", "-q", "-O-", "http://localhost:8082/health"]
```

### Environment-Specific Config

```yaml
# docker-compose.prod.yml
services:
  api:
    environment:
      - HOST_URL=https://platform.prod.example.com
      - LOG_LEVEL=info
```

Usage:
```bash
docker-compose -f docker-compose.yml -f docker-compose.prod.yml up
```

### Kubernetes Migration

The same plugin code works in Kubernetes:

```yaml
# Kubernetes Deployment
apiVersion: apps/v1
kind: Deployment
metadata:
  name: storage-plugin
spec:
  replicas: 2
  template:
    spec:
      containers:
      - name: storage
        image: myregistry/storage-plugin:latest
        env:
        - name: HOST_URL
          value: "http://plugin-host:8080"
        - name: PORT
          value: "8082"
```

Same Model B self-registration pattern!

## Troubleshooting

### Plugins Report Degraded

**Check logs:**
```bash
docker-compose logs storage | grep degraded
```

**Output:**
```
storage-1  | Logger not available yet, reporting degraded
```

**Solution:** Wait for dependencies to start. Check:
```bash
docker-compose ps  # Ensure logger is running
docker-compose logs logger | grep "Registered service"
```

### Plugin-to-Plugin Calls Fail

**Check router logs:**
```bash
docker-compose logs host | grep ROUTER
```

**Common issues:**
- Missing runtime_id header → Check plugin sends headers
- Invalid token → Check handshake completed
- Provider not found → Check service registered
- Provider unhealthy → Check plugin health

### Containers Won't Start

**Check build errors:**
```bash
docker-compose build 2>&1 | grep error
```

**Check Go version:**
```bash
# Dockerfiles use golang:alpine (Go 1.24+)
# Ensure go.mod is compatible
```

### Port Conflicts

**Check ports:**
```bash
docker-compose ps
lsof -i :8080  # Check if port in use
```

**Change ports:**
```yaml
# docker-compose.yml
services:
  host:
    ports:
      - "9080:8080"  # Map to different host port
```

## File Reference

### Plugin Implementations

- `../logger-plugin/main.go` - Logger service (no dependencies)
- `../storage-plugin/main.go` - Storage service (requires logger)
- `../api-plugin/main.go` - HTTP API (requires storage)
- `../url-client/main.go` - CLI client

### Docker Files

- `Dockerfile.host` - Host platform image
- `Dockerfile.logger` - Logger plugin image
- `Dockerfile.storage` - Storage plugin image
- `Dockerfile.api` - API plugin image
- `Dockerfile.client` - Client CLI image
- `docker-compose.yml` - Service definitions

### Automation Scripts

- `setup.sh` - Build all images
- `run.sh` - Start services and wait for readiness
- `test.sh` - Run end-to-end tests
- `cleanup.sh` - Stop and remove containers

### Platform Code

- `host/main.go` - Host platform server

## Next Steps

- [Deployment Models](../getting-started/deployment-models.md) - Understand Model A vs Model B
- [Service Registry](service-registry.md) - Deep dive into plugin-to-plugin communication
- [Interceptors](interceptors.md) - Add retry, circuit breaker, auth
- Migrate to Kubernetes - Same plugins, different orchestrator
