# Docker Compose Example: URL Shortener

Complete docker-compose example demonstrating **Model B (self-registering)** deployment.

## Quick Start

```bash
cd examples/docker-compose

# 1. Build all images
./setup.sh

# 2. Start all services
./run.sh

# 3. Test the URL shortener
./test.sh

# 4. Stop and cleanup
./cleanup.sh
```

### Manual Commands

```bash
# Build images
docker-compose build

# Start services
docker-compose up -d

# Test manually
docker-compose run --rm client shorten https://github.com/masegraye/connect-plugin-go
docker-compose run --rm client resolve <code-from-above>

# Stop services
docker-compose down
```

## Architecture

```
Client (CLI) → API Plugin → Storage Plugin → Logger Plugin
                    ↓            ↓              ↓
            All plugin→plugin calls route through Host
```

### Services

1. **host** (:8080) - Phase 2 platform with registry/lifecycle/router
2. **logger** (:8081) - Logging service (no deps)
3. **storage** (:8082) - Key-value storage (requires logger)
4. **api** (:8083) - HTTP API (requires storage)
5. **client** - CLI tool for shorten/resolve

### Dependency Graph (Managed by Host)

```
Logger (no dependencies)
   ↑ required
Storage (requires logger)
   ↑ required  
API (requires storage)
```

**Key:** docker-compose only knows plugin→host dependencies!
Host manages plugin→plugin via dependency graph and health tracking.

## How It Works

### 1. Compose Starts Services

```yaml
# Compose only knows: all plugins depend on host
logger:
  depends_on: [host]   # NOT [host, logger]!
  
storage:
  depends_on: [host]   # NOT [host, logger]!
  
api:
  depends_on: [host]   # NOT [host, storage]!
```

### 2. Plugins Self-Register

All plugins start in parallel and connect to host:

```
1. Plugin connects: Handshake(self_id="storage-plugin")
2. Host responds: {runtime_id="storage-plugin-a1b2", token="..."}
3. Plugin registers: RegisterService(type="storage")
4. Plugin checks deps: DiscoverService(type="logger")
   - If not found → ReportHealth(DEGRADED, "logger unavailable")
   - If found → ReportHealth(HEALTHY)
```

### 3. Host Routes Plugin→Plugin Calls

API calls storage via host router:

```
API → Host.DiscoverService("storage") 
    → {endpoint: "/services/storage/storage-plugin-a1b2"}

API → POST http://host:8080/services/storage/storage-plugin-a1b2/Store
    → Host validates runtime_id and token
    → Host checks storage health (must be healthy/degraded)
    → Host proxies to storage plugin
    → Storage handles request
```

### 4. Health Negotiates Readiness

**Startup timeline:**
```
t=0s: host starts
t=1s: logger, storage, api all start (parallel)
t=1.5s: logger registers → reports HEALTHY
t=2s: storage registers → discovers logger → reports HEALTHY
t=2.5s: api registers → discovers storage → reports HEALTHY
```

If logger starts slow:
```
t=0s: host starts
t=1s: storage, api start
t=1.5s: storage registers → logger not found → reports DEGRADED
t=2s: api registers → storage degraded → reports DEGRADED  
t=5s: logger starts late → registers
t=5.5s: storage re-discovers → reports HEALTHY
t=6s: api re-discovers → reports HEALTHY
```

**Result:** Graceful degradation, no failures!

## Commands

### Shorten URL

```bash
docker-compose run --rm client shorten https://example.com
```

Output:
```
✓ Shortened URL
  Original: https://example.com
  Code:     a1b2c3d4
  Resolve:  http://api:8083/api.v1.API/Resolve?code=a1b2c3d4
```

### Resolve Short Code

```bash
docker-compose run --rm client resolve a1b2c3d4
```

Output:
```
✓ Resolved short code: a1b2c3d4
  URL: https://example.com
```

### View Logs

```bash
# All services
docker-compose logs -f

# Specific service
docker-compose logs -f storage

# See plugin→plugin routing in host
docker-compose logs host | grep ROUTER
```

Example output:
```
[ROUTER] api-plugin-e5f6 → storage-plugin-c3d4 /Store (service: storage)
[ROUTER] storage-plugin-c3d4 → logger-plugin-a1b2 /Log (service: logger)
```

## What This Demonstrates

✅ **Model B deployment** - Compose orchestrates, host doesn't manage processes
✅ **Self-registration** - Plugins connect and register independently  
✅ **Compose simplicity** - Only plugin→host in depends_on
✅ **Host dependency graph** - Manages logger→storage→api internally
✅ **Plugin→plugin calls** - All routed through host
✅ **Service discovery** - Plugins find each other via registry
✅ **Graceful degradation** - Plugins work even if deps start late
✅ **Health tracking** - Degraded→healthy transitions
✅ **Production-like** - Containerized, scalable architecture

## Troubleshooting

**Check service health:**
```bash
docker-compose ps
```

**Plugin reports degraded:**
```bash
# Check if dependency is running
docker-compose logs logger | grep "Registered service"

# Check discovery
docker-compose logs storage | grep "Logger discovered"
```

**Client fails:**
```bash
# Verify API is healthy
docker-compose logs api | grep healthy

# Check API is accessible
curl http://localhost:8083/api.v1.API/Shorten?url=https://test.com
```

**Rebuild after code changes:**
```bash
docker-compose up --build --force-recreate
```

## Files

- `docker-compose.yml` - Service definitions
- `Dockerfile.host` - Host platform image
- `Dockerfile.logger` - Logger plugin image
- `Dockerfile.storage` - Storage plugin image  
- `Dockerfile.api` - API plugin image
- `Dockerfile.client` - Client CLI image
- `host/main.go` - Host platform server
- `../storage-plugin/main.go` - Storage plugin
- `../api-plugin/main.go` - API plugin
- `../url-client/main.go` - CLI client

## Next Steps

- Add metrics/observability
- Deploy to Kubernetes (similar pattern)
- Add more plugins
- Scale plugins (multiple replicas)

