#!/bin/bash
set -e

echo "🧹 Cleaning up URL Shortener services..."
echo ""

# Stop and remove containers
echo "Stopping containers..."
docker-compose down

# Remove volumes (if any)
echo "Removing volumes..."
docker-compose down -v

# Optional: Remove images (commented out by default)
# Uncomment to remove built images
# echo "Removing images..."
# docker-compose down --rmi all

echo ""
echo "✅ Cleanup complete!"
echo ""
echo "All containers and networks removed."
echo ""
echo "To rebuild and run again:"
echo "  ./setup.sh"
echo "  ./run.sh"
