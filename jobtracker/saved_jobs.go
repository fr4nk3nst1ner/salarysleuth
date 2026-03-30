package main

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type SavedJob struct {
	JobID    string    `json:"job_id"`
	Company  string    `json:"company"`
	Title    string    `json:"title"`
	Location string    `json:"location"`
	URL      string    `json:"url"`
	Source   string    `json:"source"`
	Salary   string    `json:"salary,omitempty"`
	SavedAt  time.Time `json:"saved_at"`
	Note     string    `json:"note,omitempty"`
}

type UserSavedJobs struct {
	Username string     `json:"username"`
	Jobs     []SavedJob `json:"jobs"`
}

var savedJobsMu sync.RWMutex

func savedJobsDir() string {
	return filepath.Join(config.DataDir, "saved_jobs")
}

func userSavedJobsFile(username string) string {
	return filepath.Join(savedJobsDir(), username+".json")
}

func ensureSavedJobsDir() {
	os.MkdirAll(savedJobsDir(), 0700)
}

func loadUserSavedJobs(username string) *UserSavedJobs {
	savedJobsMu.RLock()
	defer savedJobsMu.RUnlock()
	return loadUserSavedJobsUnsafe(username)
}

func loadUserSavedJobsUnsafe(username string) *UserSavedJobs {
	data, err := os.ReadFile(userSavedJobsFile(username))
	if err != nil {
		return &UserSavedJobs{Username: username, Jobs: []SavedJob{}}
	}
	var saved UserSavedJobs
	if err := json.Unmarshal(data, &saved); err != nil {
		return &UserSavedJobs{Username: username, Jobs: []SavedJob{}}
	}
	if saved.Jobs == nil {
		saved.Jobs = []SavedJob{}
	}
	return &saved
}

func saveUserSavedJobsUnsafe(data *UserSavedJobs) error {
	ensureSavedJobsDir()
	out, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(userSavedJobsFile(data.Username), out, 0600)
}

func addSavedJob(username string, job SavedJob) (bool, error) {
	savedJobsMu.Lock()
	defer savedJobsMu.Unlock()

	store := loadUserSavedJobsUnsafe(username)
	for _, j := range store.Jobs {
		if j.JobID == job.JobID {
			return false, nil // already saved
		}
	}
	store.Jobs = append(store.Jobs, job)
	if err := saveUserSavedJobsUnsafe(store); err != nil {
		return false, err
	}
	return true, nil
}

func removeSavedJob(username, jobID string) bool {
	savedJobsMu.Lock()
	defer savedJobsMu.Unlock()

	store := loadUserSavedJobsUnsafe(username)
	found := false
	filtered := make([]SavedJob, 0, len(store.Jobs))
	for _, j := range store.Jobs {
		if j.JobID == jobID {
			found = true
			continue
		}
		filtered = append(filtered, j)
	}
	if !found {
		return false
	}
	store.Jobs = filtered
	if err := saveUserSavedJobsUnsafe(store); err != nil {
		log.Printf("Failed to save jobs for %s: %v", username, err)
		return false
	}
	return true
}

func getUserSavedJobIDs(username string) map[string]bool {
	store := loadUserSavedJobs(username)
	ids := make(map[string]bool, len(store.Jobs))
	for _, j := range store.Jobs {
		ids[j.JobID] = true
	}
	return ids
}

func getUserSavedJobsList(username string) []SavedJob {
	store := loadUserSavedJobs(username)
	// Return newest saved first
	jobs := make([]SavedJob, len(store.Jobs))
	copy(jobs, store.Jobs)
	for i, j := 0, len(jobs)-1; i < j; i, j = i+1, j-1 {
		jobs[i], jobs[j] = jobs[j], jobs[i]
	}
	return jobs
}
