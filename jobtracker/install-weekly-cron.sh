#!/bin/bash
# Install weekly cron job for SalarySleuth scraper

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SCRAPER_SCRIPT="$SCRIPT_DIR/run-scraper.sh"
CRON_FILE="/tmp/salarysleuth-cron-$$.tmp"

# Check if scraper script exists
if [ ! -f "$SCRAPER_SCRIPT" ]; then
    echo "Error: Scraper script not found at $SCRAPER_SCRIPT"
    exit 1
fi

# Make sure the scraper script is executable
chmod +x "$SCRAPER_SCRIPT"

echo "Installing weekly cron job for SalarySleuth scraper..."
echo ""
echo "Current crontab entries:"
crontab -l 2>/dev/null || echo "(no crontab entries)"
echo ""

# Save current crontab
crontab -l 2>/dev/null > "$CRON_FILE" || touch "$CRON_FILE"

# Remove any existing SalarySleuth entries
sed -i '/salarysleuth.*run-scraper.sh/d' "$CRON_FILE"

# Add new entry - runs every Monday at 9 AM
echo "# SalarySleuth Job Scraper - runs weekly on Mondays at 9 AM" >> "$CRON_FILE"
echo "0 9 * * 1 $SCRAPER_SCRIPT" >> "$CRON_FILE"

# Install the new crontab
crontab "$CRON_FILE"
rm "$CRON_FILE"

echo ""
echo "✓ Cron job installed successfully!"
echo ""
echo "Schedule: Every Monday at 9:00 AM"
echo "Script: $SCRAPER_SCRIPT"
echo ""
echo "New crontab:"
crontab -l | grep -A1 "SalarySleuth"
echo ""
echo "To manually run the scraper: $SCRAPER_SCRIPT"
echo "To edit the cron schedule: crontab -e"
echo "To remove the cron job: crontab -e (then delete the SalarySleuth lines)"
