#!/bin/bash
set -e

echo "🧹 Uninstalling URL Shortener..."
echo ""

helm uninstall url-shortener

echo ""
echo "✅ URL Shortener uninstalled!"
echo ""
echo "To reinstall:"
echo "  ./install.sh"
echo ""
echo "To delete kind cluster:"
echo "  kind delete cluster --name url-shortener"
echo "  docker rm -f kind-registry"
