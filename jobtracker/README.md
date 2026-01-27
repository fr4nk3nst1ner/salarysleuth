# OffSec Jobs Tracker

Automated offensive security job tracker that scrapes LinkedIn, Greenhouse, and Lever for offensive security positions, filters them based on your criteria, sends Telegram notifications for new jobs, and hosts an interactive web dashboard.

## 🚀 Quick Start

### View the Dashboard
Visit: http://YOURIP:9090/

### Run Scraper Manually
```bash
cd /home/jstines/salarysleuth/jobtracker
./run-scraper.sh
```

### Check Scraper Logs
```bash
ls -lt /home/jstines/salarysleuth/jobtracker/logs/
tail -f /home/jstines/salarysleuth/jobtracker/logs/scraper_*.log
```

## ⚙️ Configuration

### Environment Variables (.env)
Edit `/home/jstines/salarysleuth/jobtracker/.env`:
```bash
# Telegram Bot Configuration
TELEGRAM_BOT_TOKEN=your_bot_token
TELEGRAM_CHAT_ID=your_chat_id

# Scraper Settings
SCRAPE_PAGES=20  # Number of pages to scrape per source
SCRAPE_DESCRIPTION=Offensive Security  # Job search query

# Timezone
TZ=America/Chicago
```

### Job Filtering (config.yaml)
Edit `/home/jstines/salarysleuth/jobtracker/config.yaml` to customize:
- **Categories**: offensive_security, penetration_testing, red_team, appsec, etc.
- **Levels**: executive, management, senior, mid, junior
- **Certifications**: OSCP, OSCE, CISSP, CEH
- **Exclude Rules**: Filter out defensive roles, SWE positions, compliance jobs, etc.

## 📅 Automated Schedule

**Weekly Scraper**: Every Monday at 9:00 AM
- Runs natively on the host (not in Docker for network reliability)
- Processes and filters jobs
- Sends Telegram notifications for new jobs
- Updates the web dashboard

### Manage Cron Job
```bash
# View current schedule
crontab -l

# Edit schedule
crontab -e

# Reinstall cron job (if needed)
/home/jstines/salarysleuth/jobtracker/install-weekly-cron.sh
```

## 🐳 Docker Management

### Web Server Commands
```bash
cd /home/jstines/salarysleuth/jobtracker

# Start web server
docker compose up -d

# Stop web server
docker compose down

# View logs
docker compose logs -f web

# Restart web server
docker compose restart web

# Rebuild after code changes
docker compose down
docker compose build
docker compose up -d
```

