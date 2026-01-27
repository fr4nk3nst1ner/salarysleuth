#!/bin/bash
set -e

DATA_DIR="/app/data"

# Ensure data directory exists and is writable
mkdir -p "$DATA_DIR"

# Function to run the scraper
run_scraper() {
    echo "[$(date)] Running job scraper..."
    cd /app
    /app/jobtracker -scrape \
        -pages "${SCRAPE_PAGES:-20}" \
        -description "${SCRAPE_DESCRIPTION:-Offensive Security}"
    echo "[$(date)] Scraper complete"
}

# Function to start the web server
start_web() {
    echo "[$(date)] Starting web server on port ${WEB_PORT:-8080}..."
    cd /app
    exec /app/jobtracker -web -port "${WEB_PORT:-8080}"
}

# Handle different modes
case "${1:-web}" in
    scrape)
        # Run scraper once and exit
        run_scraper
        ;;
    web)
        # Start web server only
        start_web
        ;;
    scrape-and-web)
        # Run scraper then start web server
        run_scraper
        start_web
        ;;
    *)
        echo "Usage: docker run ... [scrape|web|scrape-and-web]"
        echo ""
        echo "Modes:"
        echo "  scrape         - Run scraper once and exit"
        echo "  web            - Start web server only (default)"
        echo "  scrape-and-web - Run scraper then start web server"
        echo ""
        echo "Environment variables:"
        echo "  TELEGRAM_BOT_TOKEN    - Telegram bot token for notifications"
        echo "  TELEGRAM_CHAT_ID      - Telegram chat ID for notifications"
        echo "  WEB_PORT              - Web server port (default: 8080)"
        echo "  SCRAPE_PAGES          - Number of pages to scrape (default: 20)"
        echo "  SCRAPE_DESCRIPTION    - Job search description (default: 'Offensive Security')"
        echo ""
        echo "For scheduled scraping, use host cron or systemd timer to run:"
        echo "  docker compose run --rm scraper scrape"
        exit 1
        ;;
esac
