#!/bin/bash
set -e

echo "🧪 Testing URL Shortener API on Kubernetes..."
echo ""

# Check if API is accessible
if ! curl -s http://localhost:8083/api.v1.API/Shorten > /dev/null 2>&1; then
    echo "❌ API not accessible at http://localhost:8083"
    echo ""
    echo "Make sure port-forward is running:"
    echo "  ./port-forward.sh"
    exit 1
fi

# Test 1: Shorten URL
echo "Test 1: Shorten URL"
echo "  POST http://localhost:8083/api.v1.API/Shorten?url=https://github.com/masegraye/connect-plugin-go"
echo ""

SHORTEN_RESPONSE=$(curl -s -X POST "http://localhost:8083/api.v1.API/Shorten?url=https://github.com/masegraye/connect-plugin-go")
echo "$SHORTEN_RESPONSE"

# Extract short code
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
echo "🎉 All API tests passed!"
echo ""
echo "View plugin-to-plugin routing:"
echo "  kubectl logs -l app.kubernetes.io/name=url-shortener -c host | grep ROUTER"
