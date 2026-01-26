#!/bin/bash
set -e

echo "📦 Deploying URL Shortener to Kubernetes..."
echo ""

# Check if already installed
if helm list | grep -q url-shortener; then
    echo "⚠️  url-shortener already installed"
    echo "Uninstall first with: ./uninstall.sh"
    exit 1
fi

# Install the chart
helm install url-shortener . --wait --timeout 90s

echo ""
echo "✅ Deployment successful!"
echo ""

# Get pod name
POD=$(kubectl get pods -l app.kubernetes.io/name=url-shortener -o jsonpath='{.items[0].metadata.name}')
echo "Pod: $POD"
echo ""

# Show container status
kubectl get pods -l app.kubernetes.io/name=url-shortener

echo ""
echo "View logs:"
echo "  kubectl logs $POD --all-containers"
echo "  kubectl logs $POD -c host | grep ROUTER"
echo "  kubectl logs $POD -c logger | grep LOG"
echo ""
echo "Access the API:"
echo "  ./port-forward.sh  # In one terminal"
echo "  ./test-api.sh      # In another terminal"
echo ""
echo "Run full test:"
echo "  ./test.sh          # Includes built-in port-forward"
echo ""
echo "Uninstall:"
echo "  ./uninstall.sh"
