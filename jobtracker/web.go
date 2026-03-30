package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// RefreshState tracks when jobs were last manually refreshed
type RefreshState struct {
	LastRefresh time.Time `json:"last_refresh"`
}

var (
	refreshMutex    sync.Mutex
	isRefreshing    bool
	refreshStateFile string
)

func getRefreshStateFile() string {
	if refreshStateFile == "" {
		refreshStateFile = filepath.Join(config.DataDir, "last_refresh.json")
	}
	return refreshStateFile
}

func loadRefreshState() RefreshState {
	data, err := os.ReadFile(getRefreshStateFile())
	if err != nil {
		return RefreshState{}
	}
	var state RefreshState
	json.Unmarshal(data, &state)
	return state
}

func saveRefreshState(state RefreshState) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(getRefreshStateFile(), data, 0644)
}

func canRefreshToday() bool {
	state := loadRefreshState()
	if state.LastRefresh.IsZero() {
		return true
	}
	now := time.Now()
	lastRefresh := state.LastRefresh
	return now.Year() != lastRefresh.Year() ||
		now.YearDay() != lastRefresh.YearDay()
}

func getNextRefreshTime() time.Time {
	state := loadRefreshState()
	if state.LastRefresh.IsZero() {
		return time.Time{}
	}
	nextDay := state.LastRefresh.AddDate(0, 0, 1)
	return time.Date(nextDay.Year(), nextDay.Month(), nextDay.Day(), 0, 0, 0, 0, state.LastRefresh.Location())
}

// Custom search state
var (
	searchMutex    sync.Mutex
	isSearching    bool
	searchResults  []TaggedJob
	searchQuery    string
	searchError    string
	searchCancel   context.CancelFunc
	searchCancelled bool
)

func startWebServer() {
	seedAdminUser()
	startSessionCleanup()
	ensureHistoryDir()
	ensureSavedJobsDir()
	ensureAlertsDir()
	runHistoryCleanup()
	startScheduler()

	// Public endpoints
	http.HandleFunc("/health", handleHealth)
	http.HandleFunc("/", handleIndex)
	http.HandleFunc("/api/refresh/status", handleRefreshStatus)
	http.HandleFunc("/api/auth/register", handleRegister)
	http.HandleFunc("/api/auth/check", handleAuthCheck)
	http.HandleFunc("/login", handleLoginPage)
	http.HandleFunc("/api/auth/login", handleLoginAPI)
	http.HandleFunc("/logout", handleLogout)

	// Authenticated endpoints (any role)
	http.HandleFunc("/api/jobs", requireAuth(handleAPIJobs))
	http.HandleFunc("/api/search", requireAuth(handleCustomSearch))
	http.HandleFunc("/api/search/status", requireAuth(handleSearchStatus))
	http.HandleFunc("/api/search/cancel", requireAuth(handleCancelSearch))
	http.HandleFunc("/api/history", requireAuth(handleHistory))
	http.HandleFunc("/api/history/results/", requireAuth(handleHistoryResults))
	http.HandleFunc("/api/history/clear", requireAuth(handleClearHistory))
	http.HandleFunc("/api/saved", requireAuth(handleSavedJobs))
	http.HandleFunc("/api/saved/ids", requireAuth(handleSavedJobIDs))
	http.HandleFunc("/api/alerts/config", requireAuth(handleAlertsConfig))
	http.HandleFunc("/api/alerts/telegram/test", requireAuth(handleTestTelegram))
	http.HandleFunc("/api/alerts/schedules", requireAuth(handleSchedules))
	http.HandleFunc("/api/alerts/schedules/delete", requireAuth(handleDeleteSchedule))

	// Admin-only endpoints
	http.HandleFunc("/api/refresh", requireAdmin(handleRefresh))
	http.HandleFunc("/api/admin/tokens", requireAdmin(handleAdminTokens))
	http.HandleFunc("/api/admin/users", requireAdmin(handleAdminUsers))
	http.HandleFunc("/api/admin/history", requireAdmin(handleAdminHistory))

	addr := fmt.Sprintf(":%d", config.WebPort)

	if isAuthEnabled() {
		log.Printf("Web server listening on http://localhost%s", addr)
		log.Printf("  Main page: Public (read-only)")
		log.Printf("  Search/Custom Search: Authenticated users")
		log.Printf("  Refresh/Admin: Admin users only")
	} else {
		log.Printf("Web server listening on http://localhost%s (all features PUBLIC - set WEB_USERNAME/WEB_PASSWORD to create admin)", addr)
	}

	log.Fatal(http.ListenAndServe(addr, nil))
}

func cancelCurrentSearch() {
	if searchCancel != nil {
		searchCancel()
		searchCancel = nil
	}
}

func handleCustomSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Query string `json:"query"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "Invalid request", http.StatusBadRequest)
		return
	}
	req.Query = strings.TrimSpace(req.Query)
	if req.Query == "" || len(req.Query) < 3 {
		jsonError(w, "Search query must be at least 3 characters", http.StatusBadRequest)
		return
	}
	if len(req.Query) > 100 {
		jsonError(w, "Search query too long", http.StatusBadRequest)
		return
	}

	searchMutex.Lock()
	if isSearching {
		log.Printf("Cancelling previous search %q to start new search %q", searchQuery, req.Query)
		cancelCurrentSearch()
		// Wait briefly for the goroutine to clean up
		searchMutex.Unlock()
		time.Sleep(500 * time.Millisecond)
		searchMutex.Lock()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	isSearching = true
	searchQuery = req.Query
	searchResults = nil
	searchError = ""
	searchCancel = cancel
	searchCancelled = false
	searchMutex.Unlock()

	user := r.Header.Get("X-Auth-User")
	queryForHistory := req.Query
	go func() {
		defer func() {
			searchMutex.Lock()
			isSearching = false
			searchCancel = nil
			cancel()
			searchMutex.Unlock()
		}()

		log.Printf("Custom search started by %s: %q (sources run in parallel, 3 min per source)", user, queryForHistory)

		sources := []string{"linkedin", "greenhouse", "lever"}
		perSourceTimeout := 3 * time.Minute
		var wg sync.WaitGroup

		for _, src := range sources {
			wg.Add(1)
			go func(source string) {
				defer wg.Done()
				srcCtx, srcCancel := context.WithTimeout(ctx, perSourceTimeout)
				defer srcCancel()

				jobs, err := runSingleSourceSearch(srcCtx, queryForHistory, config.Pages, source)
				if err != nil {
					if ctx.Err() == context.Canceled {
						return
					}
					log.Printf("Custom search source %s error: %v", source, err)
					return
				}

				if len(jobs) > 0 {
					cfg, _ := LoadConfig()
					tagged := TagJobs(jobs, cfg)
					searchMutex.Lock()
					searchResults = append(searchResults, tagged...)
					searchMutex.Unlock()
					log.Printf("Custom search source %s returned %d jobs (total so far: %d)", source, len(tagged), len(searchResults))
				} else {
					log.Printf("Custom search source %s returned 0 jobs", source)
				}
			}(src)
		}

		wg.Wait()

		if ctx.Err() == context.Canceled {
			log.Printf("Custom search cancelled: %q", queryForHistory)
			searchMutex.Lock()
			searchCancelled = true
			searchError = "Search was cancelled"
			searchMutex.Unlock()
			return
		}

		searchMutex.Lock()
		totalResults := len(searchResults)
		resultsSnapshot := make([]TaggedJob, len(searchResults))
		copy(resultsSnapshot, searchResults)
		searchMutex.Unlock()

		entryID := generateEntryID()
		resultsFile := ""
		if totalResults > 0 {
			resultsFile = saveSearchResults(entryID, resultsSnapshot)
		}
		addHistoryEntry(user, SearchHistoryEntry{
			ID: entryID, Timestamp: time.Now(),
			Type: "custom_search", Query: queryForHistory,
			ResultCount: totalResults, ResultsFile: resultsFile,
		})

		log.Printf("Custom search complete: %q found %d total jobs", queryForHistory, totalResults)
	}()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Search started for: " + req.Query,
	})
}

func handleCancelSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	searchMutex.Lock()
	if !isSearching {
		searchMutex.Unlock()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"message": "No search is currently running",
		})
		return
	}
	q := searchQuery
	cancelCurrentSearch()
	searchMutex.Unlock()

	log.Printf("Search cancelled via API: %q", q)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Search for \"" + q + "\" has been cancelled",
	})
}

func handleSearchStatus(w http.ResponseWriter, r *http.Request) {
	searchMutex.Lock()
	searching := isSearching
	query := searchQuery
	results := searchResults
	errMsg := searchError
	cancelled := searchCancelled
	searchMutex.Unlock()

	w.Header().Set("Content-Type", "application/json")
	if searching {
		partialCount := len(results)
		msg := fmt.Sprintf("Searching for: %s... (LinkedIn, Greenhouse, Lever running in parallel)", query)
		resp := map[string]interface{}{
			"is_searching":  true,
			"query":         query,
			"partial_count": partialCount,
		}
		if partialCount > 0 {
			msg = fmt.Sprintf("Searching for: %s... Found %d jobs so far, waiting for more sources...", query, partialCount)
			resp["partial_results"] = results
		}
		resp["message"] = msg
		json.NewEncoder(w).Encode(resp)
		return
	}

	if cancelled {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"is_searching": false,
			"cancelled":    true,
			"query":        query,
			"message":      "Search was cancelled",
		})
		return
	}

	if errMsg != "" {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"is_searching": false,
			"query":        query,
			"error":        errMsg,
		})
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"is_searching": false,
		"query":        query,
		"results":      results,
		"count":        len(results),
	})
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

func handleRefreshStatus(w http.ResponseWriter, r *http.Request) {
	refreshMutex.Lock()
	currentlyRefreshing := isRefreshing
	refreshMutex.Unlock()

	state := loadRefreshState()
	canRefresh := canRefreshToday()
	nextRefresh := getNextRefreshTime()

	response := struct {
		CanRefresh   bool      `json:"can_refresh"`
		IsRefreshing bool      `json:"is_refreshing"`
		LastRefresh  time.Time `json:"last_refresh,omitempty"`
		NextRefresh  time.Time `json:"next_refresh,omitempty"`
		Message      string    `json:"message"`
	}{
		CanRefresh:   canRefresh && !currentlyRefreshing,
		IsRefreshing: currentlyRefreshing,
		LastRefresh:  state.LastRefresh,
		NextRefresh:  nextRefresh,
	}

	if currentlyRefreshing {
		response.Message = "A refresh is currently in progress..."
	} else if !canRefresh {
		response.Message = fmt.Sprintf("Jobs have already been updated today. Next refresh available after midnight.")
	} else {
		response.Message = "Ready to refresh"
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func handleRefresh(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	refreshMutex.Lock()
	if isRefreshing {
		refreshMutex.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"message": "A refresh is already in progress. Please wait.",
		})
		return
	}

	if !canRefreshToday() {
		refreshMutex.Unlock()
		state := loadRefreshState()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success":      false,
			"message":      "Jobs have already been updated today. Please try again tomorrow.",
			"last_refresh": state.LastRefresh,
		})
		return
	}

	isRefreshing = true
	refreshMutex.Unlock()

	go func() {
		defer func() {
			refreshMutex.Lock()
			isRefreshing = false
			refreshMutex.Unlock()
		}()

		log.Println("Manual refresh triggered via web UI")

		const scrapeRuns = 3
		allJobs := make(map[string]Job)

		for run := 1; run <= scrapeRuns; run++ {
			log.Printf("  Refresh pass %d/%d...\n", run, scrapeRuns)

			jobs, err := runSalarySleuth(config.Description, config.Pages)
			if err != nil {
				log.Printf("  Warning: Pass %d failed: %v\n", run, err)
				continue
			}

			for _, job := range jobs {
				if _, exists := allJobs[job.ID]; !exists {
					allJobs[job.ID] = job
				} else {
					existing := allJobs[job.ID]
					if job.LevelSalary != "" && existing.LevelSalary == "" {
						existing.LevelSalary = job.LevelSalary
						allJobs[job.ID] = existing
					}
					if job.SalaryRange != "" && existing.SalaryRange == "" {
						existing.SalaryRange = job.SalaryRange
						allJobs[job.ID] = existing
					}
				}
			}

			if run < scrapeRuns {
				time.Sleep(5 * time.Second)
			}
		}

		var jobs []Job
		for _, job := range allJobs {
			jobs = append(jobs, job)
		}

		log.Printf("Refresh found %d total unique jobs\n", len(jobs))

		filteredJobs := filterOffsecJobs(jobs)
		log.Printf("After filtering: %d OSCP/offsec jobs\n", len(filteredJobs))

		existingStore := loadJobStore()
		newJobs, updatedStore := processJobs(filteredJobs, existingStore)

		if err := saveJobStore(updatedStore); err != nil {
			log.Printf("Failed to save job store: %v", err)
			return
		}

		if len(newJobs) > 0 {
			log.Printf("Found %d new jobs, notifying subscribed users...\n", len(newJobs))
			notifySubscribedUsers("Default OffSec Refresh", newJobs)
		}

		saveRefreshState(RefreshState{LastRefresh: time.Now()})
		log.Printf("Manual refresh complete. %d active jobs in store.\n", len(updatedStore.Jobs))
	}()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Refresh started. This may take a few minutes. The page will update automatically when complete.",
	})
}

func generateEntryID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func handleHistory(w http.ResponseWriter, r *http.Request) {
	username := r.Header.Get("X-Auth-User")

	switch r.Method {
	case http.MethodGet:
		entries := getUserHistory(username)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"username": username,
			"entries":  entries,
			"count":    len(entries),
		})

	case http.MethodPost:
		var req struct {
			Type        string        `json:"type"`
			Query       string        `json:"query"`
			Filters     SearchFilters `json:"filters"`
			ResultCount int           `json:"result_count"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonError(w, "Invalid request", http.StatusBadRequest)
			return
		}
		if req.Type != "filter" && req.Type != "custom_search" {
			req.Type = "filter"
		}

		entry := SearchHistoryEntry{
			ID:          generateEntryID(),
			Timestamp:   time.Now(),
			Type:        req.Type,
			Query:       req.Query,
			Filters:     req.Filters,
			ResultCount: req.ResultCount,
		}
		addHistoryEntry(username, entry)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"id":      entry.ID,
		})

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func handleClearHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	username := r.Header.Get("X-Auth-User")
	if err := clearUserHistory(username); err != nil {
		jsonError(w, "Failed to clear history", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "History cleared",
	})
}

