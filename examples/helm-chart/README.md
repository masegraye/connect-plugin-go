# Kubernetes Helm Chart: URL Shortener with Sidecar Pattern

Helm chart for deploying the URL shortener example to Kubernetes using the **sidecar pattern**.

## Architecture

**Single pod with 4 containers:**

```
┌─────────────────────────────────────────────────────────┐
│                         Pod                             │
│  ┌─────────────┐  ┌────────────┐  ┌─────────────────┐  │
│  │    Host     │  │   Logger   │  │    Storage      │  │
│  │  (primary)  │  │  (sidecar) │  │   (sidecar)     │  │
│  │             │  │            │  │                 │  │
│  │   :8080     │  │   :8081    │  │     :8082       │  │
│  │             │  │            │  │   requires      │  │
│  │  Registry   │  │  provides  │  │   logger ───────┼──┤
│  │  Lifecycle  │  │  logger    │  │                 │  │
│  │  Router     │  │            │  │   provides      │  │
│  │             │  │            │  │   storage       │  │
│  └─────────────┘  └────────────┘  └─────────────────┘  │
│         ↑              localhost communication          │
│  ┌──────────────────────────────────────────────────┐   │
│  │              API (sidecar)                       │   │
│  │              :8083                               │   │
│  │              requires storage                    │   │
│  │              provides api                        │   │
│  └──────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────┘
         │
         │ Kubernetes Service
         ↓
    External Access
```

## Prerequisites

- Kubernetes cluster (kind, minikube, or cloud provider)
- Helm 3.x
- Docker images built and pushed to registry

## Quick Start with kind (Local Testing)

### 1. Create kind Cluster with Local Registry

```bash
# Create kind cluster with local registry
cat <<EOF | kind create cluster --config=-
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
containerdConfigPatches:
- |-
  [plugins."io.containerd.grpc.v1.cri".registry.mirrors."localhost:5001"]
    endpoint = ["http://kind-registry:5000"]
EOF

# Start local registry
docker run -d --restart=always -p 5001:5000 --network=kind --name kind-registry registry:2

# Connect registry to kind network (if not already)
docker network connect kind kind-registry || true
```

### 2. Build and Push Images to Local Registry

```bash
# Build images (from docker-compose directory)
cd ../docker-compose
docker-compose build

# Tag and push to local registry with descriptive names
docker tag docker-compose-host localhost:5001/plugin-platform:latest
docker tag docker-compose-logger localhost:5001/logger-plugin:latest
docker tag docker-compose-storage localhost:5001/storage-plugin:latest
docker tag docker-compose-api localhost:5001/api-plugin:latest

docker push localhost:5001/plugin-platform:latest
docker push localhost:5001/logger-plugin:latest
docker push localhost:5001/storage-plugin:latest
docker push localhost:5001/api-plugin:latest
```

### 3. Install Helm Chart

```bash
cd ../helm-chart

# Install
helm install url-shortener .

# Or with custom values
helm install url-shortener . --set replicaCount=2
```

### 4. Test the URL Shortener

```bash
# Port-forward to access API
kubectl port-forward svc/url-shortener 8083:8083 &

# Shorten a URL
curl -X POST "http://localhost:8083/api.v1.API/Shorten?url=https://github.com/masegraye/connect-plugin-go"

# Output: {"short_code":"abc123","url":"https://github.com/masegraye/connect-plugin-go"}

# Resolve the short code
curl "http://localhost:8083/api.v1.API/Resolve?code=abc123"

# Output: {"url":"https://github.com/masegraye/connect-plugin-go"}
```

### 5. View Logs

```bash
# Get pod name
export POD=$(kubectl get pods -l app.kubernetes.io/name=url-shortener -o jsonpath='{.items[0].metadata.name}')

# View all container logs
kubectl logs $POD --all-containers

# View specific container
kubectl logs $POD -c host
kubectl logs $POD -c logger
kubectl logs $POD -c storage
kubectl logs $POD -c api

# Watch plugin→plugin routing
kubectl logs $POD -c host | grep ROUTER
```

### 6. Cleanup

```bash
# Uninstall chart
helm uninstall url-shortener

# Delete kind cluster (if done)
kind delete cluster
```

## Production Deployment

### 1. Push Images to Your Registry

```bash
# Build images first (from docker-compose directory)
cd ../docker-compose && docker-compose build && cd ../helm-chart

# Tag for your registry
docker tag docker-compose-host your-registry.io/plugin-platform:v1.0.0
docker tag docker-compose-logger your-registry.io/logger-plugin:v1.0.0
docker tag docker-compose-storage your-registry.io/storage-plugin:v1.0.0
docker tag docker-compose-api your-registry.io/api-plugin:v1.0.0

# Push
docker push your-registry.io/plugin-platform:v1.0.0
docker push your-registry.io/logger-plugin:v1.0.0
docker push your-registry.io/storage-plugin:v1.0.0
docker push your-registry.io/api-plugin:v1.0.0
```

### 2. Create values-prod.yaml

