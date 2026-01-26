#!/bin/bash
set -e

echo "🚀 Running URL Shortener on Kubernetes..."
echo ""

# Check if images are available locally or in registry
echo "Checking for local images..."
if ! docker images | grep -q "plugin-platform.*latest"; then
    echo "⚠️  Local images not found"
    echo ""
    echo "Building images..."
    cd ../docker-compose
    docker-compose build
    cd ../helm-chart
    echo "✅ Images built"
else
    echo "✅ Local images found"
fi

echo ""

# Check if Helm chart is installed
if helm list | grep -q url-shortener; then
    echo "ℹ️  url-shortener already installed"
    echo ""
    POD=$(kubectl get pods -l app.kubernetes.io/name=url-shortener -o jsonpath='{.items[0].metadata.name}')
    kubectl get pods -l app.kubernetes.io/name=url-shortener
else
    echo "Installing Helm chart..."
    ./install.sh
fi

echo ""
echo "✅ URL Shortener is running!"
echo ""
echo "Next steps:"
echo "  ./port-forward.sh  # Access API (Ctrl+C to stop)"
echo "  ./test-api.sh      # Test in another terminal"
echo "  ./test.sh          # Run full automated test"
echo "  ./cleanup.sh       # Remove deployment"
