package scraper

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"strings"
	"time"

	"github.com/fr4nk3nst1ner/salarysleuth/internal/client"
	"github.com/fr4nk3nst1ner/salarysleuth/internal/models"
	"github.com/fr4nk3nst1ner/salarysleuth/internal/utils"
)

const (
	leverAPIURL   = "https://api.lever.co/v0/postings"
	minLeverDelay = 1 * time.Second
	maxLeverDelay = 3 * time.Second
)

// LeverJob represents a job posting from Lever's public API
type LeverJob struct {
	ID               string `json:"id"`
	Text             string `json:"text"` // Job title
	HostedURL        string `json:"hostedUrl"`
	ApplyURL         string `json:"applyUrl"`
	DescriptionPlain string `json:"descriptionPlain"`
	Description      string `json:"description"` // HTML description
	Additional       string `json:"additional"`
	AdditionalPlain  string `json:"additionalPlain"`
	Categories       struct {
		Commitment string `json:"commitment"`
		Department string `json:"department"`
		Location   string `json:"location"`
		Team       string `json:"team"`
	} `json:"categories"`
	Lists []struct {
		Text    string `json:"text"`
		Content string `json:"content"`
	} `json:"lists"`
	SalaryRange *struct {
		Min      int    `json:"min"`
		Max      int    `json:"max"`
		Currency string `json:"currency"`
		Interval string `json:"interval"`
	} `json:"salaryRange"`
	WorkplaceType string `json:"workplaceType"`
	CreatedAt     int64  `json:"createdAt"`
}

// List of major tech companies that use Lever
var leverCompanies = []string{
	"plaid",
	"figma",
	"notion",
	"netflix",
	"openai",
	"anthropic",
	"scale",
	"anduril",
	"verkada",
	"flexport",
	"faire",
	"intercom",
	"carta",
	"retool",
	"samsara",
	"duolingo",
	"cruise",
	"nuro",
	"waymo",
	"aurora",
	"zoox",
	"rivian",
	"lucid",
	"airtable",
	"amplitude",
	"mixpanel",
	"segment",
	"braze",
	"iterable",
	"onelogin",
	"okta",
	"auth0",
	"lacework",
	"snyk",
	"crowdstrike",
	"sentinelone",
	"palo-alto-networks",
	"cloudflare",
	"fastly",
	"netlify",
	"supabase",
	"planetscale",
	"cockroachlabs",
	"timescale",
	"yugabyte",
	"materialize",
	"starburst",
	"dremio",
	"fivetran",
	"airbyte",
}

// ScrapeLever scrapes job listings from Lever's public API
func ScrapeLever(description string, pages int, debug bool, proxyURL string, progress *models.ScrapeProgress, topPayOnly bool) ([]models.SalaryInfo, error) {
	httpClient := client.CreateProxyHTTPClient(proxyURL)
	var results []models.SalaryInfo

	if debug {
		fmt.Printf("Searching Lever for jobs with description: %s\n", description)
	}

	for _, company := range leverCompanies {
		// Skip if not in top paying companies when filter is enabled
		if topPayOnly && !utils.IsTopPayingCompany(company, debug) {
			if debug {
				fmt.Printf("Skipping %s - not in top paying companies list\n", company)
			}
			continue
		}

		if debug {
			fmt.Printf("Fetching jobs from Lever for %s\n", company)
		}

		jobs, err := fetchLeverJobs(httpClient, company, debug)
		if err != nil {
			if debug {
				fmt.Printf("Error fetching jobs for %s: %v\n", company, err)
			}
			continue
		}

		if debug {
			fmt.Printf("Found %d total jobs for %s\n", len(jobs), company)
		}

		// Filter and process jobs
		matchingJobs := 0
		for _, job := range jobs {
			// Check if job title matches search criteria
			if !strings.Contains(strings.ToLower(job.Text), strings.ToLower(description)) {
				continue
			}

			matchingJobs++

			// Extract salary information
			salary := extractLeverSalary(job, debug)

			// Format company name
			formattedCompany := formatLeverCompanyName(company)

			jobInfo := models.SalaryInfo{
				Company:     formattedCompany,
				Title:       job.Text,
				Location:    job.Categories.Location,
				URL:         job.HostedURL,
				SalaryRange: salary,
				Source:      "lever",
			}

			results = append(results, jobInfo)
			if debug {
				fmt.Printf("Added job: %s at %s (%s) - Salary: %s\n", jobInfo.Title, jobInfo.Company, jobInfo.Location, salary)
			}
		}

		if debug && matchingJobs > 0 {
			fmt.Printf("Found %d matching jobs for %s\n", matchingJobs, company)
		}

		progress.FoundJobs = len(results)

		// Add delay between companies
		delay := time.Duration(rand.Int63n(int64(maxLeverDelay-minLeverDelay))) + minLeverDelay
		if debug {
			fmt.Printf("Waiting %v before next company\n", delay)
		}
		time.Sleep(delay)
	}

	return results, nil
}

