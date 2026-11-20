#!/usr/bin/env bash
set -euo pipefail

# ==============================================================================
# FleetDeck — 1-Line Zero-Config Installer
# Works on: Ubuntu, Debian, Rocky Linux, AlmaLinux, Arch, Alpine, and macOS
# ==============================================================================

BOLD='\033[1m'
BLUE='\033[0;34m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

echo -e "${BLUE}${BOLD}"
cat << 'EOF'
  _____ _           _   _____            _    
 |  ___| | ___  ___| |_|  __ \          | |   
 | |_  | |/ _ \/ _ \ __| |  | | ___  ___| | __
 |  _| | |  __/  __/ |_| |  | |/ _ \/ __| |/ /
 |_|   |_|\___|\___|\__| |__| |  __/ (__|   < 
                       |_____/ \___|\___|_|\_\
          Self-Hosted Agentless PaaS
EOF
echo -e "${NC}"

# 1. Detect Architecture & Environment
ARCH=$(uname -m)
case "$ARCH" in
  x86_64) ARCH="amd64" ;;
  aarch64|arm64) ARCH="arm64" ;;
  *) echo -e "${YELLOW}Unsupported architecture: $ARCH${NC}"; exit 1 ;;
esac

echo -e "-> Checking environment and dependencies..."

# 2. Check / Auto-Install Docker if missing
if ! command -v docker &> /dev/null; then
  echo -e "-> ${YELLOW}Docker not found. Installing Docker Engine automatically...${NC}"
  curl -fsSL https://get.docker.com | sh
  systemctl enable --now docker || true
  echo -e "-> ${GREEN}Docker installed successfully!${NC}"
else
  echo -e "-> ${GREEN}Docker is already installed.${NC}"
fi

# 3. Detect Public / Local IP Address (Zero-Domain Magic DNS)
SERVER_IP=$(curl -s -4 ifconfig.me || curl -s -4 icanhazip.com || echo "127.0.0.1")
MAGIC_DOMAIN="${SERVER_IP}.sslip.io"

echo -e "-> Detected Server IP: ${BOLD}${SERVER_IP}${NC}"
echo -e "-> Auto-generated Zero-Config Domain: ${BOLD}fleet.${MAGIC_DOMAIN}${NC}"

# 4. Prepare Directories
INSTALL_DIR="/var/lib/fleetdeck"
CONFIG_DIR="/etc/fleetdeck"
mkdir -p "${INSTALL_DIR}/data" "${CONFIG_DIR}"

# 5. Generate Initial Config if not present
if [ ! -f "${CONFIG_DIR}/fleet.yaml" ]; then
  cat << EOF > "${CONFIG_DIR}/fleet.yaml"
server:
  host: "0.0.0.0"
  port: 8089
  poll_interval_seconds: 300
  auth_username: ""
  auth_password: ""

nodes:
  - id: "local-node"
    name: "Primary Server"
    provider: "local_docker"
    tags: ["primary"]
    ingress_domain: "*.${MAGIC_DOMAIN}"
    direct_url: "http://${SERVER_IP}:8089"
    docker_access:
      driver: "local_socket"
      target: "/var/run/docker.sock"
EOF
fi

# 6. Generate Docker Compose Stack (Includes Traefik Reverse Proxy)
cat << EOF > "${INSTALL_DIR}/docker-compose.yml"
services:
  traefik:
    image: traefik:v3.2
    container_name: fleetdeck-traefik
    restart: unless-stopped
    command:
      - "--api.insecure=true"
      - "--providers.docker=true"
      - "--providers.docker.exposedbydefault=false"
      - "--entrypoints.web.address=:80"
    ports:
      - "80:80"
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock:ro
    networks:
      - gateway

  fleetdeck:
    image: ghcr.io/abibinyun/fleetdeck:latest
    container_name: fleetdeck
    restart: unless-stopped
    ports:
      - "8089:8089"
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock
      - ${CONFIG_DIR}/fleet.yaml:/etc/fleetdeck/fleet.yaml:ro
      - ${INSTALL_DIR}/data:/data
      - ~/.ssh:/root/.ssh:ro
    networks:
      - gateway
    labels:
      - "traefik.enable=true"
      - "traefik.http.routers.fleetdeck.rule=Host(\`fleet.${MAGIC_DOMAIN}\`) || Host(\`${SERVER_IP}\`)"
      - "traefik.http.routers.fleetdeck.entrypoints=web"
      - "traefik.http.services.fleetdeck.loadbalancer.server.port=8089"

networks:
  gateway:
    name: gateway
    driver: bridge
EOF

# 7. Launch FleetDeck Stack
echo -e "-> Starting FleetDeck Control Plane..."
cd "${INSTALL_DIR}"
if command -v docker-compose &> /dev/null; then
  docker-compose up -d
else
  docker compose up -d
fi

echo -e "\n${GREEN}${BOLD}================================================================${NC}"
echo -e "${GREEN}${BOLD}  🎉 FleetDeck is live and ready!${NC}"
echo -e "${GREEN}${BOLD}================================================================${NC}"
echo -e "Open your browser at:"
echo -e "  🌐 Direct IP Access : ${BOLD}http://${SERVER_IP}:8089${NC}"
echo -e "  🌐 Zero-DNS URL     : ${BOLD}http://fleet.${MAGIC_DOMAIN}${NC}"
echo -e ""
echo -e "Apps you deploy will automatically be accessible at:"
echo -e "  👉 ${BOLD}http://[app-name].${MAGIC_DOMAIN}${NC} (No domain purchase required!)"
echo -e "================================================================\n"