```yaml
replicaCount: 3

image:
  repository: your-registry.io
  tag: v1.0.0

images:
  host: plugin-platform
  logger: logger-plugin
  storage: storage-plugin
  api: api-plugin

service:
  type: LoadBalancer
  port: 80
  targetPort: 8083

resources:
  host:
    limits:
      cpu: 500m
      memory: 256Mi
    requests:
      cpu: 250m
      memory: 128Mi
  api:
    limits:
      cpu: 500m
      memory: 256Mi
    requests:
      cpu: 250m
      memory: 128Mi
```

### 3. Deploy to Production

```bash
helm install url-shortener . -f values-prod.yaml
```

## Configuration

### Key Values

| Parameter | Description | Default |
|-----------|-------------|---------|
| `replicaCount` | Number of pod replicas | `1` |
| `image.repository` | Container registry prefix | `localhost:5001` |
| `image.tag` | Image tag (defaults to Chart.AppVersion) | `""` |
| `service.type` | Kubernetes service type | `ClusterIP` |
| `service.port` | Service port | `8083` |
| `resources.host.limits.cpu` | Host CPU limit | `200m` |
| `resources.host.limits.memory` | Host memory limit | `128Mi` |

See `values.yaml` for all configuration options.

### Custom Values Example

```bash
helm install url-shortener . \
  --set replicaCount=3 \
  --set service.type=LoadBalancer \
  --set image.repository=myregistry.io/url-shortener
```

## How It Works

### Sidecar Pattern

All 4 containers run in the **same pod**:

- **Share localhost network** - Fast communication via 127.0.0.1
- **Atomic scaling** - All containers scale together
- **Shared lifecycle** - Start/stop as a unit
- **No network policies needed** - All communication is local

### Container Startup Order

1. **Init container** waits for host health check
2. **Host** starts first (provides Service Registry services)
3. **Logger, Storage, API** start in parallel as sidecars
4. **Plugins self-register** with host via localhost:8080

### Plugin Communication

**All via localhost:**

```
API → http://localhost:8080/services/storage/{id}/Store
      ↓ (host routes to)
      http://localhost:8082/storage.v1.Storage/Store

Storage → http://localhost:8080/services/logger/{id}/Log
          ↓ (host routes to)
          http://localhost:8081/logger.v1.Logger/Log
```

**Fast:** No network hops, no service mesh needed!

### Service Discovery

Plugins discover each other via **host's ServiceRegistry**:

```
1. Logger registers: RegisterService(type="logger")
2. Storage discovers: DiscoverService(type="logger")
   → {endpoint: "/services/logger/logger-plugin-abc"}
3. Storage calls via localhost host router
```

### Health and Readiness

**Host container:**
- Liveness: `GET /health` on :8080
- Readiness: `GET /health` on :8080

**Sidecar containers:**
- No direct probes (checked via host lifecycle tracking)
- Report health to host platform
- Host won't route to unhealthy plugins

## Scaling

```bash
# Scale to 3 replicas
kubectl scale deployment url-shortener --replicas=3

# Each pod runs all 4 containers
# Each pod is independent (own storage)
```

**Note:** Storage is in-memory, so each pod has its own data. For production, you'd add a shared database.

## Advantages of Sidecar Pattern

✅ **Fast communication** - Localhost, no network latency
✅ **Simple networking** - No service mesh required
✅ **Atomic lifecycle** - Containers start/stop together
✅ **Resource co-location** - Guaranteed to run on same node
✅ **No DNS resolution** - Direct localhost communication

## Disadvantages

❌ **Tight coupling** - All containers scale together
❌ **Resource waste** - Can't scale plugins independently
❌ **Large pods** - More resource usage per replica

## When to Use Sidecar Pattern

**Good for:**
- Tightly coupled plugins that always need each other
- Development/testing environments
- Small-scale deployments
- When network latency matters

**Not ideal for:**
- Microservices that scale independently
- Large-scale production (prefer separate deployments)
- When plugins have different scaling needs

## Alternative: Separate Deployments

For production, consider separate deployments per plugin:

```yaml
# One Deployment per plugin
apiVersion: apps/v1
kind: Deployment
metadata:
  name: storage-plugin
spec:
  replicas: 5  # Scale storage independently
  ...
  containers:
  - name: storage
    image: storage-plugin
    env:
    - name: HOST_URL
      value: "http://plugin-host:8080"  # K8s Service DNS
```

This requires KOR-zcwl (Kubernetes Discovery) or simple Service DNS.

## Troubleshooting

### Pods won't start

```bash
# Check pod status
kubectl get pods

# Describe pod
kubectl describe pod <pod-name>

# Check init container logs
kubectl logs <pod-name> -c wait-for-host
```

### Plugins report degraded

```bash
# Check all container logs
kubectl logs <pod-name> --all-containers

# Check if plugins registered
kubectl logs <pod-name> -c host | grep "Registered service"

# Check health states
kubectl logs <pod-name> -c storage | grep -E "degraded|healthy"
```

### Images won't pull

```bash
# Check image names
kubectl describe pod <pod-name> | grep Image

# For kind local registry, ensure images are pushed:
docker push localhost:5001/docker-compose-host:latest
```

## Next Steps

- Add persistent volume for storage (shared across replicas)
- Add Ingress for external access
- Add monitoring (Prometheus metrics)
- Migrate to separate deployments for independent scaling
- Implement KOR-zcwl for dynamic K8s service discovery
