#!/bin/bash
set -e

echo "🚀 Starting URL Shortener services..."
echo ""

# Start services in detached mode
docker-compose up -d

echo ""
echo "⏳ Waiting for services to be ready..."
echo ""

# Wait for host to be healthy
timeout=30
elapsed=0
while ! docker-compose exec -T host wget -q -O- http://localhost:8080/health > /dev/null 2>&1; do
    if [ $elapsed -ge $timeout ]; then
        echo "❌ Timeout waiting for host to be ready"
        docker-compose logs host
        exit 1
    fi
    sleep 1
    elapsed=$((elapsed + 1))
    echo -n "."
done

echo ""
echo "✅ Host platform ready"

# Give plugins time to register and report health
echo "⏳ Waiting for plugins to register..."
sleep 5

echo ""
echo "✅ All services running!"
echo ""
echo "Service Status:"
docker-compose ps
echo ""
echo "Plugin Logs:"
echo "  docker-compose logs -f logger"
echo "  docker-compose logs -f storage"
echo "  docker-compose logs -f api"
echo "  docker-compose logs -f host"
echo ""
echo "Test the URL shortener:"
echo "  ./test.sh"
echo ""
echo "Stop services:"
echo "  ./cleanup.sh"