func handleHistoryResults(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	username := r.Header.Get("X-Auth-User")
	entryID := strings.TrimPrefix(r.URL.Path, "/api/history/results/")
	if entryID == "" {
		jsonError(w, "Missing entry ID", http.StatusBadRequest)
		return
	}

	entries := getUserHistory(username)
	var found *SearchHistoryEntry
	for i := range entries {
		if entries[i].ID == entryID {
			found = &entries[i]
			break
		}
	}
	if found == nil {
		jsonError(w, "History entry not found", http.StatusNotFound)
		return
	}
	if found.ResultsFile == "" {
		jsonError(w, "No stored results for this entry", http.StatusNotFound)
		return
	}

	data, err := loadSearchResults(found.ResultsFile)
	if err != nil {
		jsonError(w, "Results file not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"entry":`))
	entryJSON, _ := json.Marshal(found)
	w.Write(entryJSON)
	w.Write([]byte(`,"results":`))
	w.Write(data)
	w.Write([]byte(`}`))
}

func handleAdminHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Allow admin to query a specific user's history via ?user=username
	targetUser := r.URL.Query().Get("user")
	if targetUser != "" {
		entries := getUserHistory(targetUser)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"username": targetUser,
			"entries":  entries,
			"count":    len(entries),
		})
		return
	}

	allHistory := getAllUsersHistory()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(allHistory)
}

func handleSavedJobs(w http.ResponseWriter, r *http.Request) {
	username := r.Header.Get("X-Auth-User")

	switch r.Method {
	case http.MethodGet:
		jobs := getUserSavedJobsList(username)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"jobs":  jobs,
			"count": len(jobs),
		})

	case http.MethodPost:
		var req SavedJob
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonError(w, "Invalid request", http.StatusBadRequest)
			return
		}
		if req.JobID == "" {
			jsonError(w, "job_id is required", http.StatusBadRequest)
			return
		}
		req.SavedAt = time.Now()
		added, err := addSavedJob(username, req)
		if err != nil {
			jsonError(w, "Failed to save job", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if !added {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success":      true,
				"already_saved": true,
				"message":      "Job was already saved",
			})
		} else {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": true,
				"message": "Job saved",
			})
		}

	case http.MethodDelete:
		var req struct {
			JobID string `json:"job_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonError(w, "Invalid request", http.StatusBadRequest)
			return
		}
		removed := removeSavedJob(username, req.JobID)
		w.Header().Set("Content-Type", "application/json")
		if removed {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": true,
				"message": "Job removed from saved list",
			})
		} else {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": false,
				"message": "Job not found in saved list",
			})
		}

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func handleSavedJobIDs(w http.ResponseWriter, r *http.Request) {
	username := r.Header.Get("X-Auth-User")
	ids := getUserSavedJobIDs(username)
	idList := make([]string, 0, len(ids))
	for id := range ids {
		idList = append(idList, id)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(idList)
}

func handleAlertsConfig(w http.ResponseWriter, r *http.Request) {
	username := r.Header.Get("X-Auth-User")
	w.Header().Set("Content-Type", "application/json")

	if r.Method == http.MethodGet {
		cfg := loadUserAlerts(username)
		safe := map[string]interface{}{
			"telegram": map[string]interface{}{
				"enabled":   cfg.Telegram.Enabled,
				"has_token":  cfg.Telegram.BotToken != "",
				"has_chatid": cfg.Telegram.ChatID != "",
				"verified":  cfg.Telegram.Verified,
			},
			"schedules": cfg.Schedules,
		}
		json.NewEncoder(w).Encode(safe)
		return
	}

	if r.Method == http.MethodPost {
		var req struct {
			BotToken string `json:"bot_token"`
			ChatID   string `json:"chat_id"`
			Enabled  bool   `json:"enabled"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid request", http.StatusBadRequest)
			return
		}

		cfg := loadUserAlerts(username)
		if req.BotToken != "" {
			cfg.Telegram.BotToken = req.BotToken
		}
		if req.ChatID != "" {
			cfg.Telegram.ChatID = req.ChatID
		}
		cfg.Telegram.Enabled = req.Enabled
		cfg.Telegram.Verified = false

		if err := saveUserAlerts(username, cfg); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": "Failed to save"})
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
		return
	}

	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
}

func handleTestTelegram(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	username := r.Header.Get("X-Auth-User")
	cfg := loadUserAlerts(username)

	if cfg.Telegram.BotToken == "" || cfg.Telegram.ChatID == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Bot token and Chat ID are required"})
		return
	}

	testMsg := "🧪 *SalarySleuth Test*\n\nTelegram integration is working\\! You will receive job alerts here\\."
	err := sendTelegramMessageWithCreds(cfg.Telegram.BotToken, cfg.Telegram.ChatID, testMsg)

	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("Test failed: %v", err)})
		return
	}

	cfg.Telegram.Verified = true
	cfg.Telegram.Enabled = true
	if err := saveUserAlerts(username, cfg); err != nil {
		log.Printf("Failed to save verified state for %s: %v", username, err)
	}

	json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "message": "Test message sent! Check your Telegram."})
}

func handleSchedules(w http.ResponseWriter, r *http.Request) {
	username := r.Header.Get("X-Auth-User")
	w.Header().Set("Content-Type", "application/json")

	if r.Method == http.MethodGet {
		cfg := loadUserAlerts(username)
		json.NewEncoder(w).Encode(cfg.Schedules)
		return
	}

	if r.Method == http.MethodPost {
		var sched ScheduledScan
		if err := json.NewDecoder(r.Body).Decode(&sched); err != nil {
			http.Error(w, "Invalid request", http.StatusBadRequest)
			return
		}

		if sched.Name == "" {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "Schedule name is required"})
			return
		}
		if sched.Type == "custom" && sched.Query == "" {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "Search query is required for custom scans"})
			return
		}
		if sched.Hour < 0 || sched.Hour > 23 || sched.Minute < 0 || sched.Minute > 59 {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "Invalid time"})
			return
		}

		cfg := loadUserAlerts(username)

		if !cfg.Telegram.Verified {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "Configure and verify Telegram first"})
			return
		}

		if len(cfg.Schedules) >= 10 {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "Maximum 10 schedules allowed"})
			return
		}

		if sched.ID == "" {
			sched.ID = generateEntryID()
			sched.CreatedAt = time.Now().Format(time.RFC3339)
			sched.Enabled = true
			cfg.Schedules = append(cfg.Schedules, sched)
		} else {
			found := false
			for i, s := range cfg.Schedules {
				if s.ID == sched.ID {
					sched.CreatedAt = s.CreatedAt
					sched.LastRun = s.LastRun
					sched.LastResult = s.LastResult
					cfg.Schedules[i] = sched
					found = true
					break
				}
			}
			if !found {
				w.WriteHeader(http.StatusNotFound)
				json.NewEncoder(w).Encode(map[string]string{"error": "Schedule not found"})
				return
			}
		}

		if err := saveUserAlerts(username, cfg); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": "Failed to save"})
			return
		}

		json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "schedules": cfg.Schedules})
		return
	}

	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
}

