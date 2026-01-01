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
	greenhouseAPIURL   = "https://api.greenhouse.io/v1/boards"
	minGreenhouseDelay = 1 * time.Second
	maxGreenhouseDelay = 3 * time.Second
)

// GreenhouseJobsResponse represents the response from the Greenhouse jobs API
type GreenhouseJobsResponse struct {
	Jobs []GreenhouseJob `json:"jobs"`
}

// GreenhouseJob represents a job posting from Greenhouse
type GreenhouseJob struct {
	ID          int64  `json:"id"`
	Title       string `json:"title"`
	AbsoluteURL string `json:"absolute_url"`
	Location    struct {
		Name string `json:"name"`
	} `json:"location"`
	Content  string `json:"content"` // HTML content (only in single job response)
	Metadata []struct {
		ID        int64  `json:"id"`
		Name      string `json:"name"`
		ValueType string `json:"value_type"`
		Value     any    `json:"value"`
	} `json:"metadata"`
}

// GreenhouseJobDetail represents a detailed job posting from Greenhouse
type GreenhouseJobDetail struct {
	ID          int64  `json:"id"`
	Title       string `json:"title"`
	AbsoluteURL string `json:"absolute_url"`
	Location    struct {
		Name string `json:"name"`
	} `json:"location"`
	Content  string `json:"content"`
	Metadata []struct {
		ID        int64  `json:"id"`
		Name      string `json:"name"`
		ValueType string `json:"value_type"`
		Value     any    `json:"value"`
	} `json:"metadata"`
}

// List of major tech companies that use Greenhouse
var greenhouseCompanies = []string{
	"discord",
	"airbnb",
	"pinterest",
	"dropbox",
	"instacart",
	"doordash",
	"lyft",
	"stripe",
	"coinbase",
	"robinhood",
	"figma",
	"notion",
	"airtable",
	"canva",
	"gitlab",
	"twitch",
	"snap",
	"square",
	"affirm",
	"brex",
	"ramp",
	"chime",
	"gusto",
	"rippling",
	"lattice",
	"vercel",
	"hashicorp",
	"datadog",
	"mongodb",
	"elastic",
	"confluent",
	"snowflake",
	"databricks",
	"dbt",
	"miro",
	"loom",
	"calendly",
	"zapier",
	"asana",
	"monday",
	"clickup",
	"linear",
	"retool",
	"webflow",
	"framer",
}

// ScrapeGreenhouse scrapes job listings from Greenhouse job boards
func ScrapeGreenhouse(description string, pages int, debug bool, proxyURL string, progress *models.ScrapeProgress, topPayOnly bool) ([]models.SalaryInfo, error) {
	httpClient := client.CreateProxyHTTPClient(proxyURL)
	var results []models.SalaryInfo

	if debug {
		fmt.Printf("Searching Greenhouse for jobs with description: %s\n", description)
	}

	for _, company := range greenhouseCompanies {
		// Skip if not in top paying companies when filter is enabled
		if topPayOnly && !utils.IsTopPayingCompany(company, debug) {
			if debug {
				fmt.Printf("Skipping %s - not in top paying companies list\n", company)
			}
			continue
		}

		if debug {
			fmt.Printf("Fetching jobs from Greenhouse for %s\n", company)
		}

		jobs, err := fetchGreenhouseJobs(httpClient, company, debug)
		if err != nil {
			if debug {
				fmt.Printf("Error fetching jobs for %s: %v\n", company, err)
			}
			continue
		}

		if debug {
			fmt.Printf("Found %d total jobs for %s\n", len(jobs), company)
		}

		// Filter jobs by description and process them
		matchingJobs := 0
		for _, job := range jobs {
			// Check if job title or content matches search criteria
			titleMatch := strings.Contains(strings.ToLower(job.Title), strings.ToLower(description))
			
			if !titleMatch {
				continue
			}

			matchingJobs++

			// Get detailed job info to extract salary
			salary := "Not Available"
			jobDetail, err := fetchGreenhouseJobDetail(httpClient, company, job.ID, debug)
			if err == nil && jobDetail.Content != "" {
				// Try to find salary in the job content
				if salaryMatch := utils.FindSalaryInText(jobDetail.Content); salaryMatch != "" {
					salary = salaryMatch
					if debug {
						fmt.Printf("Found salary %s for job %s\n", salary, job.Title)
					}
				}
				
				// Also check metadata for salary info
				for _, meta := range jobDetail.Metadata {
					metaName := strings.ToLower(meta.Name)
					if strings.Contains(metaName, "salary") || strings.Contains(metaName, "compensation") {
						if strVal, ok := meta.Value.(string); ok && strVal != "" {
							if salary == "Not Available" {
								salary = strVal
							}
						}
					}
				}
			}

			// Format company name with proper capitalization
			formattedCompany := formatCompanyName(company)

			jobInfo := models.SalaryInfo{
				Company:     formattedCompany,
				Title:       job.Title,
				Location:    job.Location.Name,
				URL:         job.AbsoluteURL,
				SalaryRange: salary,
				Source:      "greenhouse",
			}

			results = append(results, jobInfo)
			if debug {
				fmt.Printf("Added job: %s at %s (%s)\n", jobInfo.Title, jobInfo.Company, jobInfo.Location)
			}
		}

		if debug && matchingJobs > 0 {
			fmt.Printf("Found %d matching jobs for %s\n", matchingJobs, company)
		}

		progress.FoundJobs = len(results)

		// Add delay between companies to be respectful
		delay := time.Duration(rand.Int63n(int64(maxGreenhouseDelay-minGreenhouseDelay))) + minGreenhouseDelay
		if debug {
			fmt.Printf("Waiting %v before next company\n", delay)
		}
		time.Sleep(delay)
	}

	return results, nil
}

