package utils

import (
	"fmt"
	"math/rand"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/fr4nk3nst1ner/salarysleuth/internal/models"
)

const (
	maxRetries = 5
	minDelay   = 200 * time.Millisecond
	maxDelay   = 500 * time.Second
	maxWorkers = 10
)

var userAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36"

// Cache for Levels.fyi salary data
var (
	salaryCache     = make(map[string]string)
	salaryCacheMux  sync.RWMutex
	salaryFetchTime time.Time
	salaryCacheTTL  = 24 * time.Hour // Cache for 24 hours

	// The top companies cache variables have been moved to top_paying_companies.go
	
	levelsFyiClient = &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:        100,
			MaxIdleConnsPerHost: 10,
			IdleConnTimeout:     90 * time.Second,
			DisableCompression:  false,
		},
	}
)

// AddRandomQueryParams adds random query parameters to a URL to avoid caching
func AddRandomQueryParams(baseURL string) string {
	u, err := url.Parse(baseURL)
	if err != nil {
		return baseURL
	}

	q := u.Query()
	q.Set("r", RandomString(8))
	q.Set("t", time.Now().Format("20060102150405"))
	u.RawQuery = q.Encode()

	return u.String()
}

// RandomString generates a random string of specified length
func RandomString(n int) string {
	const letters = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
	b := make([]byte, n)
	for i := range b {
		b[i] = letters[rand.Intn(len(letters))]
	}
	return string(b)
}

// ExtractNumericValue extracts numeric value from a salary string
func ExtractNumericValue(salaryStr string) int {
	// Remove any currency symbols, commas, and spaces
	salaryStr = strings.TrimSpace(salaryStr)
	salaryStr = strings.ReplaceAll(salaryStr, "$", "")
	salaryStr = strings.ReplaceAll(salaryStr, ",", "")
	
	// Handle "K" suffix (e.g., "100K" -> 100000)
	if strings.Contains(strings.ToUpper(salaryStr), "K") {
		salaryStr = strings.ToUpper(salaryStr)
		salaryStr = strings.ReplaceAll(salaryStr, "K", "")
		if val, err := strconv.Atoi(salaryStr); err == nil {
			return val * 1000
		}
	}
	
	// Try to parse the numeric value
	if val, err := strconv.Atoi(salaryStr); err == nil {
		return val
	}
	
	return 0
}

// FormatSalary formats a numeric salary as a string with proper formatting
func FormatSalary(salary string) string {
	// If salary is empty or "Not Available", return as is
	if salary == "" || salary == "Not Available" || salary == "No Data" {
		return salary
	}

	// Extract numeric value
	value := ExtractNumericValue(salary)
	if value == 0 {
		return salary // Return original if parsing fails
	}

	// Format with comma separators and dollar sign
	return fmt.Sprintf("$%s", formatNumberWithCommas(value))
}

// formatNumberWithCommas adds commas to a number for better readability
func formatNumberWithCommas(n int) string {
	// Convert number to string
	numStr := strconv.Itoa(n)
	
	// Add commas every 3 digits from the right
	for i := len(numStr) - 3; i > 0; i -= 3 {
		numStr = numStr[:i] + "," + numStr[i:]
	}
	
	return numStr
}

// IsValidSource checks if the source is supported
func IsValidSource(source string) bool {
	validSources := map[string]bool{
		"greenhouse": true,
		"lever":     true,
		"linkedin":  true,
		"monster":   true,
		"indeed":    true,
	}
	return validSources[strings.ToLower(source)]
}

// NormalizeGreenhouseURL normalizes Greenhouse job URLs
func NormalizeGreenhouseURL(jobURL string) string {
	if strings.Contains(jobURL, "boards.greenhouse.io") {
		return jobURL
	}
	
	parts := strings.Split(jobURL, "/")
	for i, part := range parts {
		if part == "jobs" && i+1 < len(parts) {
			jobID := parts[i+1]
			company := parts[2]
			return fmt.Sprintf("https://boards.greenhouse.io/embed/job_app?for=%s&token=%s", company, jobID)
		}
	}
	return jobURL
}

// IsValidJob checks if a job matches the search criteria
func IsValidJob(title, location, titleKeyword string, remoteOnly, internshipsOnly, topPayOnly bool, company string) bool {
	title = strings.ToLower(title)
	location = strings.ToLower(location)
	titleKeyword = strings.ToLower(titleKeyword)

	if titleKeyword != "" && !strings.Contains(title, titleKeyword) {
		return false
	}

	if remoteOnly {
		// Check for various remote indicators in the location
		isRemote := strings.Contains(location, "remote") ||
			strings.Contains(location, "anywhere") ||
			strings.Contains(location, "work from home") ||
			strings.Contains(location, "wfh") ||
			strings.Contains(title, "remote") ||
			// LinkedIn often lists nationwide remote jobs as "United States"
			(strings.Contains(location, "united states") && !strings.Contains(location, ",")) ||
			// Check for remote indicators in the title
			strings.Contains(title, "work from home") ||
			strings.Contains(title, "wfh")
		
		if !isRemote {
			return false
		}
	}

	if internshipsOnly && !strings.Contains(title, "intern") && !strings.Contains(title, "internship") {
		return false
	}

	if topPayOnly && !IsTopPayingCompany(company, false) {
		return false
	}

	return true
}

