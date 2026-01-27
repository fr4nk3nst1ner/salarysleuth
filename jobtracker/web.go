package main

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"
)

// basicAuth wraps an http.HandlerFunc with HTTP Basic Authentication
func basicAuth(next http.HandlerFunc) http.HandlerFunc {
	username := os.Getenv("WEB_USERNAME")
	password := os.Getenv("WEB_PASSWORD")
	
	// If no credentials set, bypass auth (backward compatible)
	if username == "" || password == "" {
		return next
	}

	return func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		
		// Use constant-time comparison to prevent timing attacks
		userMatch := subtle.ConstantTimeCompare([]byte(user), []byte(username)) == 1
		passMatch := subtle.ConstantTimeCompare([]byte(pass), []byte(password)) == 1
		
		if !ok || !userMatch || !passMatch {
			w.Header().Set("WWW-Authenticate", `Basic realm="OffSec Jobs"`)
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte("Unauthorized"))
			return
		}
		
		next(w, r)
	}
}

func startWebServer() {
	// Public endpoints (no auth required)
	http.HandleFunc("/health", handleHealth)
	http.HandleFunc("/", handleIndex) // Main job board is public
	
	// Protected API endpoints (require auth if WEB_USERNAME/WEB_PASSWORD set)
	http.HandleFunc("/api/jobs", basicAuth(handleAPIJobs))

	addr := fmt.Sprintf(":%d", config.WebPort)
	
	username := os.Getenv("WEB_USERNAME")
	if username != "" {
		log.Printf("Web server listening on http://localhost%s", addr)
		log.Printf("  📖 Main page: Public access")
		log.Printf("  🔒 API endpoints: Authentication ENABLED")
	} else {
		log.Printf("Web server listening on http://localhost%s (⚠️  All endpoints PUBLIC - set WEB_USERNAME/WEB_PASSWORD to protect API)", addr)
	}
	
	log.Fatal(http.ListenAndServe(addr, nil))
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}

// sanitizeConfigForPublic removes sensitive filtering logic from config
// Only returns display names and structure needed for the frontend
func sanitizeConfigForPublic(cfg *AppConfig) map[string]interface{} {
	sanitized := make(map[string]interface{})
	
	// Only include filter display information, not the actual keywords/logic
	filters := make(map[string]interface{})
	
	if cfg.Filters.Categories != nil {
		categories := make(map[string]interface{})
		for id, cat := range cfg.Filters.Categories {
			categories[id] = map[string]string{
				"DisplayName": cat.DisplayName,
			}
		}
		filters["Categories"] = categories
	}
	
	if cfg.Filters.Levels != nil {
		levels := make(map[string]interface{})
		for id, level := range cfg.Filters.Levels {
			levels[id] = map[string]string{
				"DisplayName": level.DisplayName,
			}
		}
		filters["Levels"] = levels
	}
	
	if cfg.Filters.Certifications != nil {
		certs := make(map[string]interface{})
		for id, cert := range cfg.Filters.Certifications {
			certs[id] = map[string]string{
				"DisplayName": cert.DisplayName,
			}
		}
		filters["Certifications"] = certs
	}
	
	sanitized["Filters"] = filters
	
	// Include display settings (safe to expose)
	sanitized["Display"] = cfg.Display
	
	return sanitized
}

func handleAPIJobs(w http.ResponseWriter, r *http.Request) {
	store := loadJobStore()
	cfg, _ := LoadConfig()

	// Tag all jobs
	taggedJobs := TagJobs(store.Jobs, cfg)

	// Sanitize config to only include display information
	sanitizedConfig := sanitizeConfigForPublic(cfg)

	response := struct {
		LastUpdated time.Time              `json:"last_updated"`
		Jobs        []TaggedJob            `json:"jobs"`
		Config      map[string]interface{} `json:"config"`
	}{
		LastUpdated: store.LastUpdated,
		Jobs:        taggedJobs,
		Config:      sanitizedConfig,
	}

	w.Header().Set("Content-Type", "application/json")
	// Remove wildcard CORS for better security
	// If you need CORS, set specific domain: w.Header().Set("Access-Control-Allow-Origin", "https://yourdomain.com")
	json.NewEncoder(w).Encode(response)
}

