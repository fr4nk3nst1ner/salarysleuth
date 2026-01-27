package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// stripANSI removes ANSI escape codes from a string
func stripANSI(str string) string {
	ansiRegex := regexp.MustCompile(`\x1b\[[0-9;]*m`)
	return ansiRegex.ReplaceAllString(str, "")
}

// Job represents a curated job listing
type Job struct {
	ID          string    `json:"id"`
	Company     string    `json:"company"`
	Title       string    `json:"title"`
	Location    string    `json:"location"`
	URL         string    `json:"url"`
	SalaryRange string    `json:"salary_range,omitempty"`
	LevelSalary string    `json:"level_salary,omitempty"`
	Source      string    `json:"source"`
	FirstSeen   time.Time `json:"first_seen"`
	LastSeen    time.Time `json:"last_seen"`
}

// JobStore represents the stored job data
type JobStore struct {
	LastUpdated time.Time `json:"last_updated"`
	Jobs        []Job     `json:"jobs"`
}

// Config holds application configuration
type Config struct {
	TelegramBotToken string
	TelegramChatID   string
	DataDir          string
	WebPort          int
	RunScraper       bool
	Pages            int
	Description      string
}

var config Config

func main() {
	// Parse command line flags
	runScraper := flag.Bool("scrape", false, "Run the scraper to fetch new jobs")
	webOnly := flag.Bool("web", false, "Run only the web server")
	webPort := flag.Int("port", 8080, "Web server port")
	pages := flag.Int("pages", 20, "Number of pages to scrape")
	description := flag.String("description", "Offensive Security", "Job description to search for")
	flag.Parse()

	// Determine data directory
	dataDir := os.Getenv("DATA_DIR")
	if dataDir == "" {
		// Get the directory where this executable is located
		execPath, err := os.Executable()
		if err != nil {
			execPath, _ = os.Getwd()
		}
		baseDir := filepath.Dir(execPath)

		// If running with go run, use current working directory
		if strings.Contains(execPath, "go-build") {
			baseDir, _ = os.Getwd()
		}
		dataDir = filepath.Join(baseDir, "data")
	}

	config = Config{
		TelegramBotToken: os.Getenv("TELEGRAM_BOT_TOKEN"),
		TelegramChatID:   os.Getenv("TELEGRAM_CHAT_ID"),
		DataDir:          dataDir,
		WebPort:          *webPort,
		RunScraper:       *runScraper,
		Pages:            *pages,
		Description:      *description,
	}

	// Ensure data directory exists
	if err := os.MkdirAll(config.DataDir, 0755); err != nil {
		log.Fatalf("Failed to create data directory: %v", err)
	}

	if *webOnly {
		// Run only the web server
		log.Printf("Starting web server on port %d...\n", config.WebPort)
		startWebServer()
		return
	}

	if *runScraper {
		// Run the scraper multiple times to ensure we capture all jobs
		// Sometimes jobs can be missed due to pagination, rate limiting, or API variability
		const scrapeRuns = 3
		log.Printf("Running SalarySleuth scraper (%d passes for comprehensive coverage)...\n", scrapeRuns)
		
		allJobs := make(map[string]Job) // Use map to deduplicate by job ID
		
		for run := 1; run <= scrapeRuns; run++ {
			log.Printf("  Pass %d/%d...\n", run, scrapeRuns)
			
			jobs, err := runSalarySleuth(config.Description, config.Pages)
			if err != nil {
				log.Printf("  Warning: Pass %d failed: %v\n", run, err)
				continue // Continue to next run instead of failing completely
			}
			
			// Add jobs to map (automatically deduplicates)
			newCount := 0
			for _, job := range jobs {
				if _, exists := allJobs[job.ID]; !exists {
					allJobs[job.ID] = job
					newCount++
				} else {
					// Update salary data if new run has it and existing doesn't
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
			
			log.Printf("  Pass %d found %d jobs (%d new unique)\n", run, len(jobs), newCount)
			
			// Small delay between runs to avoid rate limiting
			if run < scrapeRuns {
				time.Sleep(5 * time.Second)
			}
		}
		
		// Convert map back to slice
		var jobs []Job
		for _, job := range allJobs {
			jobs = append(jobs, job)
		}

		log.Printf("Found %d total unique jobs from %d passes\n", len(jobs), scrapeRuns)

		// Filter for OSCP/offsec jobs
		filteredJobs := filterOffsecJobs(jobs)
		log.Printf("After filtering: %d OSCP/offsec jobs\n", len(filteredJobs))

		// Load existing jobs
		existingStore := loadJobStore()

		// Find new jobs and update store
		newJobs, updatedStore := processJobs(filteredJobs, existingStore)

		// Save updated store
		if err := saveJobStore(updatedStore); err != nil {
			log.Fatalf("Failed to save job store: %v", err)
		}

		// Send notifications for new jobs
		if len(newJobs) > 0 {
			log.Printf("Sending Telegram notifications for %d new jobs...\n", len(newJobs))
			sendTelegramNotifications(newJobs)
		} else {
			log.Println("No new jobs found")
		}

		log.Printf("Job tracking complete. %d active jobs in store.\n", len(updatedStore.Jobs))
	}

	// Start web server if requested or by default after scraping
	if !*runScraper && !*webOnly {
		fmt.Println("Usage:")
		fmt.Println("  -scrape    Run the scraper to fetch and process new jobs")
		fmt.Println("  -web       Run only the web server")
		fmt.Println("  -port N    Web server port (default: 8080)")
		fmt.Println("  -pages N   Number of pages to scrape (default: 20)")
		fmt.Println("  -description \"text\"  Job description to search for")
		fmt.Println("\nExamples:")
		fmt.Println("  go run . -scrape              # Run scraper and send notifications")
		fmt.Println("  go run . -web                 # Start web server only")
		fmt.Println("  go run . -scrape -web         # Scrape then start web server")
	}

	if *runScraper && *webOnly {
		log.Printf("Starting web server on port %d...\n", config.WebPort)
		startWebServer()
	}
}

func getEnvOrDefault(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}

// runSalarySleuth executes the salarysleuth command and parses the output
func runSalarySleuth(description string, pages int) ([]Job, error) {
	// Find the salarysleuth executable
	salarysleuthPath := findSalarySleuthExecutable()
	
	cmd := exec.Command(salarysleuthPath,
		"-nobanner",
		"-pages", fmt.Sprintf("%d", pages),
		"-description", description,
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("scraper error: %v\nOutput: %s", err, string(output))
	}

	return parseScraperOutput(string(output))
}

func findSalarySleuthExecutable() string {
	// Check for environment variable first (set by run-scraper.sh)
	if binPath := os.Getenv("SALARYSLEUTH_BIN"); binPath != "" && fileExists(binPath) {
		return binPath
	}
	
	// Check for Docker environment
	if _, err := os.Stat("/app/salarysleuth"); err == nil {
		return "/app/salarysleuth"
	}
	
	// Check in same directory as jobtracker
	execPath, err := os.Executable()
	if err == nil {
		execDir := filepath.Dir(execPath)
		if path := filepath.Join(execDir, "salarysleuth"); fileExists(path) {
			return path
		}
		// Check parent directory
		if path := filepath.Join(filepath.Dir(execDir), "salarysleuth"); fileExists(path) {
			return path
		}
	}
	
	// Try project root
	projectRoot := findProjectRoot()
	if path := filepath.Join(projectRoot, "salarysleuth"); fileExists(path) {
		return path
	}
	
	// Fall back to error - don't use "go" as it causes issues
	log.Fatal("ERROR: Could not find salarysleuth executable. Please build it first with: go build -o salarysleuth ./cmd/salarysleuth/main.go")
	return ""
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func findProjectRoot() string {
	// Check for Docker environment variable first
	if root := os.Getenv("SALARYSLEUTH_ROOT"); root != "" {
		return root
	}
	
	// Check common Docker paths
	dockerPaths := []string{
		"/app/salarysleuth",
		"/app",
	}
	for _, p := range dockerPaths {
		if _, err := os.Stat(filepath.Join(p, "cmd", "salarysleuth", "main.go")); err == nil {
			return p
		}
	}
	
	// Try to find project root by looking for the cmd/salarysleuth directory
	dir, _ := os.Getwd()
	
	// First check if we're in the jobtracker subdirectory
	if filepath.Base(dir) == "jobtracker" {
		parent := filepath.Dir(dir)
		if _, err := os.Stat(filepath.Join(parent, "cmd", "salarysleuth", "main.go")); err == nil {
			return parent
		}
	}
	
	// Check current directory
	if _, err := os.Stat(filepath.Join(dir, "cmd", "salarysleuth", "main.go")); err == nil {
		return dir
	}
	
	// Walk up looking for the main project
	for {
		if _, err := os.Stat(filepath.Join(dir, "cmd", "salarysleuth", "main.go")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	
	// Default to parent of current directory
	cwd, _ := os.Getwd()
	return filepath.Dir(cwd)
}

// parseScraperOutput parses the text output from salarysleuth
func parseScraperOutput(output string) ([]Job, error) {
	var jobs []Job
	lines := strings.Split(output, "\n")

	var currentJob Job
	inJob := false

	for _, line := range lines {
		line = strings.TrimSpace(line)

		if strings.HasPrefix(line, "Company: ") {
			if inJob && currentJob.Company != "" {
				currentJob.ID = generateJobID(currentJob)
				currentJob.FirstSeen = time.Now()
				currentJob.LastSeen = time.Now()
				jobs = append(jobs, currentJob)
			}
			currentJob = Job{}
			currentJob.Company = strings.TrimPrefix(line, "Company: ")
			inJob = true
		} else if strings.HasPrefix(line, "Title: ") {
			currentJob.Title = strings.TrimPrefix(line, "Title: ")
		} else if strings.HasPrefix(line, "Location: ") {
			currentJob.Location = strings.TrimPrefix(line, "Location: ")
		} else if strings.HasPrefix(line, "URL: ") {
			currentJob.URL = strings.TrimPrefix(line, "URL: ")
		} else if strings.HasPrefix(line, "Source: ") {
			currentJob.Source = strings.TrimPrefix(line, "Source: ")
	} else if strings.HasPrefix(line, "Salary Range: ") {
		salary := strings.TrimPrefix(line, "Salary Range: ")
		salary = stripANSI(salary) // Remove ANSI color codes
		if salary != "" && salary != "Not Available" {
			currentJob.SalaryRange = salary
		}
	} else if strings.HasPrefix(line, "Levels.fyi Average: ") {
		salary := strings.TrimPrefix(line, "Levels.fyi Average: ")
		salary = stripANSI(salary) // Remove ANSI color codes
		if salary != "" && salary != "No Data" {
			currentJob.LevelSalary = salary
		}
	} else if strings.HasPrefix(line, "Levels.fyi Median: ") {
		salary := strings.TrimPrefix(line, "Levels.fyi Median: ")
		salary = stripANSI(salary) // Remove ANSI color codes
		if salary != "" && salary != "No Data" && currentJob.LevelSalary == "" {
			currentJob.LevelSalary = salary
		}
	}
	}

	// Don't forget the last job
	if inJob && currentJob.Company != "" {
		currentJob.ID = generateJobID(currentJob)
		currentJob.FirstSeen = time.Now()
		currentJob.LastSeen = time.Now()
		jobs = append(jobs, currentJob)
	}

	return jobs, nil
}

func generateJobID(job Job) string {
	// Create a unique ID from company + title + URL
	base := strings.ToLower(job.Company + "|" + job.Title + "|" + job.URL)
	base = regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(base, "-")
	if len(base) > 100 {
		base = base[:100]
	}
	return base
}

// filterOffsecJobs filters jobs based on configuration rules
// Now uses config.yaml for filtering rules
func filterOffsecJobs(jobs []Job) []Job {
	cfg, err := LoadConfig()
	if err != nil {
		log.Printf("Warning: Could not load config, using defaults: %v", err)
		cfg = getDefaultConfig()
	}

	var filtered []Job

	for _, job := range jobs {
		titleLower := strings.ToLower(job.Title)

		// Check exclusion rules from config
		excluded := false
		
		for _, kw := range cfg.Filters.Exclude.DefensiveRoles {
			if strings.Contains(titleLower, strings.ToLower(kw)) {
				excluded = true
				break
			}
		}
		
		if !excluded {
			for _, kw := range cfg.Filters.Exclude.NonSecurity {
				if strings.Contains(titleLower, strings.ToLower(kw)) {
					excluded = true
					break
				}
			}
		}
		
		if !excluded {
			for _, kw := range cfg.Filters.Exclude.Compliance {
				if strings.Contains(titleLower, strings.ToLower(kw)) {
					excluded = true
					break
				}
			}
		}

		if excluded {
			continue
		}

		// Check if job matches any category from config
		matchesCategory := false
		for _, cat := range cfg.Filters.Categories {
			for _, kw := range cat.Keywords {
				if strings.Contains(titleLower, strings.ToLower(kw)) {
					matchesCategory = true
					break
				}
			}
			if matchesCategory {
				break
			}
		}

		// Must match at least one category or contain "security"
		if matchesCategory || strings.Contains(titleLower, "security") {
			filtered = append(filtered, job)
		}
	}

	return filtered
}

// processJobs compares new jobs with existing ones, returns new jobs and updated store
func processJobs(newJobs []Job, existingStore JobStore) ([]Job, JobStore) {
	now := time.Now()

	// Create a map of existing jobs by ID
	existingMap := make(map[string]*Job)
	for i := range existingStore.Jobs {
		existingMap[existingStore.Jobs[i].ID] = &existingStore.Jobs[i]
	}

	// Create a map of new jobs by ID
	newMap := make(map[string]bool)
	for _, job := range newJobs {
		newMap[job.ID] = true
	}

	var actuallyNewJobs []Job
	var updatedJobs []Job

	// Process new jobs
	for _, job := range newJobs {
		if existing, found := existingMap[job.ID]; found {
			// Job exists, update last seen
			existing.LastSeen = now
			
			// Update salary data if the new scrape has it and existing doesn't
			// This ensures salary data is captured even if it wasn't available initially
			if job.LevelSalary != "" && existing.LevelSalary == "" {
				existing.LevelSalary = job.LevelSalary
			}
			if job.SalaryRange != "" && existing.SalaryRange == "" {
				existing.SalaryRange = job.SalaryRange
			}
			
			updatedJobs = append(updatedJobs, *existing)
		} else {
			// Truly new job
			job.FirstSeen = now
			job.LastSeen = now
			updatedJobs = append(updatedJobs, job)
			actuallyNewJobs = append(actuallyNewJobs, job)
		}
	}

	// Jobs not in the new scrape are removed (not added to updatedJobs)
	// This implements the "remove from page if not in latest run" requirement

	return actuallyNewJobs, JobStore{
		LastUpdated: now,
		Jobs:        updatedJobs,
	}
}

func loadJobStore() JobStore {
	dataFile := filepath.Join(config.DataDir, "jobs.json")

	data, err := os.ReadFile(dataFile)
	if err != nil {
		// File doesn't exist yet
		return JobStore{Jobs: []Job{}}
	}

	var store JobStore
	if err := json.Unmarshal(data, &store); err != nil {
		log.Printf("Warning: Could not parse job store: %v", err)
		return JobStore{Jobs: []Job{}}
	}

	return store
}

func saveJobStore(store JobStore) error {
	dataFile := filepath.Join(config.DataDir, "jobs.json")

	data, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(dataFile, data, 0644)
}