// FindSalaryInText attempts to find salary information in text using common patterns
func FindSalaryInText(text string) string {
	patterns := []string{
		// Ranges with K suffix
		`\$\d{2,3}K\s*-\s*\$\d{2,3}K`,
		`\$\d{2,3},\d{3}\s*-\s*\$\d{2,3},\d{3}`,
		// Single values with K suffix
		`\$\d{2,3}K`,
		`\$\d{2,3},\d{3}`,
		// Hourly rates
		`\$\d{2,3}(?:\.\d{2})?\s*(?:per hour|\/hr|\/hour)`,
		// Annual salary indicators
		`\$\d{2,3}(?:,\d{3})?\s*(?:per year|\/year|annual|annually)`,
	}

	text = strings.ToLower(text)
	for _, pattern := range patterns {
		re := regexp.MustCompile(`(?i)` + pattern)
		if match := re.FindString(text); match != "" {
			return match
		}
	}
	return ""
}

// GetSalaryFromLevelsFyi fetches salary data from levels.fyi for a company
func GetSalaryFromLevelsFyi(companyName string, debug bool) (string, error) {
	// Check cache first
	salaryCacheMux.RLock()
	if salary, exists := salaryCache[companyName]; exists {
		salaryCacheMux.RUnlock()
		return FormatSalary(salary), nil
	}
	salaryCacheMux.RUnlock()

	// Clean and format company name for URL
	cleanName := strings.ToLower(strings.ReplaceAll(companyName, " ", "-"))
	
	// Special case for Meta - use Facebook instead for levels.fyi
	if strings.ToLower(companyName) == "meta" {
		cleanName = "facebook"
		if debug {
			fmt.Printf("Company is Meta, using Facebook for levels.fyi lookup\n")
		}
	}
	
	url := fmt.Sprintf("https://www.levels.fyi/companies/%s/salaries/", cleanName)

	if debug {
		fmt.Printf("Fetching Levels.fyi data from: %s\n", url)
	}

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", err
	}

	// Use a more browser-like User-Agent
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Accept-Encoding", "gzip, deflate, br")
	req.Header.Set("Connection", "keep-alive")

	resp, err := levelsFyiClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("received non-200 status code: %d", resp.StatusCode)
	}

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return "", err
	}

	// Parse the salary from the levels.fyi page
	salaryElem := doc.Find("td:contains('Software Engineer Salary')").Next().Text()
	if salaryElem == "" {
		salaryElem = "No Data"
	} else {
		salaryElem = FormatSalary(salaryElem)
	}

	if debug {
		fmt.Printf("Found Levels.fyi salary for %s: %s\n", companyName, salaryElem)
	}

	// Cache the result
	salaryCacheMux.Lock()
	salaryCache[companyName] = salaryElem
	salaryCacheMux.Unlock()

	return salaryElem, nil
}

// ProcessWithLevelsFyi enriches job listings with Levels.fyi salary data
func ProcessWithLevelsFyi(jobs []models.SalaryInfo, debug bool) {
	// Create a map to track unique companies to avoid duplicate requests
	uniqueCompanies := make(map[string]struct{})
	for _, job := range jobs {
		uniqueCompanies[job.Company] = struct{}{}
	}

	// Create channels for concurrent processing
	type salaryResult struct {
		company string
		salary  string
	}
	resultsChan := make(chan salaryResult, len(uniqueCompanies))
	semaphore := make(chan struct{}, maxWorkers)
	var wg sync.WaitGroup

	// Process unique companies concurrently
	for company := range uniqueCompanies {
		wg.Add(1)
		go func(companyName string) {
			defer wg.Done()
			semaphore <- struct{}{} // Acquire semaphore
			defer func() { <-semaphore }() // Release semaphore

			// Get salary data
			salary, err := GetSalaryFromLevelsFyi(companyName, debug)
			if err == nil && salary != "No Data" {
				resultsChan <- salaryResult{company: companyName, salary: salary}
			}

			// Add small random delay to avoid rate limiting
			time.Sleep(time.Duration(rand.Int63n(500)) * time.Millisecond)
		}(company)
	}

	// Close results channel when all goroutines complete
	go func() {
		wg.Wait()
		close(resultsChan)
	}()

	// Build salary map from results
	salaryMap := make(map[string]string)
	for result := range resultsChan {
		salaryMap[result.company] = result.salary
	}

	// Update job listings with salary data
	for i := range jobs {
		if salary, exists := salaryMap[jobs[i].Company]; exists {
			jobs[i].LevelSalary = salary
		}
	}
}

// The FetchTopPayingCompanies and IsTopPayingCompany functions have been moved to top_paying_companies.go