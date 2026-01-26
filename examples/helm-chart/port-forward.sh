#!/bin/bash
set -e

echo "🔌 Setting up port-forward to URL Shortener..."
echo ""

# Check if release exists
if ! helm list | grep -q url-shortener; then
    echo "❌ url-shortener release not found"
    echo "Install first with: ./install.sh"
    exit 1
fi

# Get pod name
POD=$(kubectl get pods -l app.kubernetes.io/name=url-shortener -o jsonpath='{.items[0].metadata.name}')

if [ -z "$POD" ]; then
    echo "❌ No pods found for url-shortener"
    exit 1
fi

echo "Pod: $POD"
echo ""
echo "🔌 Port-forwarding svc/url-shortener 8083:8083"
echo ""
echo "API available at: http://localhost:8083"
echo ""
echo "Test in another terminal:"
echo "  ./test-api.sh"
echo ""
echo "Press Ctrl+C to stop port-forwarding"
echo ""

kubectl port-forward svc/url-shortener 8083:8083
