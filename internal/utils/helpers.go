package utils

import (
	"fmt"
	"math/rand"
	"net/url"
	"regexp"
	"strings"
	"time"
	"net/http"
	"sync"
	"strconv"

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

// Cache for Levels.fyi salary data to avoid repeated requests
var (
	salaryCache     = make(map[string]string)
	salaryCacheMux  sync.RWMutex
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

// Cache for top paying companies to avoid repeated fetches
var (
	topCompaniesCache     = make(map[string]bool)
	topCompaniesCacheMux sync.RWMutex
	lastFetchTime        time.Time
	cacheDuration        = 24 * time.Hour // Refresh cache every 24 hours
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

	if remoteOnly && !strings.Contains(location, "remote") {
		return false
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

// FetchTopPayingCompanies fetches the list of top paying companies from levels.fyi
func FetchTopPayingCompanies(debug bool) error {
	topCompaniesCacheMux.Lock()
	defer topCompaniesCacheMux.Unlock()

	// Check if cache is still valid
	if time.Since(lastFetchTime) < cacheDuration && len(topCompaniesCache) > 0 {
		if debug {
			fmt.Printf("\n[DEBUG] Using cached top companies list (%d companies)\n", len(topCompaniesCache))
			fmt.Println("[DEBUG] Cached companies:")
			for company := range topCompaniesCache {
				fmt.Printf("[DEBUG] - %s\n", company)
			}
		}
		return nil
	}

	if debug {
		fmt.Printf("\n[DEBUG] Cache expired or empty, fetching fresh top companies data\n")
	}

	// List of known top-paying companies and their profile URLs
	topCompanyProfiles := []struct {
		Name string
		URL  string
	}{
		{"Google", "https://www.levels.fyi/companies/google/salaries"},
		{"Meta", "https://www.levels.fyi/companies/meta/salaries"},
		{"Apple", "https://www.levels.fyi/companies/apple/salaries"},
		{"Microsoft", "https://www.levels.fyi/companies/microsoft/salaries"},
		{"Amazon", "https://www.levels.fyi/companies/amazon/salaries"},
		{"Netflix", "https://www.levels.fyi/companies/netflix/salaries"},
		{"Uber", "https://www.levels.fyi/companies/uber/salaries"},
		{"Lyft", "https://www.levels.fyi/companies/lyft/salaries"},
		{"Airbnb", "https://www.levels.fyi/companies/airbnb/salaries"},
		{"Stripe", "https://www.levels.fyi/companies/stripe/salaries"},
		{"Coinbase", "https://www.levels.fyi/companies/coinbase/salaries"},
		{"Robinhood", "https://www.levels.fyi/companies/robinhood/salaries"},
		{"Snap", "https://www.levels.fyi/companies/snap/salaries"},
		{"Twitter", "https://www.levels.fyi/companies/twitter/salaries"},
		{"LinkedIn", "https://www.levels.fyi/companies/linkedin/salaries"},
		{"Square", "https://www.levels.fyi/companies/square/salaries"},
		{"Pinterest", "https://www.levels.fyi/companies/pinterest/salaries"},
		{"Dropbox", "https://www.levels.fyi/companies/dropbox/salaries"},
		{"Salesforce", "https://www.levels.fyi/companies/salesforce/salaries"},
		{"Adobe", "https://www.levels.fyi/companies/adobe/salaries"},
		{"Oracle", "https://www.levels.fyi/companies/oracle/salaries"},
		{"Intel", "https://www.levels.fyi/companies/intel/salaries"},
		{"Nvidia", "https://www.levels.fyi/companies/nvidia/salaries"},
		{"AMD", "https://www.levels.fyi/companies/amd/salaries"},
		{"Palantir", "https://www.levels.fyi/companies/palantir/salaries"},
		{"Databricks", "https://www.levels.fyi/companies/databricks/salaries"},
		{"Snowflake", "https://www.levels.fyi/companies/snowflake/salaries"},
		{"ByteDance", "https://www.levels.fyi/companies/bytedance/salaries"},
		{"Instacart", "https://www.levels.fyi/companies/instacart/salaries"},
		{"DoorDash", "https://www.levels.fyi/companies/doordash/salaries"},
	}

	newCache := make(map[string]bool)
	totalCompanies := 0

	for _, company := range topCompanyProfiles {
		if debug {
			fmt.Printf("\n[DEBUG] Processing company: %s\n", company.Name)
		}

		// Normalize company name
		companyName := strings.ToLower(company.Name)
		companyName = strings.ReplaceAll(companyName, " ", "")
		companyName = strings.ReplaceAll(companyName, ".", "")
		companyName = strings.ReplaceAll(companyName, ",", "")
		companyName = strings.ReplaceAll(companyName, "inc", "")
		companyName = strings.ReplaceAll(companyName, "corp", "")
		companyName = strings.ReplaceAll(companyName, "technologies", "")
		companyName = strings.ReplaceAll(companyName, "technology", "")

		if !newCache[companyName] {
			newCache[companyName] = true
			totalCompanies++
			if debug {
				fmt.Printf("[DEBUG] Added %s to top companies list\n", companyName)
			}
		}
	}

	// Update cache if we found any companies
	if len(newCache) > 0 {
		topCompaniesCache = newCache
		lastFetchTime = time.Now()
		if debug {
			fmt.Printf("\n[DEBUG] Successfully updated top companies cache with %d total companies\n", totalCompanies)
			fmt.Println("[DEBUG] Final list of all companies:")
			for company := range topCompaniesCache {
				fmt.Printf("[DEBUG] - %s\n", company)
			}
		}
	} else {
		if debug {
			fmt.Printf("\n[DEBUG] No companies found, using default list\n")
		}
		// If no companies found, use a minimal default list
		topCompaniesCache = map[string]bool{
			"netflix": true,
			"google": true,
			"meta": true,
			"apple": true,
			"microsoft": true,
			"amazon": true,
		}
		if debug {
			fmt.Printf("\n[DEBUG] Using default list:\n")
			for company := range topCompaniesCache {
				fmt.Printf("[DEBUG] - %s\n", company)
			}
		}
	}

	return nil
}

// IsTopPayingCompany checks if a company is in the top paying companies list from levels.fyi
func IsTopPayingCompany(company string, debug bool) bool {
	// Ensure we have the latest top companies data
	if err := FetchTopPayingCompanies(debug); err != nil {
		// If fetch fails, fall back to existing cache
		if len(topCompaniesCache) == 0 {
			// If no cache exists, use a minimal default list
			topCompaniesCacheMux.Lock()
			topCompaniesCache = map[string]bool{
				"google":    true,
				"meta":      true,
				"amazon":    true,
				"microsoft": true,
				"apple":     true,
				"netflix":   true,
			}
			topCompaniesCacheMux.Unlock()
		}
	}

	// Normalize company name
	originalCompany := company
	company = strings.ToLower(strings.TrimSpace(company))
	company = strings.ReplaceAll(company, " ", "")
	company = strings.ReplaceAll(company, ".", "")
	company = strings.ReplaceAll(company, ",", "")
	company = strings.ReplaceAll(company, "inc", "")
	company = strings.ReplaceAll(company, "corp", "")
	company = strings.ReplaceAll(company, "technologies", "")
	company = strings.ReplaceAll(company, "technology", "")

	// Handle special cases and common variations
	companyVariations := map[string]string{
		// FAANG/MANGA Companies
		"fb": "meta",
		"facebook": "meta",
		"alphabet": "google",
		"block": "square",
		"x": "twitter",
		"xcom": "twitter",
		"metaplatforms": "meta",
		"twitterinc": "twitter",
		"blockformerlysquare": "square",
		"metaplatformsinc": "meta",
		"alphabetinc": "google",
		"microsoftcorporation": "microsoft",
		"appletechnology": "apple",
		"aws": "amazon",
		"googlecloud": "google",
		"microsoftazure": "microsoft",
		"awscloud": "amazon",
		"metaplatform": "meta",
		"facebookmeta": "meta",
		"tiktokbytedance": "bytedance",
		"snapchatinc": "snapchat",
		"goldmansachsgroup": "goldman sachs",
		"goldmansachs": "goldman sachs",
		"jpmorganchase": "jpmorgan",
		"morganstanleygroup": "morgan stanley",
		"bankofamericacorp": "bank of america",
		"bofagroup": "bank of america",
		"citigroup": "citi",
		"wellsfargobank": "wells fargo",
		"capitalonefinancial": "capital one",
		"amazonwebservices": "amazon",
		"amazoncom": "amazon",
		"amazoninc": "amazon",
		"amazontech": "amazon",
		"amazontechnology": "amazon",
		"amazonwebservicesinc": "amazon",
		"awsinc": "amazon",
		"microsoftinc": "microsoft",
		"microsofttech": "microsoft",
		"microsofttechnology": "microsoft",
		"microsoftcorp": "microsoft",
		"microsoftazurecloud": "microsoft",
		"googleinc": "google",
		"googletech": "google",
		"googletechnology": "google",
		"googlellc": "google",
		"googleai": "google",
		"appleinc": "apple",
		"appletech": "apple",
		"appletechnologies": "apple",
		"applecorp": "apple",
		"applecomputer": "apple",
		"metainc": "meta",
		"metatech": "meta",
		"metatechnology": "meta",
		"metacorp": "meta",
		"facebookinc": "meta",
		"facebooktech": "meta",
		"netflixinc": "netflix",
		"netflixtech": "netflix",
		"netflixtechnology": "netflix",
		"netflixcorp": "netflix",
		"nvidiacorp": "nvidia",
		"nvidiainc": "nvidia",
		"nvidiatech": "nvidia",
		"nvidiatechnology": "nvidia",
		"amdcorp": "amd",
		"amdinc": "amd",
		"amdtech": "amd",
		"amdtechnology": "amd",
		"intelcorp": "intel",
		"intelinc": "intel",
		"inteltech": "intel",
		"inteltechnology": "intel",
		"salesforceinc": "salesforce",
		"salesforcecom": "salesforce",
		"salesforcetech": "salesforce",
		"salesforcetechnology": "salesforce",
		"vmwareinc": "vmware",
		"vmwaretech": "vmware",
		"vmwaretechnology": "vmware",
		"ibmcorp": "ibm",
		"ibminc": "ibm",
		"ibmtech": "ibm",
		"ibmtechnology": "ibm",
		"ciscocorp": "cisco",
		"ciscoinc": "cisco",
		"ciscotech": "cisco",
		"ciscotechnology": "cisco",
		"qualcomminc": "qualcomm",
		"qualcommtech": "qualcomm",
		"qualcommtechnology": "qualcomm",
		"intuitinc": "intuit",
		"intuittech": "intuit",
		"intuittechnology": "intuit",
		"workdayinc": "workday",
		"workdaytech": "workday",
		"workdaytechnology": "workday",
		"servicenowcorp": "servicenow",
		"servicenowtech": "servicenow",
		"autodesktech": "autodesk",
		"autodeskinc": "autodesk",
		"autodesktechnology": "autodesk",
		"hpinc": "hp",
		"hptech": "hp",
		"hptechnology": "hp",
		"hewlettpackard": "hp",
		"dellinc": "dell",
		"delltech": "dell",
		"delltechnology": "dell",
		"dellcomputer": "dell",
		"broadcomcorp": "broadcom",
		"broadcominc": "broadcom",
		"broadcomtech": "broadcom",
		"broadcomtechnology": "broadcom",
		"texasinstrumentsinc": "texas instruments",
		"titech": "texas instruments",
		"titechnology": "texas instruments",
		"stripeinc": "stripe",
		"stripetech": "stripe",
		"stripetechnology": "stripe",
		"plaidinc": "plaid",
		"plaidtech": "plaid",
		"plaidtechnology": "plaid",
		"databricksinc": "databricks",
		"databrickstech": "databricks",
		"databrickstechnology": "databricks",
		"snowflakeinc": "snowflake",
		"snowflaketech": "snowflake",
		"snowflaketechnology": "snowflake",
		"palantirinc": "palantir",
		"palantirtech": "palantir",
		"palantirtechnology": "palantir",
		"robloxcorp": "roblox",
		"robloxinc": "roblox",
		"robloxtech": "roblox",
		"coinbaseinc": "coinbase",
		"coinbasetech": "coinbase",
		"coinbasetechnology": "coinbase",
		"robinhoodinc": "robinhood",
		"robinhoodtech": "robinhood",
		"robinhoodtechnology": "robinhood",
		"affirminc": "affirm",
		"affirmtech": "affirm",
		"affirmtechnology": "affirm",
		"chimeinc": "chime",
		"chimetech": "chime",
		"chimetechnology": "chime",
		"instacartinc": "instacart",
		"instacarttech": "instacart",
		"instacarttechnology": "instacart",
		"doordashcorp": "doordash",
		"doordashinc": "doordash",
		"doordashtech": "doordash",
		"airbnbinc": "airbnb",
		"airbnbtech": "airbnb",
		"airbnbtechnology": "airbnb",
		"uberinc": "uber",
		"ubertech": "uber",
		"ubertechnology": "uber",
		"lyftinc": "lyft",
		"lyfttech": "lyft",
		"lyfttechnology": "lyft",
		"bytedanceinc": "bytedance",
		"bytedancetech": "bytedance",
		"bytedancetechnology": "bytedance",
		"snaptech": "snap",
		"snaptechnology": "snap",
		"pinterestinc": "pinterest",
		"pinteresttech": "pinterest",
		"pinteresttechnology": "pinterest",
		"squareinc": "square",
		"squaretech": "square",
		"squaretechnology": "square",
		"dropboxinc": "dropbox",
		"dropboxtech": "dropbox",
		"dropboxtechnology": "dropbox",
		"twilioinc": "twilio",
		"twiliotech": "twilio",
		"twiliotechnology": "twilio",
		"asanainc": "asana",
		"asanatech": "asana",
		"asanatechnology": "asana",
		"figmainc": "figma",
		"figmatech": "figma",
		"figmatechnology": "figma",
		"notioninc": "notion",
		"notiontech": "notion",
		"notiontechnology": "notion",
		"airtableinc": "airtable",
		"airtabletech": "airtable",
		"airtabletechnology": "airtable",
		"discordinc": "discord",
		"discordtech": "discord",
		"discordtechnology": "discord",
		"gitlabinc": "gitlab",
		"gitlabtech": "gitlab",
		"gitlabtechnology": "gitlab",
		"githubinc": "github",
		"githubtech": "github",
		"githubtechnology": "github",
		"teslainc": "tesla",
		"teslatech": "tesla",
		"teslatechnology": "tesla",
		"teslacorp": "tesla",
		"spacexcorp": "spacex",
		"spacextech": "spacex",
		"spacextechnology": "spacex",
		"rivianinc": "rivian",
		"riviantech": "rivian",
		"riviantechnology": "rivian",
		"lucidinc": "lucid",
		"lucidtech": "lucid",
		"lucidtechnology": "lucid",
		"arminc": "arm",
		"armtech": "arm",
		"armtechnology": "arm",
		"samsunginc": "samsung",
		"samsungtech": "samsung",
		"samsungtechnology": "samsung",
		"microninc": "micron",
		"microntech": "micron",
		"microntechnology": "micron",
		"appliedmaterialsinc": "applied materials",
		"appliedmaterialstech": "applied materials",
		"lamresearchinc": "lam research",
		"lamresearchtech": "lam research",
		"asmlholding": "asml",
		"asmlinc": "asml",
		"asmltech": "asml",
		"mongodbinc": "mongodb",
		"mongodbtech": "mongodb",
		"mongodbtechnology": "mongodb",
		"datadoginc": "datadog",
		"datadogtech": "datadog",
		"datadogtechnology": "datadog",
		"splunkinc": "splunk",
		"splunktech": "splunk",
		"splunktechnology": "splunk",
		"oktainc": "okta",
		"oktatech": "okta",
		"oktatechnology": "okta",
		"crowdstrikeinc": "crowdstrike",
		"crowdstriketech": "crowdstrike",
		"crowdstriketechnology": "crowdstrike",
		"paloaltoinc": "palo alto networks",
		"paloaltotech": "palo alto networks",
		"zscalerinc": "zscaler",
		"zscalertech": "zscaler",
		"zscalertechnology": "zscaler",
		"fortinettechnology": "fortinet",
		"cloudflare": "cloudflare",
		"fastlyinc": "fastly",
		"fastlytech": "fastly",
		"fastlytechnology": "fastly",
		"digitaloceancorp": "digitalocean",
		"digitaloceantech": "digitalocean",
		"hashicorpinc": "hashicorp",
		"hashicorptech": "hashicorp",
		"hashicorptechnology": "hashicorp",
		"confluentinc": "confluent",
		"confluenttech": "confluent",
		"confluenttechnology": "confluent",
		"elasticinc": "elastic",
		"elastictech": "elastic",
		"elastictechnology": "elastic",
		"newrelicinc": "new relic",
		"newrelictech": "new relic",
		"docusigninc": "docusign",
		"docusigntech": "docusign",
		"docusigntechnology": "docusign",
		"zoominc": "zoom",
		"zoomtech": "zoom",
		"zoomtechnology": "zoom",
		"slackinc": "slack",
		"slacktech": "slack",
		"slacktechnology": "slack",
	}

	if normalizedCompany, exists := companyVariations[company]; exists {
		if debug {
			fmt.Printf("[DEBUG] Company name variation found: %s -> %s\n", originalCompany, normalizedCompany)
		}
		company = normalizedCompany
	}

	topCompaniesCacheMux.RLock()
	defer topCompaniesCacheMux.RUnlock()
	isTopPaying := topCompaniesCache[company]
	if debug {
		if isTopPaying {
			fmt.Printf("[DEBUG] Found top paying company: %s (normalized from: %s)\n", company, originalCompany)
		} else {
			fmt.Printf("[DEBUG] Company not in top paying list: %s (normalized from: %s)\n", company, originalCompany)
		}
	}
	return isTopPaying
}