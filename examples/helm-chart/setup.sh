#!/bin/bash
set -e

echo "🔨 Building and pushing images for Kubernetes deployment..."
echo ""

# Check prerequisites
if ! command -v docker &> /dev/null; then
    echo "❌ docker not installed"
    exit 1
fi

if ! command -v helm &> /dev/null; then
    echo "❌ helm not installed"
    echo "Install with: brew install helm"
    exit 1
fi

if ! kubectl cluster-info &> /dev/null; then
    echo "❌ No Kubernetes cluster available"
    echo "kubectl cluster-info failed"
    exit 1
fi

# Build images via docker-compose
echo "📦 Building images..."
cd ../docker-compose
docker-compose build

echo ""
echo "📦 Tagging images for Docker Hub..."
docker tag plugin-platform:latest masongraye827/cpg-platform:latest
docker tag logger-plugin:latest masongraye827/cpg-logger-plugin:latest
docker tag storage-plugin:latest masongraye827/cpg-storage-plugin:latest
docker tag api-plugin:latest masongraye827/cpg-api-plugin:latest

echo ""
echo "📦 Pushing images to Docker Hub..."
docker push masongraye827/cpg-platform:latest
docker push masongraye827/cpg-logger-plugin:latest
docker push masongraye827/cpg-storage-plugin:latest
docker push masongraye827/cpg-api-plugin:latest

cd ../helm-chart

echo ""
echo "✅ All images built and pushed!"
echo ""
echo "Next steps:"
echo "  ./install.sh     - Deploy to Kubernetes"
echo "  ./port-forward.sh - Access the API"
echo "  ./test-api.sh    - Test the URL shortener"
echo "  ./uninstall.sh   - Remove deployment"
