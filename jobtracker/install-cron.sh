#!/bin/bash
#
# Install weekly cron job for SalarySleuth Job Tracker (Docker version)
#

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# Cron expression for running every Sunday at 9 AM
CRON_SCHEDULE="0 9 * * 0"

# The cron job command - runs Docker scraper
CRON_CMD="$CRON_SCHEDULE cd $SCRIPT_DIR && docker compose run --rm scraper scrape >> $SCRIPT_DIR/data/cron.log 2>&1"

# Ensure data directory exists
mkdir -p "$SCRIPT_DIR/data"

# Check if cron job already exists
if crontab -l 2>/dev/null | grep -q "offsec-jobs"; then
    echo "Cron job already exists. Updating..."
    # Remove old entry and add new one
    (crontab -l 2>/dev/null | grep -v "offsec-jobs\|run-tracker.sh"; echo "$CRON_CMD") | crontab -
else
    echo "Adding new cron job..."
    (crontab -l 2>/dev/null; echo "$CRON_CMD") | crontab -
fi

echo ""
echo "✅ Cron job installed successfully!"
echo ""
echo "Schedule: Every Sunday at 9:00 AM"
echo "Command:  docker compose run --rm scraper scrape"
echo "Log file: $SCRIPT_DIR/data/cron.log"
echo ""
echo "Current crontab:"
crontab -l | grep -E "offsec-jobs|scraper" || echo "(No matching entries found)"
echo ""
echo "To view logs:   tail -f $SCRIPT_DIR/data/cron.log"
echo "To remove:      crontab -e  (and delete the line)"
echo ""
echo "Quick commands:"
echo "  Start web:     docker compose up -d web"
echo "  Run scraper:   docker compose run --rm scraper scrape"
echo "  View logs:     docker compose logs -f web"
echo "  Stop all:      docker compose down"