func handleIndex(w http.ResponseWriter, r *http.Request) {
	store := loadJobStore()
	cfg, _ := LoadConfig()

	// Sort jobs by FirstSeen (newest first)
	sort.Slice(store.Jobs, func(i, j int) bool {
		return store.Jobs[i].FirstSeen.After(store.Jobs[j].FirstSeen)
	})

	// Tag all jobs
	taggedJobs := TagJobs(store.Jobs, cfg)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	html := generateInteractiveHTML(taggedJobs, store.LastUpdated, cfg)
	w.Write([]byte(html))
}

func generateInteractiveHTML(jobs []TaggedJob, lastUpdated time.Time, cfg *AppConfig) string {
	// Convert jobs to JSON for JavaScript
	jobsJSON, _ := json.Marshal(jobs)
	configJSON, _ := json.Marshal(cfg)

	lastUpdatedStr := "Never"
	if !lastUpdated.IsZero() {
		lastUpdatedStr = lastUpdated.Format("January 2, 2006 at 3:04 PM")
	}

	jobsStr := string(jobsJSON)
	configStr := string(configJSON)

	// Build HTML using string replacement instead of fmt.Sprintf
	html := `<!DOCTYPE html>
<html lang="en">
<head>
	<meta charset="UTF-8">
	<meta name="viewport" content="width=device-width, initial-scale=1.0">
	<title>OffSec Jobs | SalarySleuth Tracker</title>
	<link rel="icon" type="image/svg+xml" href="data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 100 100'%3E%3Cdefs%3E%3ClinearGradient id='g' x1='0%25' y1='0%25' x2='100%25' y2='100%25'%3E%3Cstop offset='0%25' style='stop-color:%2300ff88'/%3E%3Cstop offset='100%25' style='stop-color:%2300cc6a'/%3E%3C/linearGradient%3E%3C/defs%3E%3Crect width='100' height='100' rx='20' fill='%230a0a0f'/%3E%3Cpath d='M50 15 L80 30 L80 55 C80 70 65 82 50 88 C35 82 20 70 20 55 L20 30 Z' fill='none' stroke='url(%23g)' stroke-width='4'/%3E%3Ctext x='50' y='62' text-anchor='middle' font-family='monospace' font-size='28' font-weight='bold' fill='%2300ff88'%3E$%3C/text%3E%3C/svg%3E">
	<link rel="preconnect" href="https://fonts.googleapis.com">
	<link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
	<link href="https://fonts.googleapis.com/css2?family=JetBrains+Mono:wght@400;500;600;700&family=Outfit:wght@300;400;500;600;700&display=swap" rel="stylesheet">
	<style>
		:root {
			--bg-primary: #0a0a0f;
			--bg-secondary: #12121a;
			--bg-card: #16161f;
			--bg-card-hover: #1c1c28;
			--accent-primary: #00ff88;
			--accent-secondary: #00cc6a;
			--accent-glow: rgba(0, 255, 136, 0.15);
			--text-primary: #e8e8ed;
			--text-secondary: #8888a0;
			--text-muted: #5a5a70;
			--border-color: #2a2a3a;
			--danger: #ff4757;
			--warning: #ffa502;
		}

		* { margin: 0; padding: 0; box-sizing: border-box; }

		body {
			font-family: 'Outfit', -apple-system, BlinkMacSystemFont, sans-serif;
			background: var(--bg-primary);
			color: var(--text-primary);
			min-height: 100vh;
			line-height: 1.6;
		}

		body::before {
			content: '';
			position: fixed;
			inset: 0;
			background: 
				radial-gradient(ellipse at 20% 20%, rgba(0, 255, 136, 0.08) 0%, transparent 50%),
				radial-gradient(ellipse at 80% 80%, rgba(0, 204, 106, 0.06) 0%, transparent 50%);
			pointer-events: none;
			z-index: -1;
		}

		.container { max-width: 1400px; margin: 0 auto; padding: 1.5rem; }

		header {
			text-align: center;
			padding: 2rem 0 1.5rem;
			border-bottom: 1px solid var(--border-color);
			margin-bottom: 1.5rem;
		}

		.logo {
			font-family: 'JetBrains Mono', monospace;
			font-size: 2.2rem;
			font-weight: 700;
			color: var(--accent-primary);
			text-shadow: 0 0 30px var(--accent-glow);
		}
		.logo span { color: var(--text-primary); }
		.tagline { color: var(--text-secondary); font-size: 1rem; }

		.filters-panel {
			background: var(--bg-secondary);
			border: 1px solid var(--border-color);
			border-radius: 12px;
			padding: 1.25rem;
			margin-bottom: 1.5rem;
		}

		.filters-header {
			display: flex;
			justify-content: space-between;
			align-items: center;
			margin-bottom: 1rem;
		}

		.filters-header h2 {
			font-size: 1rem;
			color: var(--text-secondary);
			font-weight: 500;
		}

		.filters-grid {
			display: grid;
			grid-template-columns: repeat(auto-fit, minmax(200px, 1fr));
			gap: 1rem;
		}

		.filter-group label {
			display: block;
			font-size: 0.75rem;
			color: var(--text-muted);
			text-transform: uppercase;
			letter-spacing: 0.5px;
			margin-bottom: 0.5rem;
		}

		.filter-group select {
			width: 100%;
			padding: 0.6rem 0.8rem;
			background: var(--bg-card);
			border: 1px solid var(--border-color);
			border-radius: 8px;
			color: var(--text-primary);
			font-size: 0.9rem;
			cursor: pointer;
			appearance: none;
			background-image: url("data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' width='12' height='12' viewBox='0 0 12 12'%3E%3Cpath fill='%238888a0' d='M6 8L1 3h10z'/%3E%3C/svg%3E");
			background-repeat: no-repeat;
			background-position: right 0.8rem center;
		}

		.filter-group select:focus {
			outline: none;
			border-color: var(--accent-primary);
		}

		.checkbox-group {
			display: flex;
			align-items: center;
			gap: 0.5rem;
			padding: 0.6rem 0;
		}

		.checkbox-group input[type="checkbox"] {
			width: 18px;
			height: 18px;
			accent-color: var(--accent-primary);
			cursor: pointer;
		}

		.checkbox-group span {
			color: var(--text-primary);
			font-size: 0.9rem;
		}

		.btn-reset {
			background: transparent;
			border: 1px solid var(--border-color);
			color: var(--text-secondary);
			padding: 0.5rem 1rem;
			border-radius: 6px;
			font-size: 0.85rem;
			cursor: pointer;
			transition: all 0.2s;
		}
		.btn-reset:hover {
			border-color: var(--accent-primary);
			color: var(--accent-primary);
		}

		.stats-bar {
			display: flex;
			justify-content: center;
			gap: 2rem;
			padding: 1rem;
			background: var(--bg-secondary);
			border-radius: 10px;
			border: 1px solid var(--border-color);
			margin-bottom: 1.5rem;
			flex-wrap: wrap;
		}

		.stat { text-align: center; }
		.stat-value {
			font-family: 'JetBrains Mono', monospace;
			font-size: 1.5rem;
			font-weight: 700;
			color: var(--accent-primary);
		}
		.stat-label {
			font-size: 0.75rem;
			color: var(--text-muted);
			text-transform: uppercase;
			letter-spacing: 0.5px;
		}

		.jobs-grid {
			display: grid;
			grid-template-columns: repeat(auto-fill, minmax(340px, 1fr));
			gap: 1.25rem;
		}

		.job-card {
			background: var(--bg-card);
			border: 1px solid var(--border-color);
			border-radius: 14px;
			padding: 1.5rem;
			transition: all 0.3s cubic-bezier(0.4, 0, 0.2, 1);
			position: relative;
			overflow: hidden;
		}

		.job-card::before {
			content: '';
			position: absolute;
			top: 0; left: 0; right: 0;
			height: 3px;
			background: linear-gradient(90deg, var(--accent-primary), var(--accent-secondary));
			opacity: 0;
			transition: opacity 0.3s;
		}

		.job-card:hover {
			background: var(--bg-card-hover);
			border-color: var(--accent-primary);
			transform: translateY(-3px);
			box-shadow: 0 15px 30px rgba(0, 0, 0, 0.3), 0 0 30px var(--accent-glow);
		}
		.job-card:hover::before { opacity: 1; }

		.job-card.excluded {
			opacity: 0.5;
			border-color: var(--danger);
		}

		.job-header {
			display: flex;
			justify-content: space-between;
			align-items: flex-start;
			margin-bottom: 0.75rem;
			gap: 0.5rem;
		}

		.company-info { display: flex; align-items: center; gap: 0.5rem; flex-wrap: wrap; }
		.company-name {
			font-size: 0.85rem;
			font-weight: 600;
			color: var(--text-secondary);
			text-transform: uppercase;
			letter-spacing: 0.3px;
		}

		.badge {
			font-size: 0.6rem;
			font-weight: 700;
			padding: 0.15rem 0.4rem;
			border-radius: 4px;
			text-transform: uppercase;
			letter-spacing: 0.3px;
		}

		.badge-new {
			background: var(--accent-primary);
			color: var(--bg-primary);
			animation: pulse 2s infinite;
		}

		.badge-category {
			background: rgba(0, 255, 136, 0.15);
			color: var(--accent-primary);
			border: 1px solid rgba(0, 255, 136, 0.3);
		}

		.badge-level {
			background: rgba(255, 165, 2, 0.15);
			color: var(--warning);
			border: 1px solid rgba(255, 165, 2, 0.3);
		}

		.badge-cert {
			background: rgba(138, 43, 226, 0.2);
			color: #b38fff;
			border: 1px solid rgba(138, 43, 226, 0.4);
		}

		.badge-remote {
			background: rgba(0, 191, 255, 0.15);
			color: #00bfff;
			border: 1px solid rgba(0, 191, 255, 0.3);
		}

		.badge-excluded {
			background: rgba(255, 71, 87, 0.2);
			color: var(--danger);
			border: 1px solid rgba(255, 71, 87, 0.3);
		}

		@keyframes pulse {
			0%, 100% { opacity: 1; }
			50% { opacity: 0.7; }
		}

		.job-title {
			font-size: 1.15rem;
			font-weight: 600;
			color: var(--text-primary);
			margin-bottom: 0.75rem;
			line-height: 1.35;
		}

		.job-tags {
			display: flex;
			flex-wrap: wrap;
			gap: 0.4rem;
			margin-bottom: 0.75rem;
		}

		.salary-info {
			display: flex;
			flex-wrap: wrap;
			gap: 0.4rem;
			margin-bottom: 0.75rem;
		}

		.salary-badge {
			display: inline-flex;
			align-items: center;
			gap: 0.25rem;
			font-family: 'JetBrains Mono', monospace;
			font-size: 0.75rem;
			font-weight: 600;
			padding: 0.4rem 0.6rem;
			border-radius: 6px;
		}
		.salary-badge small { font-size: 0.6rem; opacity: 0.7; font-weight: 400; }
		.salary-badge.levels {
			background: linear-gradient(135deg, #1a472a 0%, #0d2818 100%);
			color: var(--accent-primary);
			border: 1px solid rgba(0, 255, 136, 0.3);
		}
		.salary-badge.posting {
			background: linear-gradient(135deg, #2a2a1a 0%, #1a1a0d 100%);
			color: #ffd700;
			border: 1px solid rgba(255, 215, 0, 0.3);
		}

		.job-meta {
			display: flex;
			flex-wrap: wrap;
			gap: 0.75rem;
			margin-bottom: 1rem;
		}
		.job-meta span {
			display: flex;
			align-items: center;
			gap: 0.3rem;
			font-size: 0.8rem;
			color: var(--text-muted);
		}
		.job-meta svg { color: var(--text-secondary); }

		.apply-btn {
			display: inline-flex;
			align-items: center;
			gap: 0.4rem;
			background: transparent;
			color: var(--accent-primary);
			border: 1px solid var(--accent-primary);
			padding: 0.6rem 1.2rem;
			border-radius: 8px;
			font-size: 0.85rem;
			font-weight: 600;
			text-decoration: none;
			transition: all 0.2s;
		}
		.apply-btn:hover {
			background: var(--accent-primary);
			color: var(--bg-primary);
			box-shadow: 0 0 15px var(--accent-glow);
		}
		.apply-btn svg { transition: transform 0.2s; }
		.apply-btn:hover svg { transform: translate(2px, -2px); }

		footer {
			text-align: center;
			padding: 2rem 0;
			margin-top: 2rem;
			border-top: 1px solid var(--border-color);
			color: var(--text-muted);
			font-size: 0.8rem;
		}
		footer a { color: var(--accent-primary); text-decoration: none; }
		footer code { background: var(--bg-card); padding: 0.2rem 0.4rem; border-radius: 4px; }

		.empty-state {
			text-align: center;
			padding: 3rem 2rem;
			color: var(--text-secondary);
			grid-column: 1 / -1;
		}
		.empty-state svg { width: 60px; height: 60px; color: var(--text-muted); margin-bottom: 1rem; }
		.empty-state h2 { font-size: 1.25rem; margin-bottom: 0.5rem; }

		.hidden { display: none !important; }

		@media (max-width: 768px) {
			.container { padding: 1rem; }
			.logo { font-size: 1.6rem; }
			.filters-grid { grid-template-columns: 1fr 1fr; }
			.stats-bar { gap: 1rem; }
			.jobs-grid { grid-template-columns: 1fr; }
		}
	</style>
</head>
<body>
	<div class="container">
		<header>
			<h1 class="logo">Offsec<span>Jobs</span></h1>
			<p class="tagline">Curated offensive security positions • @fr4nk3nst1ner</p>
		</header>

		<div class="filters-panel">
			<div class="filters-header">
				<h2>🔍 Filter Jobs</h2>
				<button class="btn-reset" onclick="resetFilters()">Reset All</button>
			</div>
			<div class="filters-grid">
				<div class="filter-group">
					<label>Category</label>
					<select id="filter-category" onchange="applyFilters()">
						<option value="">All Categories</option>
					</select>
				</div>
				<div class="filter-group">
					<label>Experience Level</label>
					<select id="filter-level" onchange="applyFilters()">
						<option value="">All Levels</option>
					</select>
				</div>
				<div class="filter-group">
					<label>Certification</label>
					<select id="filter-cert" onchange="applyFilters()">
						<option value="">Any Certification</option>
					</select>
				</div>
				<div class="filter-group">
					<label>Sort By</label>
					<select id="filter-sort" onchange="applyFilters()">
						<option value="newest">Newest First</option>
						<option value="salary_high">Highest Salary</option>
						<option value="salary_low">Lowest Salary</option>
						<option value="company">Company A-Z</option>
					</select>
				</div>
				<div class="filter-group">
					<label>Options</label>
					<div class="checkbox-group">
						<input type="checkbox" id="filter-remote" onchange="applyFilters()">
						<span>Remote Only</span>
					</div>
				</div>
			<div class="filter-group">
				<label>&nbsp;</label>
				<div class="checkbox-group">
					<input type="checkbox" id="filter-show-excluded" onchange="applyFilters()">
					<span>Show Excluded</span>
				</div>
			</div>
			<div class="filter-group">
				<label>&nbsp;</label>
				<div class="checkbox-group">
					<input type="checkbox" id="filter-salary-only" onchange="applyFilters()">
					<span>💰 Only Jobs with Salary</span>
				</div>
			</div>
		</div>
	</div>

		<div class="stats-bar">
			<div class="stat">
				<div class="stat-value" id="stat-showing">0</div>
				<div class="stat-label">Showing</div>
			</div>
			<div class="stat">
				<div class="stat-value" id="stat-total">0</div>
				<div class="stat-label">Total Jobs</div>
			</div>
			<div class="stat">
				<div class="stat-value" id="stat-updated">{{LAST_UPDATED}}</div>
				<div class="stat-label">Last Updated</div>
			</div>
		</div>

		<main class="jobs-grid" id="jobs-container">
			<div class="empty-state">
				<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1">
					<circle cx="11" cy="11" r="8"></circle>
					<path d="m21 21-4.35-4.35"></path>
				</svg>
				<h2>Loading jobs...</h2>
			</div>
		</main>

		<footer>
			<p>Powered by <a href="https://github.com/fr4nk3nst1ner/salarysleuth" target="_blank">SalarySleuth</a> • Edit <code>config.yaml</code> to customize filters</p>
		</footer>
	</div>

	<script>
		const allJobs = {{ALL_JOBS}};
		const appConfig = {{APP_CONFIG}};

		function initFilters() {
			const categorySelect = document.getElementById('filter-category');
			const levelSelect = document.getElementById('filter-level');
			const certSelect = document.getElementById('filter-cert');

			if (appConfig.Filters && appConfig.Filters.Categories) {
				Object.entries(appConfig.Filters.Categories).forEach(function(entry) {
					var id = entry[0], cat = entry[1];
					var opt = document.createElement('option');
					opt.value = id;
					opt.textContent = cat.DisplayName || id;
					categorySelect.appendChild(opt);
				});
			}

			if (appConfig.Filters && appConfig.Filters.Levels) {
				Object.entries(appConfig.Filters.Levels).forEach(function(entry) {
					var id = entry[0], level = entry[1];
					var opt = document.createElement('option');
					opt.value = id;
					opt.textContent = level.DisplayName || id;
					levelSelect.appendChild(opt);
				});
			}

			if (appConfig.Filters && appConfig.Filters.Certifications) {
				Object.entries(appConfig.Filters.Certifications).forEach(function(entry) {
					var id = entry[0], cert = entry[1];
					var opt = document.createElement('option');
					opt.value = id;
					opt.textContent = cert.DisplayName || id;
					certSelect.appendChild(opt);
				});
			}

			document.getElementById('stat-total').textContent = allJobs.length;
			
			// Set default filters: sort by highest salary and show only jobs with salary
			document.getElementById('filter-sort').value = 'salary_high';
			document.getElementById('filter-salary-only').checked = true;
		}

	function applyFilters() {
		var category = document.getElementById('filter-category').value;
		var level = document.getElementById('filter-level').value;
		var cert = document.getElementById('filter-cert').value;
		var sortBy = document.getElementById('filter-sort').value;
		var remoteOnly = document.getElementById('filter-remote').checked;
		var showExcluded = document.getElementById('filter-show-excluded').checked;
		var salaryOnly = document.getElementById('filter-salary-only').checked;

		var filtered = allJobs.filter(function(j) {
			if (!showExcluded && j.tags.is_excluded) return false;
			if (category && j.tags.categories.indexOf(category) === -1) return false;
			if (level && j.tags.level !== level) return false;
			if (cert && j.tags.certifications.indexOf(cert) === -1) return false;
			if (remoteOnly && !j.tags.is_remote) return false;
			if (salaryOnly && !hasSalary(j)) return false;
			return true;
		});

			filtered.sort(function(a, b) {
				switch(sortBy) {
					case 'salary_high':
						return extractSalary(b) - extractSalary(a);
					case 'salary_low':
						return extractSalary(a) - extractSalary(b);
					case 'company':
						return a.job.company.localeCompare(b.job.company);
					default:
						return new Date(b.job.first_seen) - new Date(a.job.first_seen);
				}
			});

			renderJobs(filtered);
			document.getElementById('stat-showing').textContent = filtered.length;
		}

	function extractSalary(job) {
		var salary = job.job.level_salary || job.job.salary_range || '';
		var match = salary.match(/\$([0-9,]+)/);
		return match ? parseInt(match[1].replace(/,/g, '')) : 0;
	}

	function hasSalary(job) {
		return !!(job.job.level_salary || job.job.salary_range);
	}

		function renderJobs(jobs) {
			var container = document.getElementById('jobs-container');
			
			if (jobs.length === 0) {
				container.innerHTML = '<div class="empty-state"><svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1"><rect x="2" y="7" width="20" height="14" rx="2" ry="2"></rect><path d="M16 21V5a2 2 0 0 0-2-2h-4a2 2 0 0 0-2 2v16"></path></svg><h2>No jobs match your filters</h2><p>Try adjusting the filters above</p></div>';
				return;
			}

			container.innerHTML = jobs.map(function(j) { return renderJobCard(j); }).join('');
		}

		function renderJobCard(taggedJob) {
			var job = taggedJob.job;
			var tags = taggedJob.tags;
			
			var isNew = (Date.now() - new Date(job.first_seen).getTime()) < 24 * 60 * 60 * 1000;
			
			var tagsHTML = '';
			tags.categories.forEach(function(cat) {
				var catConfig = appConfig.Filters && appConfig.Filters.Categories && appConfig.Filters.Categories[cat];
				tagsHTML += '<span class="badge badge-category">' + (catConfig && catConfig.DisplayName ? catConfig.DisplayName : cat) + '</span>';
			});
			
			if (tags.level) {
				var levelConfig = appConfig.Filters && appConfig.Filters.Levels && appConfig.Filters.Levels[tags.level];
				tagsHTML += '<span class="badge badge-level">' + (levelConfig && levelConfig.DisplayName ? levelConfig.DisplayName : tags.level) + '</span>';
			}
			
			tags.certifications.forEach(function(cert) {
				var certConfig = appConfig.Filters && appConfig.Filters.Certifications && appConfig.Filters.Certifications[cert];
				tagsHTML += '<span class="badge badge-cert">' + (certConfig && certConfig.DisplayName ? certConfig.DisplayName : cert) + '</span>';
			});
			
			if (tags.is_remote) {
				tagsHTML += '<span class="badge badge-remote">Remote</span>';
			}
			
			if (tags.is_excluded) {
				tagsHTML += '<span class="badge badge-excluded">' + escapeHtml(tags.exclude_reason) + '</span>';
			}

			var salaryHTML = '';
			if (job.level_salary || job.salary_range) {
				salaryHTML = '<div class="salary-info">';
				if (job.level_salary) {
					salaryHTML += '<span class="salary-badge levels">💰 ' + escapeHtml(job.level_salary) + ' <small>(Levels.fyi)</small></span>';
				}
				if (job.salary_range) {
					salaryHTML += '<span class="salary-badge posting">💵 ' + escapeHtml(job.salary_range) + ' <small>(Posted)</small></span>';
				}
				salaryHTML += '</div>';
			}

			var cardClass = 'job-card' + (tags.is_excluded ? ' excluded' : '');
			var newBadge = isNew ? '<span class="badge badge-new">NEW</span>' : '';

			return '<article class="' + cardClass + '">' +
				'<div class="job-header">' +
					'<div class="company-info">' +
						'<h2 class="company-name">' + escapeHtml(job.company) + '</h2>' +
						newBadge +
					'</div>' +
				'</div>' +
				'<h3 class="job-title">' + escapeHtml(job.title) + '</h3>' +
				'<div class="job-tags">' + tagsHTML + '</div>' +
				salaryHTML +
				'<div class="job-meta">' +
					'<span><svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M21 10c0 7-9 13-9 13s-9-6-9-13a9 9 0 0 1 18 0z"/><circle cx="12" cy="10" r="3"/></svg> ' + escapeHtml(job.location) + '</span>' +
					'<span><svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M10 13a5 5 0 0 0 7.54.54l3-3a5 5 0 0 0-7.07-7.07l-1.72 1.71"/><path d="M14 11a5 5 0 0 0-7.54-.54l-3 3a5 5 0 0 0 7.07 7.07l1.71-1.71"/></svg> ' + job.source + '</span>' +
				'</div>' +
				'<a href="' + job.url + '" target="_blank" rel="noopener noreferrer" class="apply-btn">' +
					'Apply Now ' +
					'<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><line x1="7" y1="17" x2="17" y2="7"/><polyline points="7 7 17 7 17 17"/></svg>' +
				'</a>' +
			'</article>';
		}

		function escapeHtml(text) {
			if (!text) return '';
			var div = document.createElement('div');
			div.textContent = text;
			return div.innerHTML;
		}

	function resetFilters() {
		document.getElementById('filter-category').value = '';
		document.getElementById('filter-level').value = '';
		document.getElementById('filter-cert').value = '';
		document.getElementById('filter-sort').value = 'newest';
		document.getElementById('filter-remote').checked = false;
		document.getElementById('filter-show-excluded').checked = false;
		document.getElementById('filter-salary-only').checked = false;
		applyFilters();
	}

		initFilters();
		applyFilters();

		setTimeout(function() { location.reload(); }, 5 * 60 * 1000);
	</script>
</body>
</html>`

	// Replace placeholders
	html = strings.Replace(html, "{{LAST_UPDATED}}", lastUpdatedStr, 1)
	html = strings.Replace(html, "{{ALL_JOBS}}", jobsStr, 1)
	html = strings.Replace(html, "{{APP_CONFIG}}", configStr, 1)
	
	return html
}
