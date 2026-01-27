#!/bin/bash
# SalarySleuth Job Tracker - Native Scraper Script
# Runs the scraper natively on the host and processes results

set -e

# Configuration
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
DATA_DIR="$SCRIPT_DIR/data"
LOG_DIR="$SCRIPT_DIR/logs"
TIMESTAMP=$(date +"%Y%m%d_%H%M%S")
LOG_FILE="$LOG_DIR/scraper_$TIMESTAMP.log"

# Load environment variables from .env
if [ -f "$SCRIPT_DIR/.env" ]; then
    export $(grep -v '^#' "$SCRIPT_DIR/.env" | xargs)
fi

# Ensure directories exist
mkdir -p "$DATA_DIR" "$LOG_DIR"

# Function to log messages
log() {
    echo "[$(date '+%Y-%m-%d %H:%M:%S')] $*" | tee -a "$LOG_FILE"
}

log "===== Starting SalarySleuth Job Scraper ====="
log "Project root: $PROJECT_ROOT"
log "Data directory: $DATA_DIR"
log "Log file: $LOG_FILE"

# Set Go environment
export PATH="/usr/local/go/bin:$PATH"

# Build the salarysleuth executable if it doesn't exist
SALARYSLEUTH_BIN="$PROJECT_ROOT/salarysleuth"
if [ ! -f "$SALARYSLEUTH_BIN" ]; then
    log "Building salarysleuth executable..."
    cd "$PROJECT_ROOT"
    go build -o "$SALARYSLEUTH_BIN" ./cmd/salarysleuth/main.go
    if [ $? -ne 0 ]; then
        log "ERROR: Failed to build salarysleuth executable"
        exit 1
    fi
    log "Build successful: $SALARYSLEUTH_BIN"
fi

# Run the jobtracker scraper
log "Running jobtracker scraper..."
cd "$SCRIPT_DIR"
export SALARYSLEUTH_BIN="$SALARYSLEUTH_BIN"
go run . -scrape -pages ${SCRAPE_PAGES:-20} -description "${SCRAPE_DESCRIPTION:-Offensive Security}" 2>&1 | tee -a "$LOG_FILE"

# Check if successful
if [ $? -eq 0 ]; then
    log "===== Scraper completed successfully ====="
    
    # Keep only the last 30 log files
    log "Cleaning up old log files..."
    cd "$LOG_DIR"
    ls -t scraper_*.log | tail -n +31 | xargs -r rm --
    
    exit 0
else
    log "===== Scraper failed ====="
    exit 1
fi
