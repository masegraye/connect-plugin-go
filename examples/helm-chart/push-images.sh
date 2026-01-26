#!/bin/bash
set -e

echo "📦 Pushing images to local registry (localhost:5001)..."
echo ""

# Tag images for local registry with better names
echo "Tagging images..."
docker tag docker-compose-host localhost:5001/plugin-platform:latest
docker tag docker-compose-logger localhost:5001/logger-plugin:latest
docker tag docker-compose-storage localhost:5001/storage-plugin:latest
docker tag docker-compose-api localhost:5001/api-plugin:latest

echo ""
echo "Pushing images..."
docker push localhost:5001/plugin-platform:latest
docker push localhost:5001/logger-plugin:latest
docker push localhost:5001/storage-plugin:latest
docker push localhost:5001/api-plugin:latest

echo ""
echo "✅ All images pushed to localhost:5001"
echo ""
echo "Images available:"
echo "  localhost:5001/plugin-platform:latest"
echo "  localhost:5001/logger-plugin:latest"
echo "  localhost:5001/storage-plugin:latest"
echo "  localhost:5001/api-plugin:latest"
echo ""
echo "Next step:"
echo "  ./install.sh"
