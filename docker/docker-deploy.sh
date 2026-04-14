#!/bin/bash

# Docker Deployment Helper Script
# This script helps you quickly set up and deploy ThinLine Radio using Docker

set -e

# Change to the docker/ directory so compose and relative paths work correctly
cd "$(dirname "$0")"

# Colors for output
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m' # No Color

echo -e "${GREEN}ThinLine Radio - Docker Deployment Helper${NC}"
echo "==========================================="
echo ""

# Check if Docker is installed
if ! command -v docker &> /dev/null; then
    echo -e "${RED}ERROR: Docker is not installed${NC}"
    echo "Please install Docker: https://docs.docker.com/get-docker/"
    exit 1
fi

# Check if Docker Compose is installed
if ! docker compose version &> /dev/null; then
    echo -e "${RED}ERROR: Docker Compose is not installed${NC}"
    echo "Please install Docker Compose: https://docs.docker.com/compose/install/"
    exit 1
fi

echo -e "${GREEN}✓ Docker installed:${NC} $(docker --version)"
echo -e "${GREEN}✓ Docker Compose installed:${NC} $(docker compose version)"
echo ""

# Docker Compose loads `.env` from this directory (same folder as docker-compose.yml).
ENV_FILE=".env"

# One-time migration: older docs put .env at repository root
if [ ! -f "$ENV_FILE" ] && [ -f ../.env ]; then
    echo -e "${YELLOW}Found .env at repository root; copying to docker/.env for Compose.${NC}"
    cp ../.env "$ENV_FILE"
fi

if [ ! -f "$ENV_FILE" ]; then
    echo -e "${YELLOW}Creating .env file from template...${NC}"

    if [ -f env.docker.example ]; then
        cp env.docker.example "$ENV_FILE"
        echo -e "${GREEN}✓ Created docker/.env${NC}"
        echo ""
        echo -e "${YELLOW}IMPORTANT: Edit docker/.env and set a secure DB_PASS (database password).${NC}"
        echo "Edit now? (y/n)"
        read -r response
        if [[ "$response" =~ ^([yY][eE][sS]|[yY])$ ]]; then
            ${EDITOR:-nano} "$ENV_FILE"
        else
            echo -e "${RED}Please edit docker/.env before continuing${NC}"
            exit 1
        fi
    else
        echo -e "${RED}ERROR: env.docker.example not found in $(pwd)${NC}"
        exit 1
    fi
else
    echo -e "${GREEN}✓ .env file exists ($(pwd)/$ENV_FILE)${NC}"

    if grep -q '^[[:space:]]*DB_PASS=[[:space:]]*$' "$ENV_FILE" 2>/dev/null || ! grep -q '^DB_PASS=' "$ENV_FILE" 2>/dev/null; then
        echo -e "${RED}ERROR: DB_PASS is missing or empty in $ENV_FILE${NC}"
        echo "Set DB_PASS to a strong password, then run this script again."
        exit 1
    fi

    if grep -q "change_this_password_immediately" "$ENV_FILE"; then
        echo -e "${RED}WARNING: Default database password detected!${NC}"
        echo "You MUST change DB_PASS in docker/.env"
        echo "Edit now? (y/n)"
        read -r response
        if [[ "$response" =~ ^([yY][eE][sS]|[yY])$ ]]; then
            ${EDITOR:-nano} "$ENV_FILE"
        else
            echo -e "${RED}Please edit docker/.env before continuing${NC}"
            exit 1
        fi
    fi
fi

echo ""

# Create required directories
echo -e "${YELLOW}Creating required directories...${NC}"

mkdir -p data/postgres
mkdir -p data/thinline
mkdir -p data/logs
mkdir -p config/ssl
mkdir -p config/credentials
mkdir -p init-db

# Set permissions
chmod -R 755 data/ config/ init-db/

echo -e "${GREEN}✓ Directories created${NC}"
echo ""

# Check if containers are already running
if docker compose ps | grep -q "Up"; then
    echo -e "${YELLOW}Containers are already running${NC}"
    echo "What would you like to do?"
    echo "  1) Restart containers"
    echo "  2) Rebuild and restart"
    echo "  3) Stop containers"
    echo "  4) View logs"
    echo "  5) Exit"
    read -r choice
    
    case $choice in
        1)
            echo -e "${YELLOW}Restarting containers...${NC}"
            docker compose restart
            ;;
        2)
            echo -e "${YELLOW}Rebuilding and restarting...${NC}"
            docker compose up -d --build
            ;;
        3)
            echo -e "${YELLOW}Stopping containers...${NC}"
            docker compose down
            exit 0
            ;;
        4)
            echo -e "${YELLOW}Viewing logs (Ctrl+C to exit)...${NC}"
            docker compose logs -f
            exit 0
            ;;
        5)
            exit 0
            ;;
        *)
            echo -e "${RED}Invalid choice${NC}"
            exit 1
            ;;
    esac
else
    # Start containers
    echo -e "${YELLOW}Building and starting Docker containers...${NC}"
    echo "This may take several minutes on first run..."
    echo ""
    
    docker compose up -d
fi

echo ""
echo -e "${GREEN}=========================================${NC}"
echo -e "${GREEN}✓ ThinLine Radio is starting!${NC}"
echo -e "${GREEN}=========================================${NC}"
echo ""
echo "Waiting for services to be ready..."

# Wait for health checks
sleep 5

# Check status
echo ""
echo "Container Status:"
docker compose ps

echo ""
echo -e "${GREEN}Access ThinLine Radio:${NC}"
echo "  Web Interface: http://localhost:3000/"
echo "  Admin Dashboard: http://localhost:3000/admin"
echo ""
echo -e "${YELLOW}Default admin password: admin${NC}"
echo -e "${RED}CHANGE THIS IMMEDIATELY after first login!${NC}"
echo ""
echo "Useful commands:"
echo "  View logs:        docker compose logs -f"
echo "  Stop containers:  docker compose down"
echo "  Restart:          docker compose restart"
echo "  Status:           docker compose ps"
echo ""
echo "Configuration file: $(pwd)/.env"
echo "For more information, see: docker/README.md"
echo ""