// fetchLeverJobs fetches all job listings from a company's Lever board using the public API
func fetchLeverJobs(httpClient *http.Client, company string, debug bool) ([]LeverJob, error) {
	// Use the public JSON API endpoint
	url := fmt.Sprintf("%s/%s?mode=json", leverAPIURL, company)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %v", err)
	}

	// Add headers
	headers := client.GetRandomHeaders()
	for key, values := range headers {
		req.Header[key] = values
	}
	req.Header.Set("Accept", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch jobs: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("received non-200 status code: %d", resp.StatusCode)
	}

	body, err := client.ReadResponseBody(resp)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %v", err)
	}

	if debug {
		// Only print first 500 chars to avoid overwhelming output
		preview := string(body)
		if len(preview) > 500 {
			preview = preview[:500] + "..."
		}
		fmt.Printf("Lever API response preview:\n%s\n", preview)
	}

	var jobs []LeverJob
	if err := json.Unmarshal(body, &jobs); err != nil {
		return nil, fmt.Errorf("failed to parse JSON response: %v", err)
	}

	return jobs, nil
}

// extractLeverSalary extracts salary information from a Lever job posting
func extractLeverSalary(job LeverJob, debug bool) string {
	// First, check the structured salary range field
	if job.SalaryRange != nil && job.SalaryRange.Max > 0 {
		min := job.SalaryRange.Min
		max := job.SalaryRange.Max
		currency := job.SalaryRange.Currency
		if currency == "" {
			currency = "USD"
		}
		interval := job.SalaryRange.Interval
		if interval == "" {
			interval = "year"
		}

		// Format salary range
		if min > 0 && min != max {
			return fmt.Sprintf("$%s - $%s/%s", formatNumber(min), formatNumber(max), interval)
		}
		return fmt.Sprintf("$%s/%s", formatNumber(max), interval)
	}

	// If no structured salary, search in the text content
	fullText := job.DescriptionPlain + " " + job.AdditionalPlain
	for _, list := range job.Lists {
		fullText += " " + list.Text + " " + list.Content
	}

	if salaryMatch := utils.FindSalaryInText(fullText); salaryMatch != "" {
		if debug {
			fmt.Printf("Found salary in text: %s\n", salaryMatch)
		}
		return salaryMatch
	}

	return "Not Available"
}

// formatNumber formats an integer with comma separators
func formatNumber(n int) string {
	str := fmt.Sprintf("%d", n)
	result := ""
	for i, c := range str {
		if i > 0 && (len(str)-i)%3 == 0 {
			result += ","
		}
		result += string(c)
	}
	return result
}

// formatLeverCompanyName formats a company slug to a proper display name
func formatLeverCompanyName(slug string) string {
	// Special cases for company names
	specialNames := map[string]string{
		"plaid":              "Plaid",
		"figma":              "Figma",
		"notion":             "Notion",
		"netflix":            "Netflix",
		"openai":             "OpenAI",
		"anthropic":          "Anthropic",
		"scale":              "Scale AI",
		"anduril":            "Anduril",
		"verkada":            "Verkada",
		"flexport":           "Flexport",
		"faire":              "Faire",
		"intercom":           "Intercom",
		"carta":              "Carta",
		"retool":             "Retool",
		"samsara":            "Samsara",
		"duolingo":           "Duolingo",
		"cruise":             "Cruise",
		"nuro":               "Nuro",
		"waymo":              "Waymo",
		"aurora":             "Aurora",
		"zoox":               "Zoox",
		"rivian":             "Rivian",
		"lucid":              "Lucid Motors",
		"airtable":           "Airtable",
		"amplitude":          "Amplitude",
		"mixpanel":           "Mixpanel",
		"segment":            "Segment",
		"braze":              "Braze",
		"iterable":           "Iterable",
		"onelogin":           "OneLogin",
		"okta":               "Okta",
		"auth0":              "Auth0",
		"lacework":           "Lacework",
		"snyk":               "Snyk",
		"crowdstrike":        "CrowdStrike",
		"sentinelone":        "SentinelOne",
		"palo-alto-networks": "Palo Alto Networks",
		"cloudflare":         "Cloudflare",
		"fastly":             "Fastly",
		"netlify":            "Netlify",
		"supabase":           "Supabase",
		"planetscale":        "PlanetScale",
		"cockroachlabs":      "Cockroach Labs",
		"timescale":          "Timescale",
		"yugabyte":           "Yugabyte",
		"materialize":        "Materialize",
		"starburst":          "Starburst",
		"dremio":             "Dremio",
		"fivetran":           "Fivetran",
		"airbyte":            "Airbyte",
	}

	if name, ok := specialNames[slug]; ok {
		return name
	}

	// Default: capitalize first letter of each word
	words := strings.Split(slug, "-")
	for i, word := range words {
		if len(word) > 0 {
			words[i] = strings.ToUpper(string(word[0])) + word[1:]
		}
	}
	return strings.Join(words, " ")
}