### Container Info
- **Container Name**: offsec-jobs-web
- **Port**: 9090
- **Network**: Host mode (accessible at http://YOURIP:9090/)
- **Data Directory**: `/home/jstines/salarysleuth/jobtracker/data` (shared with host)

## 📊 Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                    HOST SYSTEM                              │
│                                                             │
│  ┌─────────────────────────────────────────────┐            │
│  │  CRON (Weekly - Mondays 9 AM)              │            │
│  │  └─> run-scraper.sh                         │            │
│  │       ├─> Build salarysleuth (if needed)    │            │
│  │       ├─> go run jobtracker -scrape         │            │
│  │       │    ├─> Run salarysleuth scraper     │            │
│  │       │    ├─> Filter jobs (config.yaml)    │            │
│  │       │    ├─> Send Telegram notifications  │            │
│  │       │    └─> Save to data/jobs.json       │            │
│  │       └─> Log to logs/scraper_*.log         │            │
│  └─────────────────────────────────────────────┘            │
│                          │                                   │
│                          ▼                                   │
│              ┌────────────────────┐                          │
│              │  data/jobs.json    │◄─────────┐              │
│              └────────────────────┘          │              │
│                          │                   │              │
│  ┌───────────────────────▼───────────────────┴──────┐       │
│  │         DOCKER CONTAINER                         │       │
│  │  ┌────────────────────────────────────────────┐  │       │
│  │  │  Web Server (Go - port 9090)              │  │       │
│  │  │  ├─> Read data/jobs.json                  │  │       │
│  │  │  ├─> Apply filters from config.yaml       │  │       │
│  │  │  └─> Serve interactive dashboard          │  │       │
│  │  └────────────────────────────────────────────┘  │       │
│  └──────────────────────────────────────────────────┘       │
│                          │                                   │
└──────────────────────────┼───────────────────────────────────┘
                           ▼
                   ┌──────────────┐
                   │   Browser    │
                   │  Port 9090   │
                   └──────────────┘
```

## 🎯 Features

### Web Dashboard
- **Interactive Filters**: Category, Experience Level, Certification, Remote Only
- **Sorting Options**: Newest, Highest/Lowest Salary, Company A-Z
- **Job Cards**: Display company, title, location, tags, salary info, and apply links
- **Salary Data**: Shows Levels.fyi averages and posted salary ranges
- **Real-time**: Auto-refreshes every 5 minutes

### Job Filtering
- **Inclusion Rules**: Categories, levels, certifications, remote
- **Exclusion Rules**: Defensive roles, non-security positions, compliance
- **Configurable**: Edit `config.yaml` for easy customization

### Telegram Notifications
- **New Job Alerts**: Notified when new offensive security jobs are found
- **Rate Limited**: 1 message per second to avoid API throttling
- **Rich Info**: Company, title, location, salary (if available)

## 🔧 Troubleshooting

### Scraper Issues
```bash
# Test scraper manually with 1 page
export SCRAPE_PAGES=1
/home/jstines/salarysleuth/jobtracker/run-scraper.sh

# Check latest log
tail -f /home/jstines/salarysleuth/jobtracker/logs/scraper_*.log
```

### Web Server Issues
```bash
# Check if web server is running
docker ps | grep offsec

# View web server logs
docker logs offsec-jobs-web

# Test API
curl http://YOURIP:9090/api/jobs | jq '.jobs | length'

# Test health endpoint
curl http://YOURIP:9090/health
```

### Data Issues
```bash
# Check data file
cat /home/jstines/salarysleuth/jobtracker/data/jobs.json | jq '.jobs | length'

# Backup data
cp /home/jstines/salarysleuth/jobtracker/data/jobs.json ~/jobs_backup_$(date +%Y%m%d).json

# Clear data (start fresh)
echo '{"last_updated":"","jobs":null}' > /home/jstines/salarysleuth/jobtracker/data/jobs.json
```

## 📝 Files & Directories

```
/home/jstines/salarysleuth/
├── jobtracker/
│   ├── .env                      # Environment variables (secrets)
│   ├── config.yaml               # Job filtering configuration
│   ├── run-scraper.sh            # Main scraper script (native)
│   ├── install-weekly-cron.sh    # Cron job installer
│   ├── docker-compose.yml        # Docker config (web only)
│   ├── Dockerfile                # Docker image definition
│   ├── main.go                   # Jobtracker application
│   ├── web.go                    # Web server
│   ├── data/
│   │   └── jobs.json             # Job database
│   └── logs/
│       └── scraper_*.log         # Scraper logs (kept 30 days)
├── cmd/salarysleuth/main.go      # SalarySleuth scraper
└── salarysleuth                  # Compiled scraper binary
```

## 🎓 Usage Examples

### Change Scraper Schedule
```bash
crontab -e
# Change from Monday 9 AM to Wednesday 6 PM:
# 0 18 * * 3 /home/jstines/salarysleuth/jobtracker/run-scraper.sh
```

### Add More Job Categories
Edit `config.yaml`:
```yaml
filters:
  categories:
    threat_hunting:
      keywords: ["threat hunt", "threat hunter", "threat hunting"]
      display_name: "Threat Hunting"
```

### Exclude More Job Types
Edit `config.yaml`:
```yaml
filters:
  exclude:
    sales_roles:
      keywords: ["sales engineer", "account executive", "business development"]
```

### Change Telegram Notifications
Edit `.env`:
```bash
# Disable notifications
TELEGRAM_BOT_TOKEN=""
TELEGRAM_CHAT_ID=""
```

## 📈 Current Status

- **Total Jobs**: 86 offensive security positions
- **Last Updated**: January 21, 2026 at 4:22 PM
- **Next Scheduled Run**: Next Monday at 9:00 AM
- **Web Dashboard**: http://YOURIP:9090/

---

**Powered by [SalarySleuth](https://github.com/fr4nk3nst1ner/salarysleuth)**
