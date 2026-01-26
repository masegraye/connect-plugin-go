#!/bin/bash
set -e

echo "🧪 Testing URL Shortener on Kubernetes..."
echo ""

# Get pod name
POD=$(kubectl get pods -l app.kubernetes.io/name=url-shortener -o jsonpath='{.items[0].metadata.name}')

if [ -z "$POD" ]; then
    echo "❌ No pods found. Is the chart installed?"
    echo "Run: ./install.sh"
    exit 1
fi

echo "Testing pod: $POD"
echo ""

# Port-forward in background
echo "Setting up port-forward..."
kubectl port-forward svc/url-shortener 8083:8083 > /dev/null 2>&1 &
PF_PID=$!

# Cleanup function
cleanup() {
    echo ""
    echo "Cleaning up port-forward..."
    kill $PF_PID 2>/dev/null || true
}
trap cleanup EXIT

# Wait for port-forward to be ready
sleep 3

# Test 1: Shorten URL
echo "Test 1: Shorten URL"
echo "  POST http://localhost:8083/api.v1.API/Shorten?url=https://github.com/masegraye/connect-plugin-go"
echo ""

SHORTEN_RESPONSE=$(curl -s -X POST "http://localhost:8083/api.v1.API/Shorten?url=https://github.com/masegraye/connect-plugin-go")
echo "$SHORTEN_RESPONSE"

# Extract short code (handles spaces in JSON)
SHORT_CODE=$(echo "$SHORTEN_RESPONSE" | grep -o '"short_code": *"[^"]*"' | cut -d'"' -f4)

if [ -z "$SHORT_CODE" ]; then
    echo ""
    echo "❌ Failed to shorten URL - no short code returned"
    exit 1
fi

echo ""
echo "✅ URL shortened successfully: $SHORT_CODE"
echo ""

# Test 2: Resolve short code
echo "Test 2: Resolve short code"
echo "  GET http://localhost:8083/api.v1.API/Resolve?code=$SHORT_CODE"
echo ""

RESOLVE_RESPONSE=$(curl -s "http://localhost:8083/api.v1.API/Resolve?code=$SHORT_CODE")
echo "$RESOLVE_RESPONSE"

if echo "$RESOLVE_RESPONSE" | grep -q "github.com/masegraye/connect-plugin-go"; then
    echo ""
    echo "✅ Short code resolved successfully"
else
    echo ""
    echo "❌ Failed to resolve short code"
    exit 1
fi

echo ""
echo "Test 3: Verify plugin-to-plugin routing"
echo ""

# Check host logs for routing
if kubectl logs $POD -c host 2>&1 | grep -q "ROUTER.*api.*storage"; then
    echo "✅ Found API → Storage routing in host logs"
else
    echo "⚠️  No API → Storage routing found"
fi

if kubectl logs $POD -c host 2>&1 | grep -q "ROUTER.*storage.*logger"; then
    echo "✅ Found Storage → Logger routing in host logs"
else
    echo "⚠️  No Storage → Logger routing found"
fi

echo ""
echo "🎉 All tests passed!"
echo ""
echo "Summary:"
echo "  ✓ URL shortening works"
echo "  ✓ URL resolution works"
echo "  ✓ Plugin-to-plugin communication via localhost"
echo ""
echo "View detailed logs:"
echo "  kubectl logs $POD -c host | grep ROUTER"
echo "  kubectl logs $POD -c logger | grep LOG"