// fetchGreenhouseJobs fetches all job listings from a company's Greenhouse board
func fetchGreenhouseJobs(httpClient *http.Client, company string, debug bool) ([]GreenhouseJob, error) {
	url := fmt.Sprintf("%s/%s/jobs", greenhouseAPIURL, company)

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
		fmt.Printf("Greenhouse API response preview:\n%s\n", preview)
	}

	var jobsResponse GreenhouseJobsResponse
	if err := json.Unmarshal(body, &jobsResponse); err != nil {
		return nil, fmt.Errorf("failed to parse JSON response: %v", err)
	}

	return jobsResponse.Jobs, nil
}

// fetchGreenhouseJobDetail fetches detailed job information including content
func fetchGreenhouseJobDetail(httpClient *http.Client, company string, jobID int64, debug bool) (*GreenhouseJobDetail, error) {
	url := fmt.Sprintf("%s/%s/jobs/%d", greenhouseAPIURL, company, jobID)

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
		return nil, fmt.Errorf("failed to fetch job detail: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("received non-200 status code: %d", resp.StatusCode)
	}

	body, err := client.ReadResponseBody(resp)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %v", err)
	}

	var jobDetail GreenhouseJobDetail
	if err := json.Unmarshal(body, &jobDetail); err != nil {
		return nil, fmt.Errorf("failed to parse JSON response: %v", err)
	}

	return &jobDetail, nil
}

// formatCompanyName formats a company slug to a proper display name
func formatCompanyName(slug string) string {
	// Special cases for company names
	specialNames := map[string]string{
		"discord":    "Discord",
		"airbnb":     "Airbnb",
		"pinterest":  "Pinterest",
		"dropbox":    "Dropbox",
		"instacart":  "Instacart",
		"doordash":   "DoorDash",
		"lyft":       "Lyft",
		"stripe":     "Stripe",
		"coinbase":   "Coinbase",
		"robinhood":  "Robinhood",
		"figma":      "Figma",
		"notion":     "Notion",
		"airtable":   "Airtable",
		"canva":      "Canva",
		"gitlab":     "GitLab",
		"twitch":     "Twitch",
		"snap":       "Snap",
		"square":     "Square",
		"affirm":     "Affirm",
		"brex":       "Brex",
		"ramp":       "Ramp",
		"chime":      "Chime",
		"gusto":      "Gusto",
		"rippling":   "Rippling",
		"lattice":    "Lattice",
		"vercel":     "Vercel",
		"hashicorp":  "HashiCorp",
		"datadog":    "Datadog",
		"mongodb":    "MongoDB",
		"elastic":    "Elastic",
		"confluent":  "Confluent",
		"snowflake":  "Snowflake",
		"databricks": "Databricks",
		"dbt":        "dbt Labs",
		"miro":       "Miro",
		"loom":       "Loom",
		"calendly":   "Calendly",
		"zapier":     "Zapier",
		"asana":      "Asana",
		"monday":     "monday.com",
		"clickup":    "ClickUp",
		"linear":     "Linear",
		"retool":     "Retool",
		"webflow":    "Webflow",
		"framer":     "Framer",
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
