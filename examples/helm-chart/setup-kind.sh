#!/bin/bash
set -e

echo "🚀 Setting up kind cluster with local registry for URL Shortener..."
echo ""

# Check if kind is installed
if ! command -v kind &> /dev/null; then
    echo "❌ kind is not installed"
    echo "Install with: brew install kind"
    exit 1
fi

# Check if helm is installed
if ! command -v helm &> /dev/null; then
    echo "❌ helm is not installed"
    echo "Install with: brew install helm"
    exit 1
fi

# Create kind cluster with local registry
echo "📦 Creating kind cluster..."

cat <<EOF | kind create cluster --name url-shortener --config=-
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
containerdConfigPatches:
- |-
  [plugins."io.containerd.grpc.v1.cri".registry.mirrors."localhost:5001"]
    endpoint = ["http://kind-registry:5000"]
EOF

echo ""
echo "📦 Starting local Docker registry..."

# Start local registry if not running
if ! docker ps | grep -q kind-registry; then
    docker run -d --restart=always -p 5001:5000 --network=kind --name kind-registry registry:2 2>/dev/null || true
fi

# Connect registry to kind network
docker network connect kind kind-registry 2>/dev/null || true

echo ""
echo "✅ kind cluster and registry ready!"
echo ""
echo "Next steps:"
echo "  cd ../docker-compose"
echo "  ./setup.sh"
echo "  cd ../helm-chart"
echo "  ./push-images.sh"
echo "  ./install.sh"
