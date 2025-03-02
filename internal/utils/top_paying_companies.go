package utils

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"
)

var (
	// Cache for top paying companies
	topCompaniesCache    = make(map[string]bool)
	// Map to store original company names
	originalCompanyNames = make(map[string]string) // normalized name -> original name
	topCompaniesCacheMux sync.Mutex
	lastFetchTime        time.Time
	cacheDuration        = 24 * time.Hour // Cache for 24 hours
	
	// Cache file path
	cacheFilePath        string
)

// CacheData represents the structure of the cache file
type CacheData struct {
	Companies       map[string]bool   `json:"companies"`
	OriginalNames   map[string]string `json:"original_names"`
	LastFetchTime   time.Time         `json:"last_fetch_time"`
}

// init initializes the cache file path
func init() {
	// Get user's home directory
	homeDir, err := os.UserHomeDir()
	if err != nil {
		// If we can't get the home directory, use a temporary directory
		homeDir = os.TempDir()
	}
	
	// Create .salarysleuth directory if it doesn't exist
	cacheDir := filepath.Join(homeDir, ".salarysleuth")
	if _, err := os.Stat(cacheDir); os.IsNotExist(err) {
		os.Mkdir(cacheDir, 0755)
	}
	
	// Set cache file path
	cacheFilePath = filepath.Join(cacheDir, "top_companies_cache.json")
}

// FetchTopPayingCompaniesFromLevelsFyi fetches the list of top paying companies from levels.fyi
// using multiple URLs for different software engineering levels
func FetchTopPayingCompaniesFromLevelsFyi(debug bool) (map[string]bool, map[string]string, error) {
	// URLs for the levels.fyi leaderboards for different Software Engineer levels in the US
	urls := []string{
		"https://www.levels.fyi/leaderboard/Software-Engineer/All-Levels/country/United-States/",
		"https://www.levels.fyi/leaderboard/Software-Engineer/Entry-Level-Engineer/country/United-States/",
		"https://www.levels.fyi/leaderboard/Software-Engineer/Software-Engineer/country/United-States/",
		"https://www.levels.fyi/leaderboard/Software-Engineer/Senior-Engineer/country/United-States/",
		"https://www.levels.fyi/leaderboard/Software-Engineer/Staff-Engineer/country/United-States/",
		"https://www.levels.fyi/leaderboard/Software-Engineer/Principal-Engineer/country/United-States/",
	}

	// Create a client with a timeout
	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	// Map to store unique companies
	uniqueCompanies := make(map[string]bool)
	// Map to store original company names
	originalNames := make(map[string]string)

	// Regular expression to check if a string is just a number
	numericRegex := regexp.MustCompile(`^[0-9]+$`)

	// Process each URL
	for _, url := range urls {
		if debug {
			fmt.Printf("[DEBUG] Fetching data from: %s\n", url)
		}
		
		// Send GET request
		resp, err := client.Get(url)
		if err != nil {
			if debug {
				fmt.Printf("[DEBUG] Error fetching URL %s: %v\n", url, err)
			}
			continue
		}

		if resp.StatusCode != 200 {
			if debug {
				fmt.Printf("[DEBUG] Error: status code %d for URL %s\n", resp.StatusCode, url)
			}
			resp.Body.Close()
			continue
		}

		// Load the HTML document
		doc, err := goquery.NewDocumentFromReader(resp.Body)
		resp.Body.Close()
		
		if err != nil {
			if debug {
				fmt.Printf("[DEBUG] Error parsing HTML from %s: %v\n", url, err)
			}
			continue
		}

		// Find all company names within the nav-link elements
		doc.Find("a.nav-link.d-flex.align-items-center strong").Each(func(i int, s *goquery.Selection) {
			companyName := strings.TrimSpace(s.Text())
			// Skip if the text is empty or just a number
			if companyName != "" && !numericRegex.MatchString(companyName) {
				// Normalize company name for comparison
				normalizedName := NormalizeCompanyName(companyName)
				uniqueCompanies[normalizedName] = true
				
				// Store the original name
				originalNames[normalizedName] = companyName
				
				// Also store the original name for debugging
				if debug {
					fmt.Printf("[DEBUG] Found company: %s (normalized: %s)\n", companyName, normalizedName)
				}
			}
		})
		
		// Add a small delay between requests to be respectful to the server
		time.Sleep(1 * time.Second)
	}

	if debug {
		fmt.Printf("[DEBUG] Total unique companies found: %d\n", len(uniqueCompanies))
	}

	return uniqueCompanies, originalNames, nil
}

