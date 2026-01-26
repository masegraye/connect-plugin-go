#!/bin/bash
set -e

echo "📦 Installing URL Shortener Helm chart..."
echo ""

# Install the chart
helm install url-shortener . --wait --timeout 2m

echo ""
echo "✅ Chart installed successfully!"
echo ""

# Get pod name
POD=$(kubectl get pods -l app.kubernetes.io/name=url-shortener -o jsonpath='{.items[0].metadata.name}')

echo "Pod: $POD"
echo ""
echo "Waiting for all containers to be ready..."
kubectl wait --for=condition=ready pod/$POD --timeout=60s

echo ""
echo "✅ All containers ready!"
echo ""
echo "View logs:"
echo "  kubectl logs $POD --all-containers"
echo "  kubectl logs $POD -c host | grep ROUTER"
echo ""
echo "Test the URL shortener:"
echo "  ./test.sh"
echo ""
echo "Uninstall:"
echo "  ./uninstall.sh"
