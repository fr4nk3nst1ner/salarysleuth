#!/bin/bash
#
# SalarySleuth Job Tracker - Weekly Runner Script
# This script runs the job scraper, filters for OSCP/offsec roles,
# and sends Telegram notifications for new positions.
#
# Usage:
#   ./run-tracker.sh              # Run scraper only
#   ./run-tracker.sh --web        # Start web server after scraping
#   ./run-tracker.sh --web-only   # Run web server only (no scraping)
#

set -e

# Configuration
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(dirname "$SCRIPT_DIR")"
LOG_FILE="$SCRIPT_DIR/data/tracker.log"

# Telegram credentials (set via environment variables or .env file)
# These must be set externally - no defaults for security
if [ -f "$SCRIPT_DIR/.env" ]; then
    source "$SCRIPT_DIR/.env"
fi

# Ensure data directory exists
mkdir -p "$SCRIPT_DIR/data"

# Log function
log() {
    echo "[$(date '+%Y-%m-%d %H:%M:%S')] $1" | tee -a "$LOG_FILE"
}

# Parse arguments
RUN_WEB=false
WEB_ONLY=false
PAGES=20
DESCRIPTION="Offensive Security"

while [[ $# -gt 0 ]]; do
    case $1 in
        --web)
            RUN_WEB=true
            shift
            ;;
        --web-only)
            WEB_ONLY=true
            shift
            ;;
        --pages)
            PAGES="$2"
            shift 2
            ;;
        --description)
            DESCRIPTION="$2"
            shift 2
            ;;
        *)
            echo "Unknown option: $1"
            exit 1
            ;;
    esac
done

log "=========================================="
log "SalarySleuth Job Tracker Starting"
log "=========================================="

cd "$SCRIPT_DIR"

if [ "$WEB_ONLY" = true ]; then
    log "Starting web server only..."
    exec go run . -web
fi

# Run the scraper
log "Running job scraper (pages: $PAGES, description: $DESCRIPTION)..."
go run . -scrape -pages "$PAGES" -description "$DESCRIPTION"

log "Scraper complete!"

# Optionally start web server
if [ "$RUN_WEB" = true ]; then
    log "Starting web server..."
    exec go run . -web
fi

log "Job tracker run complete"
log "=========================================="
