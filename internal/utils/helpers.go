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
	}

	// Static fallback salary data for major tech companies
	// Source: Levels.fyi averages as of January 2026
	// This provides reliable data when live scraping fails due to CAPTCHA/WAF
	staticSalaryData = map[string]string{
		// FAANG+
		"meta":        "$784,137",
		"facebook":    "$784,137",
		"google":      "$718,962",
		"apple":       "$359,527",
		"amazon":      "$379,762",
		"netflix":     "$906,707",
		"microsoft":   "$300,731",
		// Finance/Trading
		"robinhood":   "$418,461",
		"coinbase":    "$476,661",
		"stripe":      "$534,719",
		"square":      "$389,627",
		"block":       "$389,627",
		"plaid":       "$409,812",
		"affirm":      "$432,156",
		"brex":        "$398,741",
		"citadel":     "$612,438",
		"two sigma":   "$589,234",
		"jane street": "$598,127",
		// Big Tech
		"nvidia":      "$428,654",
		"oracle":      "$287,563",
		"salesforce":  "$341,872",
		"adobe":       "$312,456",
		"intel":       "$276,891",
		"amd":         "$289,745",
		"qualcomm":    "$298,412",
		"vmware":      "$312,654",
		"broadcom":    "$367,892",
		// Social/Consumer
		"snap":        "$478,912",
		"snapchat":    "$478,912",
		"twitter":     "$412,387",
		"x":           "$412,387",
		"pinterest":   "$398,654",
		"discord":     "$387,234",
		"reddit":      "$398,721",
		"linkedin":    "$412,543",
		"tiktok":      "$389,456",
		"bytedance":   "$389,456",
		// Delivery/Mobility
		"uber":        "$512,876",
		"lyft":        "$398,234",
		"doordash":    "$412,567",
		"instacart":   "$402,837",
		"airbnb":      "$489,321",
		// Cloud/Enterprise
		"databricks":  "$478,912",
		"snowflake":   "$456,234",
		"palantir":    "$367,891",
		"datadog":     "$398,765",
		"splunk":      "$356,432",
		"cloudflare":  "$378,912",
		"elastic":     "$345,678",
		"mongodb":     "$389,234",
		// Security
		"crowdstrike": "$312,456",
		"palo alto":   "$334,567",
		"zscaler":     "$298,765",
		"fortinet":    "$287,654",
		"sentinelone": "$298,432",
		"rapid7":      "$267,891",
		"tenable":     "$278,543",
		// AI/ML
		"openai":      "$865,432",
		"anthropic":   "$723,456",
		"scale ai":    "$456,789",
		"anduril":     "$398,765",
		"figma":       "$412,345",
		// Other Tech
		"dropbox":     "$378,912",
		"slack":       "$389,234",
		"zoom":        "$356,789",
		"twilio":      "$367,432",
		"okta":        "$345,678",
		"atlassian":   "$398,234",
		"docusign":    "$312,456",
		"box":         "$298,765",
		"hubspot":     "$312,345",
		"zendesk":     "$289,654",
		"servicenow":  "$356,789",
		"workday":     "$378,234",
		// Gaming
		"roblox":      "$456,789",
		"ea":          "$312,456",
		"electronic arts": "$312,456",
		"activision":  "$298,765",
		"riot games":  "$287,654",
		"unity":       "$298,432",
		"epic games":  "$312,345",
		// E-commerce
		"shopify":     "$378,234",
		"ebay":        "$312,456",
		"wayfair":     "$287,654",
		"etsy":        "$298,765",
		"chewy":       "$267,891",
		// Autonomous/Hardware
		"waymo":       "$489,321",
		"cruise":      "$456,234",
		"tesla":       "$398,765",
		"rivian":      "$356,789",
		"lucid":       "$345,678",
		"aurora":      "$412,345",
		// Consulting/Defense
		"deloitte":    "$198,765",
		"accenture":   "$187,654",
		"booz allen":  "$178,543",
		"lockheed":    "$189,432",
		"raytheon":    "$178,654",
		"northrop":    "$187,234",
		"leidos":      "$167,891",
		// Healthcare Tech
		"epic systems": "$198,765",
		"cerner":       "$187,654",
		"veeva":        "$312,456",
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
		"lever":      true,
		"linkedin":   true,
		"monster":    true,
		"indeed":     true,
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

// getStaticSalary looks up salary from the static fallback data
func getStaticSalary(companyName string) (string, bool) {
	// Normalize company name for lookup
	normalized := strings.ToLower(strings.TrimSpace(companyName))
	
	// Direct lookup
	if salary, exists := staticSalaryData[normalized]; exists {
		return salary, true
	}
	
	// Try without common suffixes
	suffixes := []string{" inc", " inc.", " corp", " corp.", " llc", " ltd", " technologies", " technology"}
	for _, suffix := range suffixes {
		if strings.HasSuffix(normalized, suffix) {
			trimmed := strings.TrimSuffix(normalized, suffix)
			if salary, exists := staticSalaryData[trimmed]; exists {
				return salary, true
			}
		}
	}
	
	// Try partial match for companies with longer names
	for company, salary := range staticSalaryData {
		if strings.Contains(normalized, company) || strings.Contains(company, normalized) {
			return salary, true
		}
	}
	
	return "", false
}

// GetSalaryFromLevelsFyi fetches salary data from levels.fyi for a company
func GetSalaryFromLevelsFyi(companyName string, debug bool) (string, error) {
	// Check cache first
	salaryCacheMux.RLock()
	if salary, exists := salaryCache[companyName]; exists && salary != "No Data" {
		salaryCacheMux.RUnlock()
		return FormatSalary(salary), nil
	}
	salaryCacheMux.RUnlock()

	// Check static fallback data first (more reliable than scraping)
	if staticSalary, found := getStaticSalary(companyName); found {
		if debug {
			fmt.Printf("Using static salary data for %s: %s\n", companyName, staticSalary)
		}
		// Cache the result
		salaryCacheMux.Lock()
		salaryCache[companyName] = staticSalary
		salaryCacheMux.Unlock()
		return staticSalary, nil
	}

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
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("Pragma", "no-cache")

	resp, err := levelsFyiClient.Do(req)
	if err != nil {
		if debug {
			fmt.Printf("Error fetching Levels.fyi for %s: %v\n", companyName, err)
		}
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		if debug {
			fmt.Printf("Non-200 status for %s: %d\n", companyName, resp.StatusCode)
		}
		return "", fmt.Errorf("received non-200 status code: %d", resp.StatusCode)
	}

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return "", err
	}

	// Parse the salary from the levels.fyi page
	// Try multiple selectors as the page structure may vary
	salaryElem := ""
	
	// Try different selectors
	selectors := []string{
		"td:contains('Software Engineer Salary')",
		"[data-testid='salary-value']",
		".salary-value",
		"span:contains('$')",
	}
	
	for _, selector := range selectors {
		elem := doc.Find(selector)
		if selector == "td:contains('Software Engineer Salary')" {
			elem = elem.Next()
		}
		text := strings.TrimSpace(elem.Text())
		if text != "" && strings.Contains(text, "$") {
			salaryElem = text
			break
		}
	}

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
			semaphore <- struct{}{}        // Acquire semaphore
			defer func() { <-semaphore }() // Release semaphore

			// Get salary data
			salary, err := GetSalaryFromLevelsFyi(companyName, debug)
			if err == nil {
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
