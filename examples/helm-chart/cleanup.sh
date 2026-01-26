#!/bin/bash
set -e

echo "🧹 Cleaning up URL Shortener from Kubernetes..."
echo ""

# Uninstall if exists
if helm list | grep -q url-shortener; then
    echo "Uninstalling Helm release..."
    helm uninstall url-shortener
    echo "✅ Helm release removed"
else
    echo "ℹ️  url-shortener release not found (already removed)"
fi

echo ""
echo "✅ Cleanup complete!"
echo ""
echo "To redeploy:"
echo "  ./install.sh"
