# ThinLine Radio - Docker Deployment Guide

This guide covers deploying ThinLine Radio using Docker and Docker Compose.

## Table of Contents

1. [Quick Start](#quick-start)
2. [Prerequisites](#prerequisites)
3. [Installation](#installation)
4. [Configuration](#configuration)
5. [Running the Containers](#running-the-containers)
6. [Accessing ThinLine Radio](#accessing-thinline-radio)
7. [Data Persistence](#data-persistence)
8. [Maintenance](#maintenance)
9. [Troubleshooting](#troubleshooting)
10. [Production Deployment](#production-deployment)

---

## Quick Start

For those who want to get started immediately:

```bash
# 1. Clone or download the repository
git clone https://github.com/Thinline-Dynamic-Solutions/ThinLineRadio.git
cd ThinLineRadio

# 2. Copy and configure environment file
cp .env.docker.example .env
nano .env  # Edit DB_PASS and other settings

# 3. Create data directories
mkdir -p docker/data/postgres docker/data/thinline docker/data/logs
mkdir -p docker/config docker/init-db

# 4. Build and start containers
docker-compose up -d

# 5. View logs
docker-compose logs -f

# 6. Access the admin dashboard
# Open http://localhost:3000/admin
# Default password: admin (change immediately!)
```

---

## Prerequisites

### System Requirements

- **Docker**: Version 20.10 or later
- **Docker Compose**: Version 2.0 or later
- **Operating System**: Linux, macOS, or Windows with WSL2
- **Disk Space**: Minimum 10GB free (audio files can grow large)
- **Memory**: Minimum 4GB RAM (8GB recommended)
- **CPU**: 2+ cores recommended

### Installing Docker

#### Linux (Ubuntu/Debian)
```bash
# Install Docker
curl -fsSL https://get.docker.com -o get-docker.sh
sudo sh get-docker.sh

# Install Docker Compose
sudo apt-get update
sudo apt-get install docker-compose-plugin

# Add your user to docker group
sudo usermod -aG docker $USER
newgrp docker
```

#### macOS
```bash
# Install Docker Desktop
# Download from: https://www.docker.com/products/docker-desktop/

# Or using Homebrew
brew install --cask docker
```

#### Windows
```bash
# Install Docker Desktop for Windows
# Download from: https://www.docker.com/products/docker-desktop/

# Requires WSL2 backend
# Follow: https://docs.docker.com/desktop/install/windows-install/
```

### Verify Installation
```bash
docker --version
docker compose version
```

---

## Installation

### 1. Obtain ThinLine Radio

**Option A: Clone from Git (if available)**
```bash
git clone https://github.com/Thinline-Dynamic-Solutions/ThinLineRadio.git
cd ThinLineRadio
```

**Option B: Download Release Package**
```bash
# Download the latest release
wget https://github.com/Thinline-Dynamic-Solutions/ThinLineRadio/releases/latest/download/thinline-radio-source.tar.gz

# Extract
tar -xzf thinline-radio-source.tar.gz
cd ThinLineRadio
```

### 2. Create Directory Structure

```bash
# Create data directories
mkdir -p docker/data/postgres
mkdir -p docker/data/thinline
mkdir -p docker/data/logs

# Create config directories
mkdir -p docker/config/ssl
mkdir -p docker/config/credentials

# Create initialization directory
mkdir -p docker/init-db

# Set proper permissions
chmod -R 755 docker/
```

### 3. Configure Environment

```bash
# Copy example environment file
cp .env.docker.example .env

# Edit with your settings
nano .env  # or use your preferred editor
```

**At minimum, change these values in `.env`:**
- `DB_PASS`: Set a strong database password
- `TZ`: Set your timezone
- `DATA_PATH`: Verify the path is correct

---

## Configuration

### Database Configuration

The PostgreSQL database is automatically configured via environment variables in `.env`:

```bash
DB_NAME=thinline_radio
DB_USER=thinline_user
DB_PASS=your_secure_password_here  # CHANGE THIS!
```

### Server Configuration

Basic server settings in `.env`:

```bash
HTTP_PORT=3000   # HTTP port
HTTPS_PORT=3443  # HTTPS port (optional)
TZ=America/New_York  # Your timezone
```

### SSL/HTTPS Configuration (Optional)

**Option A: Let's Encrypt (Recommended for production)**

1. Ensure your domain points to your server
2. Update `.env`:
   ```bash
   SSL_AUTO_CERT=yourdomain.com
   ```
3. Ensure port 443 is forwarded to 3443

**Option B: Custom Certificates**

1. Place certificates in `docker/config/ssl/`:
   ```bash
   docker/config/ssl/cert.pem
   docker/config/ssl/key.pem
   ```

2. Update `docker-compose.yml` to uncomment SSL volume mount

### Transcription Services (Optional)

See [Transcription Configuration](#transcription-configuration) below.

---

## Running the Containers

### Start Services

```bash
# Start in detached mode (background)
docker-compose up -d

# Or start with logs visible
docker-compose up
```

### View Logs

```bash
# All services
docker-compose logs -f

# ThinLine Radio only
docker-compose logs -f thinline-radio

# PostgreSQL only
docker-compose logs -f postgres

# Last 100 lines
docker-compose logs --tail=100
```

### Stop Services

```bash
# Stop containers (data is preserved)
docker-compose down

# Stop and remove volumes (DELETES ALL DATA!)
docker-compose down -v
```

### Restart Services

```bash
# Restart all services
docker-compose restart

# Restart specific service
docker-compose restart thinline-radio
```

---

## Accessing ThinLine Radio

### Admin Dashboard

1. Open your browser and navigate to:
   ```
   http://localhost:3000/admin
   ```
   
2. **Default credentials:**
   - Password: `admin`
   
3. **IMPORTANT:** Change the admin password immediately:
   - Go to Admin → Settings → Security
   - Or use command line:
     ```bash
     docker-compose exec thinline-radio /app/thinline-radio -admin_password new_secure_password
     ```

### Web Application

Access the scanner interface at:
```
http://localhost:3000/
```

### From External Network

If accessing from another computer:
```
http://your-server-ip:3000/
```

Make sure to:
1. Configure firewall to allow port 3000
2. Consider using a reverse proxy (nginx/traefik) for SSL

---

## Data Persistence

### Volume Locations

By default, data is stored in:
```
docker/data/
├── postgres/     # Database files
├── thinline/     # Audio files and application data
└── logs/         # Application logs
```

### Backup

**Backup Database:**
```bash
# Create backup
docker-compose exec postgres pg_dump -U thinline_user thinline_radio > backup.sql

# Or using docker directly
docker exec thinline-postgres pg_dump -U thinline_user thinline_radio > backup.sql
```

**Backup Audio Files:**
```bash
# Create tar archive of audio files
tar -czf thinline-audio-backup.tar.gz docker/data/thinline/
```

**Automated Backup Script:**
```bash
#!/bin/bash
# backup.sh - Run daily via cron

BACKUP_DIR="/backups/thinline"
DATE=$(date +%Y%m%d-%H%M%S)

mkdir -p $BACKUP_DIR

# Backup database
docker exec thinline-postgres pg_dump -U thinline_user thinline_radio | \
  gzip > $BACKUP_DIR/db-$DATE.sql.gz

# Backup audio (if not too large)
tar -czf $BACKUP_DIR/audio-$DATE.tar.gz docker/data/thinline/

# Keep only last 7 days
find $BACKUP_DIR -name "*.gz" -mtime +7 -delete

echo "Backup completed: $DATE"
```

### Restore

**Restore Database:**
```bash
# Stop ThinLine Radio
docker-compose stop thinline-radio

# Restore database
cat backup.sql | docker-compose exec -T postgres psql -U thinline_user thinline_radio

# Restart
docker-compose start thinline-radio
```

**Restore Audio Files:**
```bash
# Stop services
docker-compose down

# Extract backup
tar -xzf thinline-audio-backup.tar.gz

# Restart
docker-compose up -d
```

---

## Maintenance

### Update ThinLine Radio

```bash
# Pull latest changes (if using Git)
git pull origin main

# Change to the Docker directory
cd docker

# Rebuild containers
docker-compose build --no-cache

# Restart with new image
docker-compose up -d

# View logs to ensure successful update
docker-compose logs -f thinline-radio
```

### Update from Docker Hub (if available)

```bash
# Pull latest image
docker-compose pull

# Restart containers
docker-compose up -d
```

### Database Maintenance

```bash
# Access PostgreSQL shell
docker-compose exec postgres psql -U thinline_user thinline_radio

# Run vacuum to reclaim space
docker-compose exec postgres psql -U thinline_user thinline_radio -c "VACUUM FULL;"

# Analyze database for query optimization
docker-compose exec postgres psql -U thinline_user thinline_radio -c "ANALYZE;"
```

### View Resource Usage

```bash
# Container stats
docker stats

# Disk usage
docker system df

# Specific container
docker stats thinline-radio
```

### Clean Up

```bash
# Remove unused images
docker image prune -a

# Remove unused volumes (CAREFUL!)
docker volume prune

# Complete cleanup
docker system prune -a --volumes
```

---

## Troubleshooting

### Container Won't Start

**Check logs:**
```bash
docker-compose logs thinline-radio
docker-compose logs postgres
```

**Common issues:**

1. **Database connection failed:**
   - Verify `DB_PASS` in `.env` matches
   - Check if postgres container is running: `docker-compose ps`
   - Verify network connectivity: `docker-compose exec thinline-radio ping postgres`

2. **Port already in use:**
   ```bash
   # Check what's using port 3000
   sudo lsof -i :3000
   
   # Change port in .env
   HTTP_PORT=3001
   ```

3. **Permission denied:**
   ```bash
   # Fix permissions
   sudo chown -R 1000:1000 docker/data/
   chmod -R 755 docker/data/
   ```

### Database Issues

**Reset database (DELETES ALL DATA):**
```bash
# Stop containers
docker-compose down

# Remove database volume
sudo rm -rf docker/data/postgres

# Restart (database will reinitialize)
docker-compose up -d
```

**Connect to database manually:**
```bash
docker-compose exec postgres psql -U thinline_user thinline_radio
```

### Performance Issues

**Increase resources in `docker-compose.yml`:**
```yaml
deploy:
  resources:
    limits:
      cpus: '8'      # Increase CPU limit
      memory: 8G     # Increase memory limit
```

**Check resource usage:**
```bash
docker stats
```

### Audio Processing Issues

**Verify FFmpeg is working:**
```bash
docker-compose exec thinline-radio ffmpeg -version
docker-compose exec thinline-radio ffprobe -version
```

### Network Issues

**Check container networking:**
```bash
# List networks
docker network ls

# Inspect network
docker network inspect thinlineradio_thinline_network

# Test connectivity
docker-compose exec thinline-radio ping postgres
```

---

## Production Deployment

### Reverse Proxy Setup

**Example: Nginx with SSL**

Create `nginx.conf`:
```nginx
server {
    listen 80;
    server_name yourdomain.com;
    return 301 https://$server_name$request_uri;
}

server {
    listen 443 ssl http2;
    server_name yourdomain.com;

    ssl_certificate /etc/nginx/ssl/cert.pem;
    ssl_certificate_key /etc/nginx/ssl/key.pem;
    ssl_protocols TLSv1.2 TLSv1.3;
    ssl_ciphers HIGH:!aNULL:!MD5;

    client_max_body_size 50M;

    location / {
        proxy_pass http://localhost:3000;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        
        # WebSocket support
        proxy_read_timeout 86400;
    }
}
```

### Security Hardening

1. **Use strong passwords** in `.env`
2. **Change default admin password** immediately
3. **Run behind reverse proxy** with SSL
4. **Enable firewall** and limit exposed ports
5. **Regular updates** and security patches
6. **Monitor logs** for suspicious activity
7. **Implement rate limiting** at reverse proxy level

### Monitoring

**Setup health checks:**
```bash
# Manual health check
curl http://localhost:3000/

# Automated monitoring with uptime check
*/5 * * * * curl -f http://localhost:3000/ || echo "ThinLine Radio is down!"
```

**Log aggregation:**
- Consider using tools like ELK stack, Graylog, or Splunk
- Docker logs are in JSON format: `docker-compose logs --json`

### Scaling

For high-traffic deployments:

1. **Increase database connections** in `docker-compose.yml`:
   ```yaml
   POSTGRES_MAX_CONNECTIONS: 400
   ```

2. **Scale ThinLine Radio horizontally** (requires load balancer):
   ```bash
   docker-compose up -d --scale thinline-radio=3
   ```

3. **Use external PostgreSQL** for better performance
4. **Implement caching** (Redis) for session management
5. **Use CDN** for static assets

---

## Advanced Configuration

### Transcription Configuration

**Google Cloud Speech-to-Text:**
```bash
# 1. Place credentials JSON in docker/config/credentials/
cp google-credentials.json docker/config/credentials/

# 2. Uncomment in docker-compose.yml:
#   GOOGLE_APPLICATION_CREDENTIALS: /app/config/credentials/google-credentials.json
```

**Azure Cognitive Services:**
```bash
# Add to .env:
AZURE_SPEECH_KEY=your-key
AZURE_SPEECH_REGION=eastus
```

**OpenAI Whisper API:**
```bash
# Add to .env:
WHISPER_API_KEY=sk-...
```

**AssemblyAI:**
```bash
# Add to .env:
ASSEMBLYAI_API_KEY=your-key
```

### Custom Initialization

Place SQL scripts in `docker/init-db/` to run on first startup:

```bash
# Example: docker/init-db/01-custom-schema.sql
CREATE INDEX IF NOT EXISTS idx_calls_timestamp ON calls(timestamp);
```

### Environment-Specific Configs

Use different compose files:

```bash
# Development
docker-compose -f docker-compose.yml -f docker-compose.dev.yml up

# Production
docker-compose -f docker-compose.yml -f docker-compose.prod.yml up
```

---

## Support

For issues specific to Docker deployment:
- Check the [Troubleshooting](#troubleshooting) section above
- Review logs: `docker-compose logs -f`
- Ensure all prerequisites are met

For general ThinLine Radio support:
- Review main documentation: `../docs/setup-and-administration.md`
- Contact: Thinline Dynamic Solutions

---

## License

ThinLine Radio is licensed under the GNU General Public License v3.0 (GPL v3).

**Happy scanning!**