// NormalizeCompanyName normalizes a company name for comparison
func NormalizeCompanyName(name string) string {
	// Convert to lowercase
	normalized := strings.ToLower(name)
	
	// Special case for Facebook/Meta
	if normalized == "facebook" {
		return "meta"
	}
	
	// Remove common suffixes and words
	normalized = strings.ReplaceAll(normalized, " ", "")
	normalized = strings.ReplaceAll(normalized, ".", "")
	normalized = strings.ReplaceAll(normalized, ",", "")
	normalized = strings.ReplaceAll(normalized, "inc", "")
	normalized = strings.ReplaceAll(normalized, "corp", "")
	normalized = strings.ReplaceAll(normalized, "technologies", "")
	normalized = strings.ReplaceAll(normalized, "technology", "")
	normalized = strings.ReplaceAll(normalized, "llc", "")
	normalized = strings.ReplaceAll(normalized, "ltd", "")
	
	return normalized
}

// loadCacheFromFile attempts to load the cache from the cache file
func loadCacheFromFile(debug bool) bool {
	// Check if cache file exists
	if _, err := os.Stat(cacheFilePath); os.IsNotExist(err) {
		if debug {
			fmt.Printf("[DEBUG] Cache file does not exist: %s\n", cacheFilePath)
		}
		return false
	}
	
	// Read cache file
	data, err := os.ReadFile(cacheFilePath)
	if err != nil {
		if debug {
			fmt.Printf("[DEBUG] Error reading cache file: %v\n", err)
		}
		return false
	}
	
	// Parse cache data
	var cacheData CacheData
	if err := json.Unmarshal(data, &cacheData); err != nil {
		if debug {
			fmt.Printf("[DEBUG] Error parsing cache file: %v\n", err)
		}
		return false
	}
	
	// Check if cache is still valid
	if time.Since(cacheData.LastFetchTime) >= cacheDuration {
		if debug {
			fmt.Printf("[DEBUG] Cache is expired (last fetch: %v)\n", cacheData.LastFetchTime)
		}
		return false
	}
	
	// Cache is valid, use it
	topCompaniesCache = cacheData.Companies
	originalCompanyNames = cacheData.OriginalNames
	lastFetchTime = cacheData.LastFetchTime
	
	if debug {
		fmt.Printf("[DEBUG] Loaded cache from file with %d companies (last fetch: %v)\n", 
			len(topCompaniesCache), lastFetchTime)
	}
	
	return true
}

// saveCacheToFile saves the current cache to the cache file
func saveCacheToFile(debug bool) {
	// Create cache data
	cacheData := CacheData{
		Companies:     topCompaniesCache,
		OriginalNames: originalCompanyNames,
		LastFetchTime: lastFetchTime,
	}
	
	// Marshal to JSON
	data, err := json.MarshalIndent(cacheData, "", "  ")
	if err != nil {
		if debug {
			fmt.Printf("[DEBUG] Error marshaling cache data: %v\n", err)
		}
		return
	}
	
	// Write to file
	if err := os.WriteFile(cacheFilePath, data, 0644); err != nil {
		if debug {
			fmt.Printf("[DEBUG] Error writing cache file: %v\n", err)
		}
		return
	}
	
	if debug {
		fmt.Printf("[DEBUG] Saved cache to file: %s\n", cacheFilePath)
	}
}