func handleDeleteSchedule(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	username := r.Header.Get("X-Auth-User")
	var req struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID == "" {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	cfg := loadUserAlerts(username)
	newSchedules := make([]ScheduledScan, 0)
	for _, s := range cfg.Schedules {
		if s.ID != req.ID {
			newSchedules = append(newSchedules, s)
		}
	}
	cfg.Schedules = newSchedules

	w.Header().Set("Content-Type", "application/json")
	if err := saveUserAlerts(username, cfg); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Failed to save"})
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "schedules": cfg.Schedules})
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
			align-items: center;
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
		.levels-link { color: var(--accent-primary); text-decoration: underline; text-underline-offset: 2px; }
		.levels-link:hover { color: #fff; }
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

		.modal {
			position: fixed;
			inset: 0;
			background: rgba(0, 0, 0, 0.8);
			display: flex;
			align-items: center;
			justify-content: center;
			z-index: 1001;
		}
		.modal-content {
			background: var(--bg-secondary);
			border: 1px solid var(--border-color);
			border-radius: 14px;
			padding: 2rem;
			width: 90%;
			max-width: 420px;
		}
		.modal-content h2 {
			color: var(--accent-primary);
			margin-bottom: 1.5rem;
			font-size: 1.3rem;
		}
		.form-group {
			margin-bottom: 1rem;
		}
		.form-group label {
			display: block;
			font-size: 0.75rem;
			color: var(--text-muted);
			text-transform: uppercase;
			letter-spacing: 0.5px;
			margin-bottom: 0.4rem;
		}
		.form-group input {
			width: 100%;
			padding: 0.6rem 0.8rem;
			background: var(--bg-card);
			border: 1px solid var(--border-color);
			border-radius: 8px;
			color: var(--text-primary);
			font-size: 0.9rem;
			font-family: 'Outfit', sans-serif;
		}
		.form-group input:focus { outline: none; border-color: var(--accent-primary); }
		.form-group input::placeholder { color: var(--text-muted); }
		.form-error {
			color: var(--danger);
			font-size: 0.85rem;
			margin-bottom: 1rem;
			padding: 0.5rem;
			background: rgba(255, 71, 87, 0.1);
			border-radius: 6px;
		}
		.form-success {
			color: var(--accent-primary);
			font-size: 0.85rem;
			margin-bottom: 1rem;
			padding: 0.5rem;
			background: rgba(0, 255, 136, 0.1);
			border-radius: 6px;
		}
		.form-actions {
			display: flex;
			gap: 0.75rem;
			margin-top: 1.5rem;
		}
		.btn-primary {
			flex: 1;
			padding: 0.65rem 1rem;
			background: linear-gradient(135deg, var(--accent-primary), var(--accent-secondary));
			color: var(--bg-primary);
			border: none;
			border-radius: 8px;
			font-size: 0.9rem;
			font-weight: 600;
			cursor: pointer;
			font-family: 'Outfit', sans-serif;
			transition: all 0.2s;
		}
		.btn-primary:hover { transform: translateY(-1px); box-shadow: 0 5px 15px var(--accent-glow); }
		.btn-secondary {
			flex: 1;
			padding: 0.65rem 1rem;
			background: transparent;
			color: var(--text-secondary);
			border: 1px solid var(--border-color);
			border-radius: 8px;
			font-size: 0.9rem;
			cursor: pointer;
			font-family: 'Outfit', sans-serif;
		}
		.btn-secondary:hover { border-color: var(--text-secondary); }

		.admin-panel {
			background: var(--bg-secondary);
			border: 1px solid var(--border-color);
			border-radius: 12px;
			padding: 1.5rem;
			margin-bottom: 1.5rem;
		}
		.admin-panel > h2 {
			color: var(--accent-primary);
			font-size: 1.1rem;
			margin-bottom: 1.25rem;
			cursor: pointer;
		}
		.admin-section {
			margin-bottom: 1.5rem;
			padding-bottom: 1.5rem;
			border-bottom: 1px solid var(--border-color);
		}
		.admin-section:last-child { margin-bottom: 0; padding-bottom: 0; border-bottom: none; }
		.admin-section h3 { font-size: 0.9rem; color: var(--text-secondary); margin-bottom: 0.75rem; }
		.token-form {
			display: flex;
			gap: 0.75rem;
			align-items: center;
			flex-wrap: wrap;
		}
		.admin-select {
			padding: 0.5rem 0.8rem;
			background: var(--bg-card);
			border: 1px solid var(--border-color);
			border-radius: 6px;
			color: var(--text-primary);
			font-size: 0.85rem;
			font-family: 'Outfit', sans-serif;
			cursor: pointer;
		}
		.admin-select:focus { outline: none; border-color: var(--accent-primary); }
		.generated-token {
			margin-top: 1rem;
			padding: 1rem;
			background: var(--bg-card);
			border: 1px solid var(--accent-primary);
			border-radius: 8px;
		}
		.generated-token label { display: block; font-size: 0.75rem; color: var(--text-muted); margin-bottom: 0.5rem; }
		.token-display { display: flex; align-items: center; gap: 0.75rem; }
		.token-display code {
			flex: 1;
			font-family: 'JetBrains Mono', monospace;
			font-size: 0.8rem;
			color: var(--accent-primary);
			word-break: break-all;
			padding: 0.5rem;
			background: var(--bg-primary);
			border-radius: 4px;
		}
		.btn-sm {
			padding: 0.35rem 0.7rem;
			background: transparent;
			color: var(--accent-primary);
			border: 1px solid var(--accent-primary);
			border-radius: 6px;
			font-size: 0.75rem;
			cursor: pointer;
			font-family: 'Outfit', sans-serif;
			white-space: nowrap;
			transition: all 0.2s;
		}
		.btn-sm:hover { background: var(--accent-primary); color: var(--bg-primary); }
		.btn-danger { color: var(--danger); border-color: var(--danger); }
		.btn-danger:hover { background: var(--danger); color: white; }
		.admin-table {
			width: 100%;
			border-collapse: collapse;
			font-size: 0.85rem;
			overflow-x: auto;
			display: block;
		}
		.admin-table th {
			text-align: left;
			padding: 0.5rem;
			color: var(--text-muted);
			font-size: 0.7rem;
			text-transform: uppercase;
			letter-spacing: 0.5px;
			border-bottom: 1px solid var(--border-color);
		}
		.admin-table td {
			padding: 0.5rem;
			color: var(--text-primary);
			border-bottom: 1px solid rgba(42, 42, 58, 0.5);
		}
		.admin-table code { font-family: 'JetBrains Mono', monospace; font-size: 0.75rem; color: var(--text-secondary); }
		.badge-role-admin { background: rgba(255, 165, 2, 0.15); color: var(--warning); padding: 0.15rem 0.4rem; border-radius: 4px; font-size: 0.7rem; font-weight: 600; }
		.badge-role-user { background: rgba(0, 191, 255, 0.15); color: #00bfff; padding: 0.15rem 0.4rem; border-radius: 4px; font-size: 0.7rem; font-weight: 600; }
		.badge-status-used { background: rgba(136, 136, 160, 0.15); color: var(--text-muted); padding: 0.15rem 0.4rem; border-radius: 4px; font-size: 0.7rem; }
		.badge-status-expired { background: rgba(255, 71, 87, 0.15); color: var(--danger); padding: 0.15rem 0.4rem; border-radius: 4px; font-size: 0.7rem; }
		.badge-status-active { background: rgba(0, 255, 136, 0.15); color: var(--accent-primary); padding: 0.15rem 0.4rem; border-radius: 4px; font-size: 0.7rem; }

		.search-group { grid-column: 1 / -1; }
		.search-group input {
			width: 100%;
			padding: 0.6rem 0.8rem;
			background: var(--bg-card);
			border: 1px solid var(--border-color);
			border-radius: 8px;
			color: var(--text-primary);
			font-size: 0.9rem;
			font-family: 'Outfit', sans-serif;
		}
		.search-group input:focus {
			outline: none;
			border-color: var(--accent-primary);
			box-shadow: 0 0 10px var(--accent-glow);
		}
		.search-group input::placeholder { color: var(--text-muted); }

		.btn-login {
			display: inline-flex;
			align-items: center;
			justify-content: center;
			gap: 0.4rem;
			background: transparent;
			border: 1px solid var(--accent-primary);
			color: var(--accent-primary);
			padding: 0 1rem;
			height: 36px;
			border-radius: 8px;
			font-size: 0.8rem;
			font-weight: 600;
			cursor: pointer;
			transition: all 0.2s;
			font-family: 'Outfit', sans-serif;
			white-space: nowrap;
		}
		.btn-login:hover {
			background: var(--accent-primary);
			color: var(--bg-primary);
		}
		.auth-hint {
			font-size: 0.7rem;
			color: var(--text-muted);
			text-align: center;
		}

		.hidden { display: none !important; }

		.refresh-btn {
			display: inline-flex;
			align-items: center;
			justify-content: center;
			gap: 0.4rem;
			background: linear-gradient(135deg, var(--accent-primary), var(--accent-secondary));
			color: var(--bg-primary);
			border: none;
			padding: 0 1rem;
			height: 36px;
			border-radius: 8px;
			font-size: 0.8rem;
			font-weight: 600;
			cursor: pointer;
			transition: all 0.3s;
			font-family: 'Outfit', sans-serif;
			white-space: nowrap;
		}
		.refresh-btn:hover:not(:disabled) {
			transform: translateY(-2px);
			box-shadow: 0 5px 20px var(--accent-glow);
		}
		.refresh-btn:disabled {
			opacity: 0.5;
			cursor: not-allowed;
			background: var(--bg-card);
			color: var(--text-muted);
		}
		.refresh-btn.refreshing {
			animation: pulse 1.5s infinite;
		}
		.refresh-btn svg {
			width: 16px;
			height: 16px;
		}
		.refresh-btn.refreshing svg {
			animation: spin 1s linear infinite;
		}
		@keyframes spin {
			from { transform: rotate(0deg); }
			to { transform: rotate(360deg); }
		}
		.searching-spinner {
			width: 48px;
			height: 48px;
			border: 4px solid var(--border-color);
			border-top-color: var(--accent-primary);
			border-radius: 50%;
			animation: spin 1s linear infinite;
			margin: 0 auto 1rem;
		}

		.toast-container {
			position: fixed;
			top: 1.5rem;
			right: 1.5rem;
			z-index: 1000;
			display: flex;
			flex-direction: column;
			gap: 0.75rem;
		}
		.toast {
			background: var(--bg-card);
			border: 1px solid var(--border-color);
			border-radius: 10px;
			padding: 1rem 1.25rem;
			min-width: 300px;
			max-width: 400px;
			box-shadow: 0 10px 30px rgba(0, 0, 0, 0.4);
			animation: slideIn 0.3s ease-out;
			display: flex;
			align-items: flex-start;
			gap: 0.75rem;
		}
		.toast.success { border-color: var(--accent-primary); }
		.toast.error { border-color: var(--danger); }
		.toast.warning { border-color: var(--warning); }
		.toast.info { border-color: #00bfff; }
		.toast-icon {
			font-size: 1.25rem;
			flex-shrink: 0;
		}
		.toast-content { flex: 1; }
		.toast-title {
			font-weight: 600;
			font-size: 0.9rem;
			margin-bottom: 0.25rem;
		}
		.toast-message {
			font-size: 0.8rem;
			color: var(--text-secondary);
			line-height: 1.4;
		}
		.toast-close {
			background: none;
			border: none;
			color: var(--text-muted);
			cursor: pointer;
			padding: 0;
			font-size: 1.25rem;
			line-height: 1;
		}
		.toast-close:hover { color: var(--text-primary); }
		@keyframes slideIn {
			from { transform: translateX(100%); opacity: 0; }
			to { transform: translateX(0); opacity: 1; }
		}
		@keyframes slideOut {
			from { transform: translateX(0); opacity: 1; }
			to { transform: translateX(100%); opacity: 0; }
		}
		.toast.hiding { animation: slideOut 0.3s ease-in forwards; }

		.history-panel {
			background: var(--bg-secondary);
			border: 1px solid var(--border-color);
			border-radius: 12px;
			padding: 1.5rem;
			margin-bottom: 1.5rem;
		}
		.history-panel > .history-header {
			display: flex;
			justify-content: space-between;
			align-items: center;
			margin-bottom: 1rem;
		}
		.history-panel > .history-header h2 {
			color: var(--accent-primary);
			font-size: 1.1rem;
			cursor: pointer;
		}
		.history-header-actions {
			display: flex;
			gap: 0.5rem;
			align-items: center;
		}
		.history-list {
			max-height: 400px;
			overflow-y: auto;
		}
		.history-entry {
			display: flex;
			align-items: flex-start;
			gap: 0.75rem;
			padding: 0.75rem;
			border-bottom: 1px solid rgba(42, 42, 58, 0.5);
			transition: background 0.2s;
		}
		.history-entry:hover { background: var(--bg-card); border-radius: 8px; }
		.history-entry:last-child { border-bottom: none; }
		.history-clickable { cursor: pointer; }
		.history-clickable:hover { background: rgba(0, 255, 135, 0.08); border-radius: 8px; }
		.history-view-hint { color: var(--accent-primary); font-size: 0.7rem; margin-left: 0.5rem; }
		.history-icon {
			flex-shrink: 0;
			width: 32px;
			height: 32px;
			border-radius: 8px;
			display: flex;
			align-items: center;
			justify-content: center;
			font-size: 0.85rem;
		}
		.history-icon.filter-type {
			background: rgba(0, 255, 136, 0.1);
			color: var(--accent-primary);
		}
		.history-icon.search-type {
			background: rgba(0, 191, 255, 0.1);
			color: #00bfff;
		}
		.history-details { flex: 1; min-width: 0; }
		.history-summary {
			font-size: 0.85rem;
			color: var(--text-primary);
			margin-bottom: 0.25rem;
		}
		.history-meta {
			font-size: 0.7rem;
			color: var(--text-muted);
			display: flex;
			gap: 0.75rem;
			flex-wrap: wrap;
		}
		.history-tags {
			display: flex;
			flex-wrap: wrap;
			gap: 0.3rem;
			margin-top: 0.3rem;
		}
		.history-tag {
			font-size: 0.6rem;
			padding: 0.1rem 0.35rem;
			border-radius: 3px;
			background: rgba(0, 255, 136, 0.1);
			color: var(--accent-primary);
			border: 1px solid rgba(0, 255, 136, 0.2);
		}
		.history-result-count {
			font-family: 'JetBrains Mono', monospace;
			font-size: 0.75rem;
			color: var(--accent-primary);
			flex-shrink: 0;
		}
		.history-replay {
			flex-shrink: 0;
			background: none;
			border: 1px solid var(--border-color);
			color: var(--text-muted);
			padding: 0.3rem 0.5rem;
			border-radius: 5px;
			font-size: 0.65rem;
			cursor: pointer;
			transition: all 0.2s;
		}
		.history-replay:hover {
			border-color: var(--accent-primary);
			color: var(--accent-primary);
		}
		.history-empty {
			text-align: center;
			padding: 2rem;
			color: var(--text-muted);
			font-size: 0.85rem;
		}
		.admin-history-user {
			font-size: 0.9rem;
			font-weight: 600;
			color: var(--accent-primary);
			padding: 0.5rem 0;
			margin-top: 0.75rem;
			border-top: 1px solid var(--border-color);
		}
		.admin-history-user:first-child {
			margin-top: 0;
			border-top: none;
		}

		.save-btn {
			background: none;
			border: 1px solid var(--border-color);
			color: var(--text-muted);
			padding: 0.4rem 0.8rem;
			border-radius: 8px;
			font-size: 0.8rem;
			cursor: pointer;
			transition: all 0.2s;
			display: inline-flex;
			align-items: center;
			gap: 0.3rem;
			font-family: 'Outfit', sans-serif;
		}
		.save-btn:hover {
			border-color: var(--warning);
			color: var(--warning);
		}
		.save-btn.saved {
			border-color: var(--warning);
			color: var(--warning);
			background: rgba(255, 165, 2, 0.1);
		}
		.job-actions {
			display: flex;
			gap: 0.6rem;
			align-items: center;
			flex-wrap: wrap;
			margin-top: 0.25rem;
		}

		.saved-job-card {
			background: var(--bg-card);
			border: 1px solid var(--border-color);
			border-radius: 10px;
			padding: 1rem;
			margin-bottom: 0.75rem;
			transition: all 0.2s;
		}
		.saved-job-card:hover {
			border-color: var(--accent-primary);
			background: var(--bg-card-hover);
		}
		.saved-job-header {
			display: flex;
			justify-content: space-between;
			align-items: flex-start;
			gap: 0.5rem;
		}
		.saved-job-company {
			font-size: 0.75rem;
			color: var(--text-muted);
			text-transform: uppercase;
			letter-spacing: 0.3px;
			margin-bottom: 0.25rem;
		}
		.saved-job-title {
			font-size: 0.95rem;
			font-weight: 600;
			color: var(--text-primary);
			margin-bottom: 0.35rem;
		}
		.saved-job-meta {
			font-size: 0.75rem;
			color: var(--text-muted);
			display: flex;
			gap: 0.75rem;
			flex-wrap: wrap;
			margin-bottom: 0.5rem;
		}
		.saved-job-actions {
			display: flex;
			gap: 0.5rem;
			align-items: center;
		}

		.cancel-search-btn {
			background: transparent;
			border: 1px solid var(--danger);
			color: var(--danger);
			padding: 0.5rem 1rem;
			border-radius: 8px;
			font-size: 0.8rem;
			font-weight: 600;
			cursor: pointer;
			transition: all 0.2s;
			font-family: 'Outfit', sans-serif;
		}
		.cancel-search-btn:hover {
			background: var(--danger);
			color: white;
		}

		@media (max-width: 768px) {
			.container { padding: 1rem; }
			.logo { font-size: 1.6rem; }
			.filters-grid { grid-template-columns: 1fr 1fr; }
			.stats-bar { gap: 1rem; }
			.jobs-grid { grid-template-columns: 1fr; }
			.toast-container { left: 1rem; right: 1rem; }
			.toast { min-width: auto; }
		}
	</style>
</head>
<body>
	<div id="toast-container" class="toast-container"></div>
	
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
					<select id="filter-category" onchange="userApplyFilters()">
						<option value="">All Categories</option>
					</select>
				</div>
				<div class="filter-group">
					<label>Experience Level</label>
					<select id="filter-level" onchange="userApplyFilters()">
						<option value="">All Levels</option>
					</select>
				</div>
				<div class="filter-group">
					<label>Certification</label>
					<select id="filter-cert" onchange="userApplyFilters()">
						<option value="">Any Certification</option>
					</select>
				</div>
				<div class="filter-group">
					<label>Source</label>
					<select id="filter-source" onchange="userApplyFilters()">
						<option value="">All Sources</option>
					</select>
				</div>
				<div class="filter-group">
					<label>Sort By</label>
					<select id="filter-sort" onchange="userApplyFilters()">
						<option value="newest">Newest First</option>
						<option value="salary_high">Highest Salary</option>
						<option value="salary_low">Lowest Salary</option>
						<option value="company">Company A-Z</option>
					</select>
				</div>
				<div class="filter-group">
					<label>Options</label>
					<div class="checkbox-group">
						<input type="checkbox" id="filter-remote" onchange="userApplyFilters()">
						<span>Remote Only</span>
					</div>
				</div>
			<div class="filter-group">
				<label>&nbsp;</label>
				<div class="checkbox-group">
					<input type="checkbox" id="filter-show-excluded" onchange="userApplyFilters()">
					<span>Show Excluded</span>
				</div>
			</div>
			<div class="filter-group">
				<label>&nbsp;</label>
				<div class="checkbox-group">
					<input type="checkbox" id="filter-salary-only" onchange="userApplyFilters()">
					<span>💰 Only Jobs with Salary</span>
				</div>
			</div>
			<div class="filter-group" id="filter-exact-match-group" style="display:none">
				<label>&nbsp;</label>
				<div class="checkbox-group">
					<input type="checkbox" id="filter-exact-match" onchange="userApplyFilters()">
					<span>🎯 Exact Title Match</span>
				</div>
			</div>
			<div class="filter-group search-group" style="min-width:220px">
				<label>Exclude Title Words</label>
				<input type="text" id="filter-exclude-words" placeholder="e.g. account, senior, manager" oninput="userApplyFilters()" style="width:100%">
			</div>
			<div id="search-container" class="filter-group search-group hidden">
				<label>Search (Title / Keywords)</label>
				<input type="text" id="search-input" placeholder="Search by title, company, or location..." oninput="userApplyFilters()">
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
			<div class="stat" id="auth-actions">
				<button id="refresh-btn" class="refresh-btn hidden" onclick="refreshJobs()">
					<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
						<path d="M21 12a9 9 0 0 0-9-9 9.75 9.75 0 0 0-6.74 2.74L3 8"/>
						<path d="M3 3v5h5"/>
						<path d="M3 12a9 9 0 0 0 9 9 9.75 9.75 0 0 0 6.74-2.74L21 16"/>
						<path d="M16 16h5v5"/>
					</svg>
					<span id="refresh-btn-text">Refresh Jobs</span>
				</button>
				<button id="saved-jobs-btn" class="btn-login hidden" onclick="toggleSavedJobsPanel()">&#9733; Saved</button>
				<button id="clear-results-btn" class="btn-login hidden" onclick="clearResultsForUser()" style="border-color:var(--warning);color:var(--warning)">&#10005; Clear Jobs</button>
				<button id="restore-results-btn" class="btn-login hidden" onclick="restoreResults()" style="border-color:var(--accent-secondary);color:var(--accent-secondary)">&#8634; Restore Jobs</button>
				<button id="history-btn" class="btn-login hidden" onclick="toggleHistoryPanel()">&#128337; History</button>
				<button id="alerts-btn" class="btn-login hidden" onclick="toggleAlertsPanel()">&#128276; Alerts</button>
				<button id="admin-panel-btn" class="btn-login hidden" onclick="toggleAdminPanel()">&#9881; Admin</button>
				<button id="admin-history-btn" class="btn-login hidden" onclick="toggleAdminHistoryPanel()">&#128337; All History</button>
				<div id="user-info" style="display:none">
					<span id="user-display" class="auth-hint"></span>
					<button class="btn-sm" onclick="logout()" style="margin-left:0.5rem;padding:0.2rem 0.5rem;font-size:0.7rem">Logout</button>
				</div>
				<div id="anon-actions" style="display:none">
					<button class="btn-login" onclick="login()">Sign In</button>
				</div>
			</div>
		</div>

		<div id="custom-search-panel" class="filters-panel hidden">
			<div class="filters-header">
				<h2>Custom Job Search</h2>
				<button id="close-search-results" class="btn-reset hidden" onclick="closeSearchResults()">Back to Default Jobs</button>
			</div>
			<p style="color:var(--text-secondary);font-size:0.85rem;margin-bottom:1rem">Run a new scraper search with a custom keyword. This searches across LinkedIn, Greenhouse, and Lever for any job title or keyword — not just the default offensive security roles.</p>
			<div style="display:flex;gap:0.75rem;align-items:center;flex-wrap:wrap">
				<input type="text" id="custom-search-input" placeholder="e.g. Cloud Security, DevSecOps, SIEM Engineer..." style="flex:1;min-width:200px;padding:0.65rem 1rem;background:var(--bg-card);border:1px solid var(--border-color);border-radius:8px;color:var(--text-primary);font-size:0.95rem;font-family:'Outfit',sans-serif" onkeydown="if(event.key==='Enter')runCustomSearch()">
				<button id="custom-search-btn" onclick="runCustomSearch()" class="refresh-btn" style="flex-shrink:0">
					<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="11" cy="11" r="8"/><path d="m21 21-4.35-4.35"/></svg>
					<span id="custom-search-btn-text">Search</span>
				</button>
				<button id="cancel-search-btn" class="cancel-search-btn hidden" onclick="cancelSearch()">Cancel</button>
			</div>
			<div id="search-status-msg" style="display:none;margin-top:0.75rem;padding:0.6rem 0.8rem;background:var(--bg-card);border-radius:8px;font-size:0.85rem;color:var(--text-secondary)"></div>
		</div>

		<div id="saved-jobs-panel" class="history-panel hidden">
			<div class="history-header">
				<h2 onclick="toggleSavedJobsPanel()">&#9733; Saved Jobs</h2>
				<div class="history-header-actions">
					<span id="saved-jobs-count" class="auth-hint"></span>
					<button class="btn-reset" onclick="toggleSavedJobsPanel()">Close</button>
				</div>
			</div>
			<div id="saved-jobs-list" class="history-list">
				<div class="history-empty">Loading...</div>
			</div>
		</div>

		<div id="history-panel" class="history-panel hidden">
			<div class="history-header">
				<h2 onclick="toggleHistoryPanel()">&#128337; Search History</h2>
				<div class="history-header-actions">
					<button class="btn-sm btn-danger" onclick="clearHistory()">Clear History</button>
					<button class="btn-reset" onclick="toggleHistoryPanel()">Close</button>
				</div>
			</div>
			<div id="history-list" class="history-list">
				<div class="history-empty">Loading...</div>
			</div>
		</div>

		<div id="admin-history-panel" class="history-panel hidden">
			<div class="history-header">
				<h2 onclick="toggleAdminHistoryPanel()">&#128337; All Users History (Admin)</h2>
				<div class="history-header-actions">
					<button class="btn-reset" onclick="toggleAdminHistoryPanel()">Close</button>
				</div>
			</div>
			<div id="admin-history-list" class="history-list">
				<div class="history-empty">Loading...</div>
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

		<div id="alerts-panel" class="history-panel hidden" style="max-width:700px">
			<div class="history-header">
				<h2>&#128276; Alerts &amp; Schedules</h2>
				<button onclick="toggleAlertsPanel()" class="btn-login" style="padding:0.3rem 0.8rem;font-size:0.8rem">Close</button>
			</div>

			<div style="padding:1rem">
				<h3 style="color:var(--accent-primary);margin-bottom:0.75rem">Telegram Setup</h3>
				<p style="color:var(--text-secondary);font-size:0.85rem;margin-bottom:0.75rem">Enter your Telegram Bot Token and Chat ID to receive job alerts. <a href="https://core.telegram.org/bots/tutorial" target="_blank" rel="noopener" style="color:var(--accent-primary)">How to create a bot</a></p>
				<div style="display:flex;flex-direction:column;gap:0.5rem;margin-bottom:0.75rem">
					<input type="password" id="tg-bot-token" placeholder="Bot Token (from @BotFather)" style="padding:0.5rem 0.75rem;background:var(--bg-card);border:1px solid var(--border-color);border-radius:6px;color:var(--text-primary);font-size:0.85rem;font-family:'Outfit',sans-serif">
					<input type="text" id="tg-chat-id" placeholder="Chat ID (from @userinfobot)" style="padding:0.5rem 0.75rem;background:var(--bg-card);border:1px solid var(--border-color);border-radius:6px;color:var(--text-primary);font-size:0.85rem;font-family:'Outfit',sans-serif">
				</div>
				<div style="display:flex;gap:0.5rem;align-items:center;flex-wrap:wrap">
					<button onclick="saveTelegramConfig()" class="refresh-btn" style="font-size:0.85rem">Save</button>
					<button onclick="testTelegram()" class="refresh-btn" style="font-size:0.85rem;background:var(--bg-card);border:1px solid var(--accent-primary)">Send Test Message</button>
					<span id="tg-status" style="font-size:0.8rem;color:var(--text-secondary)"></span>
				</div>
			</div>

			<hr style="border-color:var(--border-color);margin:0.5rem 1rem">

			<div style="padding:1rem">
				<h3 style="color:var(--accent-primary);margin-bottom:0.75rem">Scan Schedules</h3>
				<p style="color:var(--text-secondary);font-size:0.85rem;margin-bottom:0.75rem">Schedule automatic scans. Results are sent to your Telegram.</p>

				<div id="schedules-list" style="margin-bottom:1rem"></div>

				<div style="background:var(--bg-card);border:1px solid var(--border-color);border-radius:8px;padding:1rem">
					<h4 style="color:var(--text-primary);margin-bottom:0.75rem">Add Schedule</h4>
					<div style="display:flex;flex-direction:column;gap:0.5rem">
						<input type="text" id="sched-name" placeholder="Schedule name (e.g. Daily OffSec Check)" style="padding:0.5rem 0.75rem;background:var(--bg-primary);border:1px solid var(--border-color);border-radius:6px;color:var(--text-primary);font-size:0.85rem;font-family:'Outfit',sans-serif">
						<div style="display:flex;gap:0.5rem;flex-wrap:wrap">
							<select id="sched-type" onchange="onSchedTypeChange()" style="flex:1;min-width:140px;padding:0.5rem;background:var(--bg-primary);border:1px solid var(--border-color);border-radius:6px;color:var(--text-primary);font-size:0.85rem;font-family:'Outfit',sans-serif">
								<option value="default">Default OffSec Roles</option>
								<option value="custom">Custom Search</option>
							</select>
							<input type="text" id="sched-query" placeholder="Search query..." style="flex:1;min-width:140px;padding:0.5rem;background:var(--bg-primary);border:1px solid var(--border-color);border-radius:6px;color:var(--text-primary);font-size:0.85rem;font-family:'Outfit',sans-serif;display:none">
						</div>
						<div style="display:flex;gap:0.5rem;flex-wrap:wrap;align-items:center">
							<select id="sched-frequency" onchange="onSchedFreqChange()" style="min-width:120px;padding:0.5rem;background:var(--bg-primary);border:1px solid var(--border-color);border-radius:6px;color:var(--text-primary);font-size:0.85rem;font-family:'Outfit',sans-serif">
								<option value="daily">Daily</option>
								<option value="weekdays">Weekdays</option>
								<option value="weekly">Weekly (Mon)</option>
								<option value="custom">Custom Days</option>
							</select>
							<div id="sched-custom-days" style="display:none;gap:0.25rem;flex-wrap:wrap">
								<label style="font-size:0.75rem;color:var(--text-secondary);cursor:pointer"><input type="checkbox" value="0" class="sched-day-cb"> Sun</label>
								<label style="font-size:0.75rem;color:var(--text-secondary);cursor:pointer"><input type="checkbox" value="1" class="sched-day-cb"> Mon</label>
								<label style="font-size:0.75rem;color:var(--text-secondary);cursor:pointer"><input type="checkbox" value="2" class="sched-day-cb"> Tue</label>
								<label style="font-size:0.75rem;color:var(--text-secondary);cursor:pointer"><input type="checkbox" value="3" class="sched-day-cb"> Wed</label>
								<label style="font-size:0.75rem;color:var(--text-secondary);cursor:pointer"><input type="checkbox" value="4" class="sched-day-cb"> Thu</label>
								<label style="font-size:0.75rem;color:var(--text-secondary);cursor:pointer"><input type="checkbox" value="5" class="sched-day-cb"> Fri</label>
								<label style="font-size:0.75rem;color:var(--text-secondary);cursor:pointer"><input type="checkbox" value="6" class="sched-day-cb"> Sat</label>
							</div>
							<span style="font-size:0.85rem;color:var(--text-secondary)">at</span>
							<input type="time" id="sched-time" value="09:00" style="padding:0.5rem;background:var(--bg-primary);border:1px solid var(--border-color);border-radius:6px;color:var(--text-primary);font-size:0.85rem;font-family:'Outfit',sans-serif">
						</div>
						<div style="display:flex;gap:0.5rem;align-items:center">
							<label style="font-size:0.8rem;color:var(--text-secondary);cursor:pointer"><input type="checkbox" id="sched-notify-empty"> Notify even if no results</label>
						</div>
						<button onclick="addSchedule()" class="refresh-btn" style="font-size:0.85rem;align-self:flex-start">Add Schedule</button>
					</div>
				</div>
			</div>
		</div>

		<div id="admin-panel" class="admin-panel hidden">
			<h2 onclick="toggleAdminPanel()">&#9881; Admin Panel</h2>
			<div class="admin-section">
				<h3>Generate Registration Token</h3>
				<div class="token-form">
					<select id="token-role" class="admin-select">
						<option value="user">Regular User</option>
						<option value="admin">Admin</option>
					</select>
					<select id="token-expiry" class="admin-select">
						<option value="24">24 hours</option>
						<option value="168" selected>7 days</option>
						<option value="720">30 days</option>
					</select>
					<button onclick="generateToken()" class="btn-primary" style="flex:0;padding:0.5rem 1rem;white-space:nowrap">Generate Token</button>
				</div>
				<div id="generated-token" class="generated-token hidden">
					<label>Share this token with the new user:</label>
					<div class="token-display">
						<code id="token-value"></code>
						<button onclick="copyToken()" class="btn-sm">Copy</button>
					</div>
				</div>
			</div>
			<div class="admin-section">
				<h3>Registration Tokens</h3>
				<div id="tokens-list"><p class="auth-hint">Loading...</p></div>
			</div>
			<div class="admin-section">
				<h3>Users</h3>
				<div id="users-list"><p class="auth-hint">Loading...</p></div>
			</div>
		</div>

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

			// Populate source filter from available job data
			var sourceSelect = document.getElementById('filter-source');
			var sources = {};
			allJobs.forEach(function(j) {
				if (j.job.source) sources[j.job.source] = true;
			});
			Object.keys(sources).sort().forEach(function(src) {
				var opt = document.createElement('option');
				opt.value = src;
				opt.textContent = src.charAt(0).toUpperCase() + src.slice(1);
				sourceSelect.appendChild(opt);
			});

			document.getElementById('stat-total').textContent = allJobs.length;
			
			// Set default filters: sort by highest salary and show only jobs with salary
			document.getElementById('filter-sort').value = 'salary_high';
			document.getElementById('filter-salary-only').checked = true;
		}

	function applyFilters() {
		var category = document.getElementById('filter-category').value;
		var level = document.getElementById('filter-level').value;
		var cert = document.getElementById('filter-cert').value;
		var source = document.getElementById('filter-source').value;
		var sortBy = document.getElementById('filter-sort').value;
		var remoteOnly = document.getElementById('filter-remote').checked;
		var showExcluded = document.getElementById('filter-show-excluded').checked;
		var salaryOnly = document.getElementById('filter-salary-only').checked;
		var searchInput = document.getElementById('search-input');
		var searchQuery = searchInput ? searchInput.value.trim() : '';
		var excludeWordsRaw = (document.getElementById('filter-exclude-words').value || '').trim();
		var excludeWords = excludeWordsRaw ? excludeWordsRaw.split(',').map(function(w) { return w.trim().toLowerCase(); }).filter(function(w) { return w.length > 0; }) : [];
		var exactMatch = document.getElementById('filter-exact-match').checked;

		var baseJobs = viewingSearchResults ? currentSearchResults : allJobs;

		var filtered = baseJobs.filter(function(j) {
			if (!viewingSearchResults) {
				if (!showExcluded && j.tags.is_excluded) return false;
				if (category && j.tags.categories.indexOf(category) === -1) return false;
				if (cert && j.tags.certifications.indexOf(cert) === -1) return false;
			}
			if (exactMatch && viewingSearchResults && currentSearchQuery) {
				var titleLower = (j.job.title || '').toLowerCase();
				if (titleLower.indexOf(currentSearchQuery.toLowerCase()) === -1) return false;
			}
			if (level && j.tags.level !== level) return false;
			if (source && j.job.source !== source) return false;
			if (remoteOnly && !j.tags.is_remote) return false;
			if (salaryOnly && !hasSalary(j)) return false;
			if (excludeWords.length > 0) {
				var titleLower = (j.job.title || '').toLowerCase();
				for (var i = 0; i < excludeWords.length; i++) {
					if (titleLower.indexOf(excludeWords[i]) !== -1) return false;
				}
			}
			if (searchQuery) {
				var q = searchQuery.toLowerCase();
				var inTitle = (j.job.title || '').toLowerCase().indexOf(q) !== -1;
				var inCompany = (j.job.company || '').toLowerCase().indexOf(q) !== -1;
				var inLocation = (j.job.location || '').toLowerCase().indexOf(q) !== -1;
				if (!inTitle && !inCompany && !inLocation) return false;
			}
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

	function userApplyFilters() {
		applyFilters();
		if (!viewingSearchResults) {
			var showing = document.getElementById('stat-showing').textContent;
			saveFilterHistory(parseInt(showing) || 0);
		}
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

		var savedJobIDs = {};

		function renderJobCard(taggedJob) {
			var job = taggedJob.job;
			var tags = taggedJob.tags;
			
			var isNew = (Date.now() - new Date(job.first_seen).getTime()) < 24 * 60 * 60 * 1000;
			var isSaved = !!savedJobIDs[job.id];
			
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
					var levelsUrl = 'https://www.levels.fyi/companies/' + encodeURIComponent(job.company.toLowerCase().replace(/\s+/g, '-')) + '/salaries/';
					salaryHTML += '<span class="salary-badge levels">💰 ' + escapeHtml(job.level_salary) + ' <small>(<a href="' + levelsUrl + '" target="_blank" rel="noopener" class="levels-link">Levels.fyi</a>)</small></span>';
				}
				if (job.salary_range) {
					salaryHTML += '<span class="salary-badge posting">💵 ' + escapeHtml(job.salary_range) + ' <small>(Posted)</small></span>';
				}
				salaryHTML += '</div>';
			}

			var cardClass = 'job-card' + (tags.is_excluded ? ' excluded' : '');
			var newBadge = isNew ? '<span class="badge badge-new">NEW</span>' : '';

			var salaryStr = job.level_salary || job.salary_range || '';
			var saveDataAttr = 'data-jobid="' + escapeHtml(job.id) + '" '
				+ 'data-company="' + escapeHtml(job.company) + '" '
				+ 'data-title="' + escapeHtml(job.title) + '" '
				+ 'data-location="' + escapeHtml(job.location) + '" '
				+ 'data-url="' + escapeHtml(job.url) + '" '
				+ 'data-source="' + escapeHtml(job.source) + '" '
				+ 'data-salary="' + escapeHtml(salaryStr) + '"';

			var saveBtn = isAuthenticated
				? '<button class="save-btn' + (isSaved ? ' saved' : '') + '" id="save-btn-' + escapeHtml(job.id) + '" onclick="toggleSaveJob(this)" ' + saveDataAttr + '>'
					+ (isSaved ? '&#9733; Saved' : '&#9734; Save')
					+ '</button>'
				: '';

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
				'<div class="job-actions">' +
					'<a href="' + job.url + '" target="_blank" rel="noopener noreferrer" class="apply-btn">' +
						'Apply Now ' +
						'<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><line x1="7" y1="17" x2="17" y2="7"/><polyline points="7 7 17 7 17 17"/></svg>' +
					'</a>' +
					saveBtn +
				'</div>' +
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
		document.getElementById('filter-source').value = '';
		document.getElementById('filter-sort').value = 'newest';
		document.getElementById('filter-remote').checked = false;
		document.getElementById('filter-show-excluded').checked = false;
		document.getElementById('filter-salary-only').checked = false;
		document.getElementById('filter-exact-match').checked = false;
		document.getElementById('filter-exclude-words').value = '';
		var searchInput = document.getElementById('search-input');
		if (searchInput) searchInput.value = '';
		applyFilters();
	}

		initFilters();
		applyFilters();

		function showToast(type, title, message, duration) {
			duration = duration || 5000;
			var container = document.getElementById('toast-container');
			var toast = document.createElement('div');
			toast.className = 'toast ' + type;
			
			var icons = {
				success: '✓',
				error: '✕',
				warning: '⚠',
				info: 'ℹ'
			};
			
			toast.innerHTML = 
				'<span class="toast-icon">' + icons[type] + '</span>' +
				'<div class="toast-content">' +
					'<div class="toast-title">' + title + '</div>' +
					'<div class="toast-message">' + message + '</div>' +
				'</div>' +
				'<button class="toast-close" onclick="closeToast(this.parentElement)">&times;</button>';
			
			container.appendChild(toast);
			
			if (duration > 0) {
				setTimeout(function() { closeToast(toast); }, duration);
			}
			
			return toast;
		}
		
		function closeToast(toast) {
			if (!toast || toast.classList.contains('hiding')) return;
			toast.classList.add('hiding');
			setTimeout(function() { toast.remove(); }, 300);
		}
		
		var refreshPollingInterval = null;
		
		function checkRefreshStatus() {
			fetch('/api/refresh/status', { credentials: 'same-origin' })
				.then(function(res) { return res.json(); })
				.then(function(data) {
					var btn = document.getElementById('refresh-btn');
					var btnText = document.getElementById('refresh-btn-text');
					
					if (data.is_refreshing) {
						btn.disabled = true;
						btn.classList.add('refreshing');
						btnText.textContent = 'Refreshing...';
						if (!refreshPollingInterval) {
							refreshPollingInterval = setInterval(pollRefreshStatus, 5000);
						}
					} else {
						btn.classList.remove('refreshing');
						if (data.can_refresh) {
							btn.disabled = false;
							btnText.textContent = 'Refresh Jobs';
						} else {
							btn.disabled = true;
							btnText.textContent = 'Updated Today';
						}
						if (refreshPollingInterval) {
							clearInterval(refreshPollingInterval);
							refreshPollingInterval = null;
						}
					}
				})
				.catch(function(err) {
					console.error('Error checking refresh status:', err);
				});
		}
		
		function pollRefreshStatus() {
			fetch('/api/refresh/status', { credentials: 'same-origin' })
				.then(function(res) { return res.json(); })
				.then(function(data) {
					if (!data.is_refreshing) {
						clearInterval(refreshPollingInterval);
						refreshPollingInterval = null;
						showToast('success', 'Refresh Complete', 'Jobs have been updated. Reloading page...', 3000);
						setTimeout(function() { location.reload(); }, 2000);
					}
				})
				.catch(function(err) {
					console.error('Error polling refresh status:', err);
				});
		}
		
		function refreshJobs() {
			var btn = document.getElementById('refresh-btn');
			var btnText = document.getElementById('refresh-btn-text');
			
			btn.disabled = true;
			btn.classList.add('refreshing');
			btnText.textContent = 'Starting...';
			
			fetch('/api/refresh', { method: 'POST', credentials: 'same-origin' })
				.then(function(res) { return res.json().then(function(data) { return { status: res.status, data: data }; }); })
				.then(function(result) {
					var status = result.status;
					var data = result.data;
					
					if (status === 429) {
						btn.classList.remove('refreshing');
						btn.disabled = true;
						btnText.textContent = 'Updated Today';
						showToast('warning', 'Already Updated', data.message, 6000);
					} else if (status === 409) {
						btnText.textContent = 'Refreshing...';
						showToast('info', 'In Progress', data.message, 4000);
						refreshPollingInterval = setInterval(pollRefreshStatus, 5000);
					} else if (data.success) {
						btnText.textContent = 'Refreshing...';
						showToast('info', 'Refresh Started', data.message, 0);
						refreshPollingInterval = setInterval(pollRefreshStatus, 5000);
					} else {
						btn.classList.remove('refreshing');
						btn.disabled = false;
						btnText.textContent = 'Refresh Jobs';
						showToast('error', 'Error', data.message || 'Failed to start refresh', 5000);
					}
				})
				.catch(function(err) {
					btn.classList.remove('refreshing');
					btn.disabled = false;
					btnText.textContent = 'Refresh Jobs';
					showToast('error', 'Connection Error', 'Could not connect to server. Please try again.', 5000);
				});
		}
		
		var isAuthenticated = false;
		var currentUser = null;
		var currentRole = null;
		var viewingSearchResults = false;
		var currentSearchResults = [];
		var currentSearchQuery = '';
		var resultsCleared = false;
		var lastPartialCount = 0;
		var searchPollingInterval = null;

		function checkAuth() {
			fetch('/api/auth/check', { credentials: 'same-origin' })
				.then(function(res) { return res.json(); })
				.then(function(data) {
					isAuthenticated = data.authenticated;
					currentUser = data.username || null;
					currentRole = data.role || null;
					updateAuthUI();
				})
				.catch(function() {
					isAuthenticated = false;
					currentUser = null;
					currentRole = null;
					updateAuthUI();
				});
		}

		function updateAuthUI() {
			var searchContainer = document.getElementById('search-container');
			var customSearchPanel = document.getElementById('custom-search-panel');
			var refreshBtn = document.getElementById('refresh-btn');
			var adminPanelBtn = document.getElementById('admin-panel-btn');
			var adminHistoryBtn = document.getElementById('admin-history-btn');
			var historyBtn = document.getElementById('history-btn');
			var savedJobsBtn = document.getElementById('saved-jobs-btn');
			var alertsBtn = document.getElementById('alerts-btn');
			var userInfo = document.getElementById('user-info');
			var userDisplay = document.getElementById('user-display');
			var anonActions = document.getElementById('anon-actions');

			var clearResultsBtn = document.getElementById('clear-results-btn');
			var restoreResultsBtn = document.getElementById('restore-results-btn');
			if (isAuthenticated) {
				searchContainer.classList.remove('hidden');
				customSearchPanel.classList.remove('hidden');
				historyBtn.classList.remove('hidden');
				savedJobsBtn.classList.remove('hidden');
				alertsBtn.classList.remove('hidden');
				if (!resultsCleared) clearResultsBtn.classList.remove('hidden');
				userInfo.style.display = '';
				userDisplay.textContent = currentUser + ' (' + currentRole + ')';
				anonActions.style.display = 'none';
				loadSavedJobIDs();
				checkActiveSearch();

				if (currentRole === 'admin') {
					refreshBtn.classList.remove('hidden');
					adminPanelBtn.classList.remove('hidden');
					adminHistoryBtn.classList.remove('hidden');
					checkRefreshStatus();
				} else {
					refreshBtn.classList.add('hidden');
					adminPanelBtn.classList.add('hidden');
					adminHistoryBtn.classList.add('hidden');
				}
			} else {
				searchContainer.classList.add('hidden');
				customSearchPanel.classList.add('hidden');
				refreshBtn.classList.add('hidden');
				adminPanelBtn.classList.add('hidden');
				adminHistoryBtn.classList.add('hidden');
				historyBtn.classList.add('hidden');
				savedJobsBtn.classList.add('hidden');
				alertsBtn.classList.add('hidden');
				clearResultsBtn.classList.add('hidden');
				restoreResultsBtn.classList.add('hidden');
				userInfo.style.display = 'none';
				anonActions.style.display = '';
			}
		}

		function login() { window.location.href = '/login'; }

		function logout() {
			window.location.href = '/logout';
		}

		// --- Custom Search ---

		function checkActiveSearch() {
			fetch('/api/search/status', { credentials: 'same-origin' })
				.then(function(res) { return res.json(); })
				.then(function(data) {
					if (data.is_searching) {
						var btn = document.getElementById('custom-search-btn');
						var btnText = document.getElementById('custom-search-btn-text');
						var cancelBtn = document.getElementById('cancel-search-btn');
						var statusMsg = document.getElementById('search-status-msg');
						var input = document.getElementById('custom-search-input');
						btn.disabled = true;
						btn.classList.add('refreshing');
						btnText.textContent = 'Searching...';
						cancelBtn.classList.remove('hidden');
						statusMsg.style.display = 'block';
						statusMsg.textContent = data.message || ('Searching for: ' + data.query + '...');
						if (data.query) input.value = data.query;

						var partials = data.partial_results || [];
						if (partials.length > 0) {
							showSearchResults(partials, data.query);
							lastPartialCount = partials.length;
						} else {
							var container = document.getElementById('jobs-container');
							container.innerHTML = '<div class="empty-state"><div class="searching-spinner"></div><h2>Searching for "' + escapeHtml(data.query) + '"</h2><p>Search in progress. This page reconnected to the running search.</p></div>';
							document.getElementById('stat-showing').textContent = '0';
							viewingSearchResults = true;
							currentSearchQuery = data.query || '';
							document.getElementById('close-search-results').classList.remove('hidden');
							document.getElementById('filter-exact-match-group').style.display = '';
							toggleSearchFilters(true);
						}

						if (!searchPollingInterval) {
							searchPollingInterval = setInterval(pollSearchStatus, 4000);
						}
					} else if (data.results && data.results.length > 0) {
						showSearchResults(data.results, data.query);
						var statusMsg = document.getElementById('search-status-msg');
						statusMsg.style.display = 'block';
						statusMsg.textContent = 'Found ' + data.results.length + ' jobs for "' + data.query + '"';
					}
				})
				.catch(function() {});
		}

		function runCustomSearch() {
			var input = document.getElementById('custom-search-input');
			var query = input.value.trim();
			if (query.length < 3) {
				showToast('warning', 'Too Short', 'Search query must be at least 3 characters');
				return;
			}
			var btn = document.getElementById('custom-search-btn');
			var btnText = document.getElementById('custom-search-btn-text');
			var cancelBtn = document.getElementById('cancel-search-btn');
			var statusMsg = document.getElementById('search-status-msg');
			btn.disabled = true;
			btn.classList.add('refreshing');
			btnText.textContent = 'Searching...';
			cancelBtn.classList.remove('hidden');
			statusMsg.style.display = 'block';
			statusMsg.textContent = 'Searching for: ' + query + '...';

			var container = document.getElementById('jobs-container');
			container.innerHTML = '<div class="empty-state"><div class="searching-spinner"></div><h2>Searching for "' + escapeHtml(query) + '"</h2><p>Scanning LinkedIn, Greenhouse, and Lever. This may take a few minutes (5 min timeout).</p></div>';
			document.getElementById('stat-showing').textContent = '0';
			viewingSearchResults = true;
			currentSearchResults = [];
			currentSearchQuery = query;
			lastPartialCount = 0;
			document.getElementById('close-search-results').classList.remove('hidden');
			document.getElementById('filter-exact-match-group').style.display = '';
			toggleSearchFilters(true);

			fetch('/api/search', {
				method: 'POST',
				credentials: 'same-origin',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({ query: query })
			})
			.then(function(res) { return res.json().then(function(d) { return { status: res.status, data: d }; }); })
			.then(function(result) {
				if (result.data.success) {
					if (!searchPollingInterval) {
						searchPollingInterval = setInterval(pollSearchStatus, 4000);
					}
				} else {
					statusMsg.textContent = result.data.error || result.data.message || 'Search failed';
					resetSearchUI();
				}
			})
			.catch(function() {
				statusMsg.textContent = 'Connection error. Please try again.';
				resetSearchUI();
			});
		}

		function cancelSearch() {
			fetch('/api/search/cancel', { method: 'POST', credentials: 'same-origin' })
				.then(function(res) { return res.json(); })
				.then(function(data) {
					if (data.success) {
						showToast('info', 'Cancelled', data.message);
					}
					if (searchPollingInterval) {
						clearInterval(searchPollingInterval);
						searchPollingInterval = null;
					}
					resetSearchUI();
					document.getElementById('search-status-msg').textContent = 'Search cancelled.';
				})
				.catch(function() {
					showToast('error', 'Error', 'Failed to cancel search');
				});
		}

		function resetSearchUI() {
			var btn = document.getElementById('custom-search-btn');
			var btnText = document.getElementById('custom-search-btn-text');
			var cancelBtn = document.getElementById('cancel-search-btn');
			btn.disabled = false;
			btn.classList.remove('refreshing');
			btnText.textContent = 'Search';
			cancelBtn.classList.add('hidden');
		}

		function pollSearchStatus() {
			fetch('/api/search/status', { credentials: 'same-origin' })
				.then(function(res) { return res.json(); })
				.then(function(data) {
					var statusMsg = document.getElementById('search-status-msg');
					if (data.is_searching) {
						statusMsg.textContent = data.message;
						var pc = data.partial_count || 0;
						if (pc > 0 && pc !== lastPartialCount) {
							lastPartialCount = pc;
							var partialResults = data.partial_results || [];
							if (partialResults.length > 0) {
								showSearchResults(partialResults, data.query);
								statusMsg.textContent = 'Found ' + partialResults.length + ' jobs so far for "' + data.query + '". Still searching other sources...';
							}
						}
						return;
					}
					clearInterval(searchPollingInterval);
					searchPollingInterval = null;
					resetSearchUI();

					var container = document.getElementById('jobs-container');
					if (data.cancelled) {
						statusMsg.textContent = 'Search was cancelled.';
						container.innerHTML = '<div class="empty-state"><h2>Search Cancelled</h2><p>Click "Back to Default Jobs" to return to the default listings, or start a new search.</p></div>';
						return;
					}

					if (data.error) {
						statusMsg.textContent = data.error;
						container.innerHTML = '<div class="empty-state"><h2>Search Failed</h2><p>' + escapeHtml(data.error) + '</p><p>Click "Back to Default Jobs" to return to default listings.</p></div>';
						showToast('error', 'Search Failed', data.error);
						return;
					}

					var results = data.results || [];
					statusMsg.textContent = 'Found ' + results.length + ' jobs for "' + data.query + '"';
					if (results.length > 0) {
						showSearchResults(results, data.query);
						showToast('success', 'Search Complete', 'Found ' + results.length + ' jobs for "' + data.query + '"');
					} else {
						container.innerHTML = '<div class="empty-state"><h2>No results for "' + escapeHtml(data.query) + '"</h2><p>Try a different search term. Click "Back to Default Jobs" to return.</p></div>';
						showToast('info', 'No Results', 'No jobs found for "' + data.query + '"');
					}
				})
				.catch(function() {});
		}

		function showSearchResults(results, query) {
			viewingSearchResults = true;
			currentSearchResults = results;
			currentSearchQuery = query || '';
			document.getElementById('close-search-results').classList.remove('hidden');
			document.getElementById('stat-total').textContent = results.length;
			document.getElementById('filter-exact-match-group').style.display = '';
			toggleSearchFilters(true);
			applyFilters();
		}

		function closeSearchResults() {
			viewingSearchResults = false;
			currentSearchResults = [];
			currentSearchQuery = '';
			document.getElementById('close-search-results').classList.add('hidden');
			document.getElementById('search-status-msg').style.display = 'none';
			document.getElementById('stat-total').textContent = allJobs.length;
			document.getElementById('filter-exact-match').checked = false;
			document.getElementById('filter-exact-match-group').style.display = 'none';
			toggleSearchFilters(false);
			document.getElementById('filter-category').value = '';
			document.getElementById('filter-cert').value = '';
			applyFilters();
		}

		function toggleSearchFilters(isSearch) {
			var catGroup = document.getElementById('filter-category').parentElement;
			var certGroup = document.getElementById('filter-cert').parentElement;
			if (isSearch) {
				catGroup.style.display = 'none';
				certGroup.style.display = 'none';
			} else {
				catGroup.style.display = '';
				certGroup.style.display = '';
			}
		}

		function clearResultsForUser() {
			resultsCleared = true;
			var container = document.getElementById('jobs-container');
			container.innerHTML = '<div class="empty-state"><h2>Results Cleared</h2><p>Use Custom Job Search above to search for specific roles, or click Restore Default Jobs to bring back the default listings.</p></div>';
			document.getElementById('stat-showing').textContent = '0';
			document.getElementById('clear-results-btn').classList.add('hidden');
			var restoreBtn = document.getElementById('restore-results-btn');
			if (restoreBtn) restoreBtn.classList.remove('hidden');
		}

		function restoreResults() {
			resultsCleared = false;
			document.getElementById('clear-results-btn').classList.remove('hidden');
			var restoreBtn = document.getElementById('restore-results-btn');
			if (restoreBtn) restoreBtn.classList.add('hidden');
			applyFilters();
		}

		// --- Saved Jobs ---

		function loadSavedJobIDs() {
			fetch('/api/saved/ids', { credentials: 'same-origin' })
				.then(function(res) { return res.json(); })
				.then(function(ids) {
					savedJobIDs = {};
					(ids || []).forEach(function(id) { savedJobIDs[id] = true; });
					if (!viewingSearchResults) {
						applyFilters();
					}
				})
				.catch(function() {});
		}

		function toggleSaveJob(btn) {
			var jobId = btn.getAttribute('data-jobid');
			var isSaved = btn.classList.contains('saved');

			if (isSaved) {
				fetch('/api/saved', {
					method: 'DELETE',
					credentials: 'same-origin',
					headers: { 'Content-Type': 'application/json' },
					body: JSON.stringify({ job_id: jobId })
				})
				.then(function(res) { return res.json(); })
				.then(function(data) {
					if (data.success) {
						delete savedJobIDs[jobId];
						btn.classList.remove('saved');
						btn.innerHTML = '&#9734; Save';
						showToast('info', 'Removed', 'Job removed from saved list');
					}
				});
			} else {
				var payload = {
					job_id: jobId,
					company: btn.getAttribute('data-company'),
					title: btn.getAttribute('data-title'),
					location: btn.getAttribute('data-location'),
					url: btn.getAttribute('data-url'),
					source: btn.getAttribute('data-source'),
					salary: btn.getAttribute('data-salary')
				};
				fetch('/api/saved', {
					method: 'POST',
					credentials: 'same-origin',
					headers: { 'Content-Type': 'application/json' },
					body: JSON.stringify(payload)
				})
				.then(function(res) { return res.json(); })
				.then(function(data) {
					if (data.success) {
						savedJobIDs[jobId] = true;
						btn.classList.add('saved');
						btn.innerHTML = '&#9733; Saved';
						showToast('success', 'Saved', 'Job added to your saved list');
					}
				});
			}
		}

		function toggleSavedJobsPanel() {
			var panel = document.getElementById('saved-jobs-panel');
			if (panel.classList.contains('hidden')) {
				panel.classList.remove('hidden');
				loadSavedJobsList();
			} else {
				panel.classList.add('hidden');
			}
		}

		function loadSavedJobsList() {
			fetch('/api/saved', { credentials: 'same-origin' })
				.then(function(res) { return res.json(); })
				.then(function(data) {
					renderSavedJobsList(data.jobs || []);
					document.getElementById('saved-jobs-count').textContent = (data.count || 0) + ' saved';
				})
				.catch(function() {
					document.getElementById('saved-jobs-list').innerHTML = '<div class="history-empty">Failed to load saved jobs</div>';
				});
		}

		function renderSavedJobsList(jobs) {
			var container = document.getElementById('saved-jobs-list');
			if (!jobs.length) {
				container.innerHTML = '<div class="history-empty">No saved jobs yet. Click the &#9734; Save button on any job card to save it here.</div>';
				return;
			}

			var html = '';
			jobs.forEach(function(job) {
				var salaryHtml = job.salary ? '<span style="color:var(--accent-primary);font-family:JetBrains Mono,monospace;font-size:0.75rem">💰 ' + escapeHtml(job.salary) + '</span>' : '';
				var savedTime = formatHistoryTime(job.saved_at);
				html += '<div class="saved-job-card">' +
					'<div class="saved-job-header">' +
						'<div>' +
							'<div class="saved-job-company">' + escapeHtml(job.company) + '</div>' +
							'<div class="saved-job-title">' + escapeHtml(job.title) + '</div>' +
						'</div>' +
					'</div>' +
					'<div class="saved-job-meta">' +
						'<span>' + escapeHtml(job.location) + '</span>' +
						'<span>' + escapeHtml(job.source) + '</span>' +
						'<span>Saved ' + savedTime + '</span>' +
					'</div>' +
					(salaryHtml ? '<div style="margin-bottom:0.5rem">' + salaryHtml + '</div>' : '') +
					'<div class="saved-job-actions">' +
						'<a href="' + escapeHtml(job.url) + '" target="_blank" rel="noopener noreferrer" class="btn-sm" style="text-decoration:none">View Listing</a>' +
						'<button class="btn-sm btn-danger" onclick="unsaveJob(\'' + escapeHtml(job.job_id) + '\')">Remove</button>' +
					'</div>' +
				'</div>';
			});
			container.innerHTML = html;
		}

		function unsaveJob(jobId) {
			fetch('/api/saved', {
				method: 'DELETE',
				credentials: 'same-origin',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({ job_id: jobId })
			})
			.then(function(res) { return res.json(); })
			.then(function(data) {
				if (data.success) {
					delete savedJobIDs[jobId];
					loadSavedJobsList();
					var cardBtn = document.getElementById('save-btn-' + jobId);
					if (cardBtn) {
						cardBtn.classList.remove('saved');
						cardBtn.innerHTML = '&#9734; Save';
					}
					showToast('info', 'Removed', 'Job removed from saved list');
				}
			});
		}

		// --- Admin Panel ---

		function toggleAdminPanel() {
			var panel = document.getElementById('admin-panel');
			if (panel.classList.contains('hidden')) {
				panel.classList.remove('hidden');
				loadAdminData();
			} else {
				panel.classList.add('hidden');
			}
		}

		function generateToken() {
			var role = document.getElementById('token-role').value;
			var expiry = parseInt(document.getElementById('token-expiry').value);
			fetch('/api/admin/tokens', {
				method: 'POST',
				credentials: 'same-origin',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({ role: role, expires_in_hours: expiry })
			})
			.then(function(res) { return res.json(); })
			.then(function(data) {
				if (data.token) {
					document.getElementById('token-value').textContent = data.token;
					document.getElementById('generated-token').classList.remove('hidden');
					showToast('success', 'Token Generated', 'Registration token created for ' + role + ' role');
					loadAdminData();
				} else {
					showToast('error', 'Error', data.error || 'Failed to generate token');
				}
			});
		}

		function copyToken() {
			var t = document.getElementById('token-value').textContent;
			navigator.clipboard.writeText(t).then(function() {
				showToast('success', 'Copied', 'Token copied to clipboard');
			});
		}

		function loadAdminData() {
			fetch('/api/admin/tokens', { credentials: 'same-origin' })
				.then(function(res) { return res.json(); })
				.then(function(tokens) { renderTokens(tokens || []); });
			fetch('/api/admin/users', { credentials: 'same-origin' })
				.then(function(res) { return res.json(); })
				.then(function(users) { renderUsers(users || []); });
		}

		function renderTokens(tokens) {
			var c = document.getElementById('tokens-list');
			if (!tokens.length) { c.innerHTML = '<p class="auth-hint">No registration tokens</p>'; return; }
			var h = '<table class="admin-table"><thead><tr><th>Token</th><th>Role</th><th>Created By</th><th>Expires</th><th>Status</th><th></th></tr></thead><tbody>';
			tokens.forEach(function(t) {
				var st = t.used ? '<span class="badge-status-used">Used' + (t.used_by ? ' by ' + escapeHtml(t.used_by) : '') + '</span>'
					: t.expired ? '<span class="badge-status-expired">Expired</span>'
					: '<span class="badge-status-active">Active</span>';
				var rc = t.role === 'admin' ? 'badge-role-admin' : 'badge-role-user';
				h += '<tr><td><code>' + escapeHtml(t.token.substring(0,12)) + '...</code></td>'
					+ '<td><span class="' + rc + '">' + t.role + '</span></td>'
					+ '<td>' + escapeHtml(t.created_by) + '</td>'
					+ '<td>' + new Date(t.expires_at).toLocaleDateString() + '</td>'
					+ '<td>' + st + '</td>'
					+ '<td><button class="btn-sm btn-danger" onclick="deleteToken(\'' + t.token + '\')">Del</button></td></tr>';
			});
			c.innerHTML = h + '</tbody></table>';
		}

		function renderUsers(users) {
			var c = document.getElementById('users-list');
			if (!users.length) { c.innerHTML = '<p class="auth-hint">No users</p>'; return; }
			var h = '<table class="admin-table"><thead><tr><th>Username</th><th>Role</th><th>Created</th><th>By</th><th></th></tr></thead><tbody>';
			users.forEach(function(u) {
				var rc = u.role === 'admin' ? 'badge-role-admin' : 'badge-role-user';
				var del = u.username === currentUser ? '' : '<button class="btn-sm btn-danger" onclick="deleteUser(\'' + escapeHtml(u.username) + '\')">Del</button>';
				h += '<tr><td>' + escapeHtml(u.username) + '</td>'
					+ '<td><span class="' + rc + '">' + u.role + '</span></td>'
					+ '<td>' + new Date(u.created_at).toLocaleDateString() + '</td>'
					+ '<td>' + escapeHtml(u.created_by) + '</td>'
					+ '<td>' + del + '</td></tr>';
			});
			c.innerHTML = h + '</tbody></table>';
		}

		function deleteToken(token) {
			if (!confirm('Delete this registration token?')) return;
			fetch('/api/admin/tokens', { method: 'DELETE', credentials: 'same-origin',
				headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ token: token })
			}).then(function(res) { return res.json(); }).then(function() { showToast('success', 'Deleted', 'Token removed'); loadAdminData(); });
		}

		function deleteUser(username) {
			if (!confirm('Delete user "' + username + '"?')) return;
			fetch('/api/admin/users', { method: 'DELETE', credentials: 'same-origin',
				headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ username: username })
			}).then(function(res) { return res.json(); }).then(function(data) {
				if (data.error) { showToast('error', 'Error', data.error); }
				else { showToast('success', 'Deleted', 'User removed'); loadAdminData(); }
			});
		}

		// --- Search History ---

		var lastSavedFilterKey = '';

		function saveFilterHistory(resultCount) {
			if (!isAuthenticated) return;
			var filters = {
				category: document.getElementById('filter-category').value,
				level: document.getElementById('filter-level').value,
				certification: document.getElementById('filter-cert').value,
				sort: document.getElementById('filter-sort').value,
				remote_only: document.getElementById('filter-remote').checked,
				show_excluded: document.getElementById('filter-show-excluded').checked,
				salary_only: document.getElementById('filter-salary-only').checked,
				exclude_words: document.getElementById('filter-exclude-words').value.trim(),
				search_text: document.getElementById('search-input') ? document.getElementById('search-input').value.trim() : ''
			};

			var key = JSON.stringify(filters);
			if (key === lastSavedFilterKey) return;
			lastSavedFilterKey = key;

			fetch('/api/history', {
				method: 'POST',
				credentials: 'same-origin',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({
					type: 'filter',
					filters: filters,
					result_count: resultCount
				})
			}).catch(function() {});
		}

		function saveCustomSearchHistory(query, resultCount) {
			if (!isAuthenticated) return;
			fetch('/api/history', {
				method: 'POST',
				credentials: 'same-origin',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({
					type: 'custom_search',
					query: query,
					result_count: resultCount
				})
			}).catch(function() {});
		}

		function toggleHistoryPanel() {
			var panel = document.getElementById('history-panel');
			if (panel.classList.contains('hidden')) {
				panel.classList.remove('hidden');
				loadHistory();
			} else {
				panel.classList.add('hidden');
			}
		}

		function toggleAdminHistoryPanel() {
			var panel = document.getElementById('admin-history-panel');
			if (panel.classList.contains('hidden')) {
				panel.classList.remove('hidden');
				loadAdminHistoryData();
			} else {
				panel.classList.add('hidden');
			}
		}

		function loadHistory() {
			fetch('/api/history', { credentials: 'same-origin' })
				.then(function(res) { return res.json(); })
				.then(function(data) {
					renderHistory(data.entries || [], 'history-list', false);
				})
				.catch(function() {
					document.getElementById('history-list').innerHTML = '<div class="history-empty">Failed to load history</div>';
				});
		}

		function loadAdminHistoryData() {
			fetch('/api/admin/history', { credentials: 'same-origin' })
				.then(function(res) { return res.json(); })
				.then(function(data) {
					renderAdminHistory(data);
				})
				.catch(function() {
					document.getElementById('admin-history-list').innerHTML = '<div class="history-empty">Failed to load history</div>';
				});
		}

		function renderHistory(entries, containerId, showUsername) {
			var container = document.getElementById(containerId);
			if (!entries || entries.length === 0) {
				container.innerHTML = '<div class="history-empty">No search history yet. Your searches will appear here.</div>';
				return;
			}

			var html = '';
			entries.forEach(function(entry) {
				html += renderHistoryEntry(entry, showUsername);
			});
			container.innerHTML = html;
		}

		function renderAdminHistory(allHistory) {
			var container = document.getElementById('admin-history-list');
			if (!allHistory || Object.keys(allHistory).length === 0) {
				container.innerHTML = '<div class="history-empty">No search history from any users.</div>';
				return;
			}

			var html = '';
			Object.keys(allHistory).sort().forEach(function(username) {
				var entries = allHistory[username];
				html += '<div class="admin-history-user">' + escapeHtml(username) + ' (' + entries.length + ' entries)</div>';
				entries.forEach(function(entry) {
					html += renderHistoryEntry(entry, false);
				});
			});
			container.innerHTML = html;
		}

		function renderHistoryEntry(entry, showUsername) {
			var isCustomSearch = entry.type === 'custom_search';
			var iconClass = isCustomSearch ? 'search-type' : 'filter-type';
			var iconChar = isCustomSearch ? '&#128269;' : '&#9881;';

			var summary = '';
			if (isCustomSearch) {
				summary = 'Searched: <strong>' + escapeHtml(entry.query) + '</strong>';
			} else {
				var parts = [];
				var f = entry.filters || {};
				if (f.category) {
					var catConf = appConfig.Filters && appConfig.Filters.Categories && appConfig.Filters.Categories[f.category];
					parts.push(catConf && catConf.DisplayName ? catConf.DisplayName : f.category);
				}
				if (f.level) {
					var lvlConf = appConfig.Filters && appConfig.Filters.Levels && appConfig.Filters.Levels[f.level];
					parts.push(lvlConf && lvlConf.DisplayName ? lvlConf.DisplayName : f.level);
				}
				if (f.certification) {
					var certConf = appConfig.Filters && appConfig.Filters.Certifications && appConfig.Filters.Certifications[f.certification];
					parts.push(certConf && certConf.DisplayName ? certConf.DisplayName : f.certification);
				}
				if (f.search_text) {
					parts.push('"' + escapeHtml(f.search_text) + '"');
				}
				if (f.remote_only) parts.push('Remote');
				if (f.salary_only) parts.push('With Salary');
				if (f.show_excluded) parts.push('Inc. Excluded');
				summary = parts.length > 0 ? 'Filtered: ' + parts.join(', ') : 'Applied filters';
			}

			var timeStr = formatHistoryTime(entry.timestamp);
			var hasResults = isCustomSearch && entry.results_file && entry.result_count > 0;
			var clickable = hasResults ? ' history-clickable' : '';
			var onclick = hasResults ? ' onclick="loadHistoryResults(\'' + escapeHtml(entry.id) + '\', \'' + escapeHtml(entry.query) + '\')"' : '';

			return '<div class="history-entry' + clickable + '"' + onclick + '>' +
				'<div class="history-icon ' + iconClass + '">' + iconChar + '</div>' +
				'<div class="history-details">' +
					'<div class="history-summary">' + summary + '</div>' +
					'<div class="history-meta">' +
						'<span>' + timeStr + '</span>' +
						(hasResults ? '<span class="history-view-hint">Click to view results</span>' : '') +
					'</div>' +
				'</div>' +
				'<div class="history-result-count">' + entry.result_count + ' results</div>' +
				(isCustomSearch ? '' : '<button class="history-replay" onclick=\'replayFilter(' + JSON.stringify(JSON.stringify(entry.filters || {})) + ')\'>Replay</button>') +
			'</div>';
		}

		function formatHistoryTime(ts) {
			var d = new Date(ts);
			var now = new Date();
			var diffMs = now - d;
			var diffMins = Math.floor(diffMs / 60000);
			if (diffMins < 1) return 'Just now';
			if (diffMins < 60) return diffMins + 'm ago';
			var diffHrs = Math.floor(diffMins / 60);
			if (diffHrs < 24) return diffHrs + 'h ago';
			var diffDays = Math.floor(diffHrs / 24);
			if (diffDays < 7) return diffDays + 'd ago';
			return d.toLocaleDateString();
		}

		function replayFilter(filtersJson) {
			var f = JSON.parse(filtersJson);
			document.getElementById('filter-category').value = f.category || '';
			document.getElementById('filter-level').value = f.level || '';
			document.getElementById('filter-cert').value = f.certification || '';
			document.getElementById('filter-sort').value = f.sort || 'newest';
			document.getElementById('filter-remote').checked = !!f.remote_only;
			document.getElementById('filter-show-excluded').checked = !!f.show_excluded;
			document.getElementById('filter-salary-only').checked = !!f.salary_only;
			document.getElementById('filter-exclude-words').value = f.exclude_words || '';
			var searchInput = document.getElementById('search-input');
			if (searchInput) searchInput.value = f.search_text || '';
			if (viewingSearchResults) closeSearchResults();
			applyFilters();
			document.getElementById('history-panel').classList.add('hidden');
			showToast('success', 'Filters Applied', 'Replayed your saved search filters');
		}

		function clearHistory() {
			if (!confirm('Clear all your search history?')) return;
			fetch('/api/history/clear', {
				method: 'POST',
				credentials: 'same-origin'
			})
			.then(function(res) { return res.json(); })
			.then(function(data) {
				if (data.success) {
					document.getElementById('history-list').innerHTML = '<div class="history-empty">No search history yet. Your searches will appear here.</div>';
					showToast('success', 'History Cleared', 'Your search history has been cleared');
				}
			})
			.catch(function() {
				showToast('error', 'Error', 'Failed to clear history');
			});
		}

		function loadHistoryResults(entryId, query) {
			showToast('info', 'Loading', 'Loading results for "' + query + '"...');
			fetch('/api/history/results/' + entryId, { credentials: 'same-origin' })
				.then(function(res) {
					if (!res.ok) throw new Error('not found');
					return res.json();
				})
				.then(function(data) {
					var results = data.results || [];
					if (results.length === 0) {
						showToast('warning', 'No Results', 'No stored results found for this search');
						return;
					}
					document.getElementById('history-panel').classList.add('hidden');
					var statusMsg = document.getElementById('search-status-msg');
					statusMsg.style.display = 'block';
					statusMsg.textContent = 'Showing ' + results.length + ' saved results for "' + query + '" (from history)';
					showSearchResults(results, query);
					showToast('success', 'Loaded', 'Showing ' + results.length + ' results for "' + query + '"');
				})
				.catch(function() {
					showToast('error', 'Error', 'Failed to load results. They may have expired.');
				});
		}

		// --- Alerts & Schedules ---

		function toggleAlertsPanel() {
			var panel = document.getElementById('alerts-panel');
			panel.classList.toggle('hidden');
			if (!panel.classList.contains('hidden')) {
				loadAlertsConfig();
			}
		}

		function loadAlertsConfig() {
			fetch('/api/alerts/config', { credentials: 'same-origin' })
				.then(function(res) { return res.json(); })
				.then(function(data) {
					var tg = data.telegram || {};
					var status = document.getElementById('tg-status');
					if (tg.verified) {
						status.textContent = '✅ Verified & Active';
						status.style.color = 'var(--accent-primary)';
					} else if (tg.has_token) {
						status.textContent = '⚠️ Not verified — send a test message';
						status.style.color = 'var(--warning)';
					} else {
						status.textContent = '';
					}
					if (tg.has_token) {
						document.getElementById('tg-bot-token').placeholder = '••••••• (saved — enter new to change)';
					}
					if (tg.has_chatid) {
						document.getElementById('tg-chat-id').placeholder = '••••••• (saved — enter new to change)';
					}
					renderSchedules(data.schedules || []);
				})
				.catch(function() {});
		}

		function saveTelegramConfig() {
			var token = document.getElementById('tg-bot-token').value.trim();
			var chatId = document.getElementById('tg-chat-id').value.trim();
			if (!token && !chatId) {
				showToast('warning', 'Missing Info', 'Enter at least a bot token or chat ID');
				return;
			}
			var body = { enabled: true };
			if (token) body.bot_token = token;
			if (chatId) body.chat_id = chatId;
			fetch('/api/alerts/config', {
				method: 'POST', credentials: 'same-origin',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify(body)
			})
			.then(function(res) { return res.json(); })
			.then(function(data) {
				if (data.success) {
					showToast('success', 'Saved', 'Telegram config saved. Send a test message to verify.');
					document.getElementById('tg-bot-token').value = '';
					document.getElementById('tg-chat-id').value = '';
					loadAlertsConfig();
				} else {
					showToast('error', 'Error', data.error || 'Failed to save');
				}
			});
		}

		function testTelegram() {
			var status = document.getElementById('tg-status');
			status.textContent = 'Sending test...';
			status.style.color = 'var(--text-secondary)';
			fetch('/api/alerts/telegram/test', {
				method: 'POST', credentials: 'same-origin'
			})
			.then(function(res) { return res.json(); })
			.then(function(data) {
				if (data.success) {
					showToast('success', 'Test Sent', data.message);
					loadAlertsConfig();
				} else {
					showToast('error', 'Test Failed', data.error);
					status.textContent = '❌ ' + (data.error || 'Test failed');
					status.style.color = 'var(--error)';
				}
			});
		}

		function onSchedTypeChange() {
			var type = document.getElementById('sched-type').value;
			document.getElementById('sched-query').style.display = type === 'custom' ? '' : 'none';
		}

		function onSchedFreqChange() {
			var freq = document.getElementById('sched-frequency').value;
			document.getElementById('sched-custom-days').style.display = freq === 'custom' ? 'flex' : 'none';
		}

		function addSchedule() {
			var name = document.getElementById('sched-name').value.trim();
			var type = document.getElementById('sched-type').value;
			var query = document.getElementById('sched-query').value.trim();
			var freq = document.getElementById('sched-frequency').value;
			var timeParts = document.getElementById('sched-time').value.split(':');
			var notifyEmpty = document.getElementById('sched-notify-empty').checked;

			if (!name) { showToast('warning', 'Missing', 'Enter a schedule name'); return; }
			if (type === 'custom' && !query) { showToast('warning', 'Missing', 'Enter a search query'); return; }

			var days = [];
			if (freq === 'custom') {
				var cbs = document.querySelectorAll('.sched-day-cb:checked');
				cbs.forEach(function(cb) { days.push(parseInt(cb.value)); });
				if (days.length === 0) { showToast('warning', 'Missing', 'Select at least one day'); return; }
			}

			var body = {
				name: name,
				type: type,
				query: type === 'custom' ? query : '',
				schedule: freq,
				days: days,
				hour: parseInt(timeParts[0]) || 9,
				minute: parseInt(timeParts[1]) || 0,
				enabled: true,
				notify_empty: notifyEmpty
			};

			fetch('/api/alerts/schedules', {
				method: 'POST', credentials: 'same-origin',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify(body)
			})
			.then(function(res) { return res.json(); })
			.then(function(data) {
				if (data.success) {
					showToast('success', 'Added', 'Schedule created');
					document.getElementById('sched-name').value = '';
					document.getElementById('sched-query').value = '';
					renderSchedules(data.schedules || []);
				} else {
					showToast('error', 'Error', data.error || 'Failed to create');
				}
			});
		}

		function renderSchedules(schedules) {
			var container = document.getElementById('schedules-list');
			if (!schedules || schedules.length === 0) {
				container.innerHTML = '<p style="color:var(--text-secondary);font-size:0.85rem;font-style:italic">No schedules yet. Add one below.</p>';
				return;
			}
			var dayNames = ['Sun','Mon','Tue','Wed','Thu','Fri','Sat'];
			var html = '';
			schedules.forEach(function(s) {
				var timeStr = (s.hour < 10 ? '0' : '') + s.hour + ':' + (s.minute < 10 ? '0' : '') + s.minute;
				var freqStr = s.schedule;
				if (s.schedule === 'custom' && s.days) {
					freqStr = s.days.map(function(d) { return dayNames[d]; }).join(', ');
				} else if (s.schedule === 'weekly') {
					freqStr = 'Mondays';
				}
				var typeLabel = s.type === 'default' ? '🛡️ OffSec' : '🔍 ' + escapeHtml(s.query);
				var statusIcon = s.enabled ? '🟢' : '🔴';
				var lastInfo = '';
				if (s.last_run) {
					var ago = formatHistoryTime(s.last_run);
					lastInfo = '<span style="font-size:0.75rem;color:var(--text-secondary)">Last: ' + ago + (s.last_result ? ' — ' + escapeHtml(s.last_result) : '') + '</span>';
				}

				html += '<div style="display:flex;justify-content:space-between;align-items:center;padding:0.6rem 0.75rem;background:var(--bg-card);border:1px solid var(--border-color);border-radius:6px;margin-bottom:0.5rem">';
				html += '<div style="flex:1">';
				html += '<div style="font-weight:600;font-size:0.9rem;color:var(--text-primary)">' + statusIcon + ' ' + escapeHtml(s.name) + '</div>';
				html += '<div style="font-size:0.8rem;color:var(--text-secondary)">' + typeLabel + ' · ' + freqStr + ' at ' + timeStr + '</div>';
				if (lastInfo) html += '<div>' + lastInfo + '</div>';
				html += '</div>';
				html += '<div style="display:flex;gap:0.4rem">';
				html += '<button onclick="toggleSchedule(\'' + s.id + '\',' + !s.enabled + ')" class="btn-login" style="padding:0.2rem 0.5rem;font-size:0.75rem">' + (s.enabled ? 'Disable' : 'Enable') + '</button>';
				html += '<button onclick="deleteSchedule(\'' + s.id + '\')" class="btn-login" style="padding:0.2rem 0.5rem;font-size:0.75rem;border-color:var(--error);color:var(--error)">Delete</button>';
				html += '</div>';
				html += '</div>';
			});
			container.innerHTML = html;
		}

		function toggleSchedule(id, enabled) {
			fetch('/api/alerts/schedules', { credentials: 'same-origin' })
				.then(function(res) { return res.json(); })
				.then(function(schedules) {
					var sched = null;
					(schedules || []).forEach(function(s) { if (s.id === id) sched = s; });
					if (!sched) return;
					sched.enabled = enabled;
					return fetch('/api/alerts/schedules', {
						method: 'POST', credentials: 'same-origin',
						headers: { 'Content-Type': 'application/json' },
						body: JSON.stringify(sched)
					});
				})
				.then(function(res) { return res.json(); })
				.then(function(data) {
					if (data && data.success) {
						renderSchedules(data.schedules || []);
						showToast('success', 'Updated', 'Schedule ' + (enabled ? 'enabled' : 'disabled'));
					}
				});
		}

		function deleteSchedule(id) {
			if (!confirm('Delete this schedule?')) return;
			fetch('/api/alerts/schedules/delete', {
				method: 'POST', credentials: 'same-origin',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({ id: id })
			})
			.then(function(res) { return res.json(); })
			.then(function(data) {
				if (data.success) {
					renderSchedules(data.schedules || []);
					showToast('success', 'Deleted', 'Schedule removed');
				}
			});
		}

		checkAuth();

		setTimeout(function() {
			if (!searchPollingInterval && !viewingSearchResults) {
				location.reload();
			}
		}, 5 * 60 * 1000);
	</script>
</body>
</html>`

	// Replace placeholders
	html = strings.Replace(html, "{{LAST_UPDATED}}", lastUpdatedStr, 1)
	html = strings.Replace(html, "{{ALL_JOBS}}", jobsStr, 1)
	html = strings.Replace(html, "{{APP_CONFIG}}", configStr, 1)
	
	return html
}
