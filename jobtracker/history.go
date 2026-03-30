package main

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const historyRetentionDays = 30

type SearchFilters struct {
	Category     string `json:"category,omitempty"`
	Level        string `json:"level,omitempty"`
	Cert         string `json:"certification,omitempty"`
	Sort         string `json:"sort,omitempty"`
	RemoteOnly   bool   `json:"remote_only,omitempty"`
	ShowExcluded bool   `json:"show_excluded,omitempty"`
	SalaryOnly   bool   `json:"salary_only,omitempty"`
	SearchText   string `json:"search_text,omitempty"`
}

type SearchHistoryEntry struct {
	ID          string        `json:"id"`
	Timestamp   time.Time     `json:"timestamp"`
	Type        string        `json:"type"` // "filter" or "custom_search"
	Query       string        `json:"query,omitempty"`
	Filters     SearchFilters `json:"filters,omitempty"`
	ResultCount int           `json:"result_count"`
	ResultsFile string        `json:"results_file,omitempty"`
}

type UserHistory struct {
	Username string               `json:"username"`
	Entries  []SearchHistoryEntry `json:"entries"`
}

var historyMu sync.RWMutex

func historyDir() string {
	return filepath.Join(config.DataDir, "history")
}

func userHistoryFile(username string) string {
	return filepath.Join(historyDir(), username+".json")
}

func searchResultsDir() string {
	return filepath.Join(config.DataDir, "search_results")
}

func ensureHistoryDir() {
	os.MkdirAll(historyDir(), 0700)
	os.MkdirAll(searchResultsDir(), 0700)
}

func saveSearchResults(entryID string, results interface{}) string {
	filename := entryID + ".json"
	fp := filepath.Join(searchResultsDir(), filename)
	data, err := json.Marshal(results)
	if err != nil {
		log.Printf("Failed to marshal search results: %v", err)
		return ""
	}
	if err := os.WriteFile(fp, data, 0600); err != nil {
		log.Printf("Failed to save search results: %v", err)
		return ""
	}
	return filename
}

func loadSearchResults(filename string) (json.RawMessage, error) {
	fp := filepath.Join(searchResultsDir(), filename)
	data, err := os.ReadFile(fp)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(data), nil
}

func deleteSearchResults(filename string) {
	if filename == "" {
		return
	}
	fp := filepath.Join(searchResultsDir(), filename)
	os.Remove(fp)
}

func loadUserHistory(username string) *UserHistory {
	historyMu.RLock()
	defer historyMu.RUnlock()
	return loadUserHistoryUnsafe(username)
}

func loadUserHistoryUnsafe(username string) *UserHistory {
	data, err := os.ReadFile(userHistoryFile(username))
	if err != nil {
		return &UserHistory{Username: username, Entries: []SearchHistoryEntry{}}
	}
	var hist UserHistory
	if err := json.Unmarshal(data, &hist); err != nil {
		return &UserHistory{Username: username, Entries: []SearchHistoryEntry{}}
	}
	if hist.Entries == nil {
		hist.Entries = []SearchHistoryEntry{}
	}
	return &hist
}

func saveUserHistoryUnsafe(hist *UserHistory) error {
	ensureHistoryDir()
	data, err := json.MarshalIndent(hist, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(userHistoryFile(hist.Username), data, 0600)
}

func addHistoryEntry(username string, entry SearchHistoryEntry) {
	historyMu.Lock()
	defer historyMu.Unlock()

	hist := loadUserHistoryUnsafe(username)
	hist.Entries = append(hist.Entries, entry)

	cutoff := time.Now().AddDate(0, 0, -historyRetentionDays)
	pruned := make([]SearchHistoryEntry, 0, len(hist.Entries))
	for _, e := range hist.Entries {
		if e.Timestamp.After(cutoff) {
			pruned = append(pruned, e)
		} else {
			deleteSearchResults(e.ResultsFile)
		}
	}
	hist.Entries = pruned

	if err := saveUserHistoryUnsafe(hist); err != nil {
		log.Printf("Failed to save history for %s: %v", username, err)
	}
}

func getUserHistory(username string) []SearchHistoryEntry {
	hist := loadUserHistory(username)

	cutoff := time.Now().AddDate(0, 0, -historyRetentionDays)
	valid := make([]SearchHistoryEntry, 0, len(hist.Entries))
	for _, e := range hist.Entries {
		if e.Timestamp.After(cutoff) {
			valid = append(valid, e)
		}
	}

	// Return newest first
	for i, j := 0, len(valid)-1; i < j; i, j = i+1, j-1 {
		valid[i], valid[j] = valid[j], valid[i]
	}
	return valid
}

func clearUserHistory(username string) error {
	historyMu.Lock()
	defer historyMu.Unlock()
	existing := loadUserHistoryUnsafe(username)
	for _, e := range existing.Entries {
		deleteSearchResults(e.ResultsFile)
	}
	hist := &UserHistory{Username: username, Entries: []SearchHistoryEntry{}}
	return saveUserHistoryUnsafe(hist)
}

// getAllUsersHistory returns history for every user (admin-only)
func getAllUsersHistory() map[string][]SearchHistoryEntry {
	historyMu.RLock()
	defer historyMu.RUnlock()

	result := make(map[string][]SearchHistoryEntry)
	entries, err := os.ReadDir(historyDir())
	if err != nil {
		return result
	}

	cutoff := time.Now().AddDate(0, 0, -historyRetentionDays)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if len(name) < 6 || name[len(name)-5:] != ".json" {
			continue
		}
		username := name[:len(name)-5]
		hist := loadUserHistoryUnsafe(username)
		valid := make([]SearchHistoryEntry, 0, len(hist.Entries))
		for _, e := range hist.Entries {
			if e.Timestamp.After(cutoff) {
				valid = append(valid, e)
			}
		}
		// Newest first
		for i, j := 0, len(valid)-1; i < j; i, j = i+1, j-1 {
			valid[i], valid[j] = valid[j], valid[i]
		}
		if len(valid) > 0 {
			result[username] = valid
		}
	}
	return result
}

// runHistoryCleanup periodically prunes old entries
func runHistoryCleanup() {
	go func() {
		for {
			time.Sleep(6 * time.Hour)
			pruneAllHistory()
		}
	}()
}

func pruneAllHistory() {
	historyMu.Lock()
	defer historyMu.Unlock()

	entries, err := os.ReadDir(historyDir())
	if err != nil {
		return
	}

	cutoff := time.Now().AddDate(0, 0, -historyRetentionDays)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if len(name) < 6 || name[len(name)-5:] != ".json" {
			continue
		}
		username := name[:len(name)-5]
		hist := loadUserHistoryUnsafe(username)
		pruned := make([]SearchHistoryEntry, 0, len(hist.Entries))
		for _, e := range hist.Entries {
			if e.Timestamp.After(cutoff) {
				pruned = append(pruned, e)
			} else {
				deleteSearchResults(e.ResultsFile)
			}
		}
		hist.Entries = pruned
		saveUserHistoryUnsafe(hist)
	}
}
