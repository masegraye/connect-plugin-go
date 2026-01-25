#!/bin/bash
set -e

echo "🔨 Building Docker images for URL Shortener example..."
echo ""

# Build all images
docker-compose build --parallel

echo ""
echo "✅ All images built successfully!"
echo ""
echo "Next steps:"
echo "  ./run.sh     - Start all services"
echo "  ./test.sh    - Test the URL shortener"
echo "  ./cleanup.sh - Stop and remove all containers"