// FetchTopPayingCompanies fetches the list of top paying companies from levels.fyi
// This is the main function that will be called by other parts of the code
func FetchTopPayingCompanies(debug bool) error {
	topCompaniesCacheMux.Lock()
	defer topCompaniesCacheMux.Unlock()

	// Check if in-memory cache is still valid
	if time.Since(lastFetchTime) < cacheDuration && len(topCompaniesCache) > 0 {
		if debug {
			fmt.Printf("\n[DEBUG] Using in-memory cached top companies list (%d companies)\n", len(topCompaniesCache))
		}
		return nil
	}
	
	// Try to load from file cache
	if loadCacheFromFile(debug) {
		return nil
	}

	if debug {
		fmt.Printf("\n[DEBUG] Cache expired or empty, fetching fresh top companies data\n")
	}

	// Fetch companies from levels.fyi
	companies, originalNames, err := FetchTopPayingCompaniesFromLevelsFyi(debug)
	if err != nil {
		if debug {
			fmt.Printf("[DEBUG] Error fetching companies from levels.fyi: %v\n", err)
		}
		
		// If fetch fails, use a minimal default list
		// This list was pulled on 03/01/2025
		if len(topCompaniesCache) == 0 {
			topCompaniesCache = map[string]bool{
				"affirm": true,
				"airbnb": true,
				"airtable": true,
				"alibaba": true,
				"amazon": true,
				"amplitude": true,
				"andurilindustries": true,
				"angellist": true,
				"applovin": true,
				"apple": true,
				"aquatic": true,
				"boschglobal": true,
				"brex": true,
				"bridgewaterassociates": true,
				"broadcom": true,
				"bytedance": true,
				"calicolifesciences": true,
				"chairesearch": true,
				"characterai": true,
				"chronosphere": true,
				"circle": true,
				"citadel": true,
				"classdojo": true,
				"cloudkitchens": true,
				"clubhouse": true,
				"coinbase": true,
				"coupang": true,
				"cruise": true,
				"databricks": true,
				"discord": true,
				"docusign": true,
				"doordash": true,
				"dropbox": true,
				"f5networks": true,
				"meta": true, // Facebook is normalized to Meta
				"faire": true,
				"fidelityinvestments": true,
				"figma": true,
				"fiverings": true,
				"fordmotor": true,
				"google": true,
				"hudsonrivertrading": true,
				"imc": true,
				"instacart": true,
				"intuit": true,
				"janestreet": true,
				"latitudeai": true,
				"leidos": true,
				"linkedin": true,
				"microsoft": true,
				"millennium": true,
				"mystenlabs": true,
				"netflix": true,
				"notion": true,
				"nuro": true,
				"oldmission": true,
				"openai": true,
				"opensea": true,
				"optiver": true,
				"oracle": true,
				"pdtpartners": true,
				"pagebites": true,
				"pinterest": true,
				"plaid": true,
				"proofpoint": true,
				"radixtrading": true,
				"reddit": true,
				"remitly": true,
				"rippling": true,
				"robinhood": true,
				"roblox": true,
				"roku": true,
				"slack": true,
				"snap": true,
				"snowflake": true,
				"stackav": true,
				"stripe": true,
				"stubhub": true,
				"tgsmanagement": true,
				"theblock": true,
				"theshawgroup": true,
				"thumbtack": true,
				"toyotaresearchinstitute": true,
				"twitch": true,
				"twosigma": true,
				"usbank": true,
				"uber": true,
				"vaticinvestments": true,
				"waymo": true,
				"wovenplanetgroup": true,
			}
			
			// Set the last fetch time to now
			lastFetchTime = time.Now()
			
			// Save the default list to cache file
			saveCacheToFile(debug)
			
			return nil
		}
		
		return err
	}

	// Update the cache
	topCompaniesCache = companies
	originalCompanyNames = originalNames
	lastFetchTime = time.Now()
	
	// Save to file cache
	saveCacheToFile(debug)

	if debug {
		fmt.Printf("[DEBUG] Updated top companies cache with %d companies\n", len(topCompaniesCache))
	}

	return nil
}

// IsTopPayingCompany checks if a company is in the list of top paying companies
func IsTopPayingCompany(company string, debug bool) bool {
	// Ensure we have the latest top companies data
	if err := FetchTopPayingCompanies(debug); err != nil {
		// If fetch fails, fall back to existing cache
		if len(topCompaniesCache) == 0 {
			// If no cache exists, use a minimal default list
			topCompaniesCacheMux.Lock()
			topCompaniesCache = map[string]bool{
				"netflix": true,
				"google": true,
				"meta": true,
				"apple": true,
				"microsoft": true,
				"amazon": true,
				"uber": true,
				"lyft": true,
				"airbnb": true,
				"stripe": true,
				"coinbase": true,
				"robinhood": true,
				"snap": true,
				"twitter": true,
				"linkedin": true,
				"square": true,
				"block": true,
				"pinterest": true,
				"dropbox": true,
				"salesforce": true,
				"adobe": true,
				"oracle": true,
				"intel": true,
				"nvidia": true,
				"amd": true,
				"palantir": true,
				"databricks": true,
				"snowflake": true,
				"bytedance": true,
				"instacart": true,
				"doordash": true,
				"openai": true,
			}
			
			// Set default original names
			originalCompanyNames = map[string]string{
				"netflix": "Netflix",
				"google": "Google",
				"meta": "Meta", // This will be used for both Meta and Facebook
				"apple": "Apple",
				"microsoft": "Microsoft",
				"amazon": "Amazon",
				"uber": "Uber",
				"lyft": "Lyft",
				"airbnb": "Airbnb",
				"stripe": "Stripe",
				"coinbase": "Coinbase",
				"robinhood": "Robinhood",
				"snap": "Snap",
				"twitter": "Twitter",
				"linkedin": "LinkedIn",
				"square": "Square",
				"block": "Block",
				"pinterest": "Pinterest",
				"dropbox": "Dropbox",
				"salesforce": "Salesforce",
				"adobe": "Adobe",
				"oracle": "Oracle",
				"intel": "Intel",
				"nvidia": "NVIDIA",
				"amd": "AMD",
				"palantir": "Palantir",
				"databricks": "Databricks",
				"snowflake": "Snowflake",
				"bytedance": "ByteDance",
				"instacart": "Instacart",
				"doordash": "DoorDash",
				"openai": "OpenAI",
			}
			topCompaniesCacheMux.Unlock()
		}
	}

	// Normalize the company name for comparison
	normalizedCompany := NormalizeCompanyName(company)
	
	topCompaniesCacheMux.Lock()
	defer topCompaniesCacheMux.Unlock()
	
	if debug {
		fmt.Printf("[DEBUG] Checking if %s (normalized: %s) is a top paying company\n", company, normalizedCompany)
	}
	
	return topCompaniesCache[normalizedCompany]
}

// PrintTopPayingCompanies prints the list of top paying companies
func PrintTopPayingCompanies(debug bool) error {
	if err := FetchTopPayingCompanies(debug); err != nil {
		return err
	}
	
	topCompaniesCacheMux.Lock()
	defer topCompaniesCacheMux.Unlock()
	
	// Create a slice of company names for sorting
	type CompanyEntry struct {
		NormalizedName string
		OriginalName   string
	}
	
	companies := make([]CompanyEntry, 0, len(topCompaniesCache))
	for normalizedName := range topCompaniesCache {
		originalName := originalCompanyNames[normalizedName]
		if originalName == "" {
			originalName = normalizedName // Fallback to normalized name if original not found
		}
		companies = append(companies, CompanyEntry{
			NormalizedName: normalizedName,
			OriginalName:   originalName,
		})
	}
	
	// Sort companies alphabetically by original name
	sort.Slice(companies, func(i, j int) bool {
		return companies[i].OriginalName < companies[j].OriginalName
	})
	
	// Print the companies
	fmt.Println("\nTop Paying Companies for Software Engineers in the US (across all levels):")
	
	if len(companies) > 0 {
		for i, company := range companies {
			fmt.Printf("%d. %s\n", i+1, company.OriginalName)
		}
		fmt.Printf("\nTotal unique companies found: %d\n", len(companies))
	} else {
		fmt.Println("No companies found. The website structure might have changed.")
	}
	
	return nil
} 