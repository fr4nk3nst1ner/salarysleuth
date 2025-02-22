package scraper

import (
	"fmt"
	"net/http"
	"strings"
	"time"
	"math/rand"
	"encoding/json"

	"github.com/fr4nk3nst1ner/salarysleuth/internal/models"
	"github.com/fr4nk3nst1ner/salarysleuth/internal/client"
	"github.com/fr4nk3nst1ner/salarysleuth/internal/utils"
)

const (
	leverBaseURL = "https://jobs.lever.co"
	minLeverDelay = 2 * time.Second
	maxLeverDelay = 5 * time.Second
)

// LeverJob represents a job posting from Lever
type LeverJob struct {
	Title       string `json:"title"`
	Description string `json:"descriptionPlain"`
	Lists       []struct {
		Text  string `json:"text"`
		Items []struct {
			Text string `json:"text"`
		} `json:"content"`
	} `json:"lists"`
	Categories struct {
		Location   string `json:"location"`
		Team       string `json:"team"`
		Department string `json:"department"`
	} `json:"categories"`
	ID          string `json:"id"`
	ApplyURL    string `json:"applyUrl"`
	Additional  string `json:"additional"`
}

// ScrapeLever scrapes job listings from Lever's job boards
func ScrapeLever(description string, pages int, debug bool, proxyURL string, progress *models.ScrapeProgress) ([]models.SalaryInfo, error) {
	httpClient := client.CreateProxyHTTPClient(proxyURL)
	var results []models.SalaryInfo

	// List of major tech companies that use Lever
	companies := []string{
		"databricks",
		"duolingo",
		"samsara",
		"retool",
		"flexport",
		"faire",
		"plaid",
		"intercom",
		"carta",
		"verkada",
	}

	if debug {
		fmt.Printf("Searching for jobs with description: %s\n", description)
	}

	for _, company := range companies {
		// Build company job board URL
		companyURL := fmt.Sprintf("%s/%s", leverBaseURL, company)
		
		if debug {
			fmt.Printf("Fetching jobs from %s\n", companyURL)
		}

		jobs, err := fetchLeverJobs(httpClient, companyURL, description, debug)
		if err != nil {
			if debug {
				fmt.Printf("Error fetching jobs for %s: %v\n", company, err)
			}
			continue
		}

		if debug {
			fmt.Printf("Found %d jobs for %s\n", len(jobs), company)
		}

		// Process jobs
		for _, job := range jobs {
			if debug {
				fmt.Printf("Processing job: %s\n", job.Title)
			}

			// Check if job title matches search criteria
			if !strings.Contains(strings.ToLower(job.Title), strings.ToLower(description)) {
				if debug {
					fmt.Printf("Skipping job %s - title doesn't match search criteria\n", job.Title)
				}
				continue
			}

			// Extract salary from job description and additional info
			salary := "Not Available"
			fullText := job.Description + " " + job.Additional
			
			// Also check any lists in the job posting
			for _, list := range job.Lists {
				fullText += " " + list.Text
				for _, item := range list.Items {
					fullText += " " + item.Text
				}
			}

			if salaryMatch := utils.FindSalaryInText(fullText); salaryMatch != "" {
				salary = salaryMatch
				if debug {
					fmt.Printf("Found salary %s for job %s\n", salary, job.Title)
				}
			} else if debug {
				fmt.Printf("No salary found in job description for %s\n", job.Title)
			}

			jobInfo := models.SalaryInfo{
				Company:     strings.Title(company),
				Title:      job.Title,
				Location:   job.Categories.Location,
				URL:        job.ApplyURL,
				SalaryRange: salary,
				Source:     "lever",
			}

			results = append(results, jobInfo)
			if debug {
				fmt.Printf("Added job: %s at %s (%s)\n", jobInfo.Title, jobInfo.Company, jobInfo.Location)
			}
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

// fetchLeverJobs fetches job listings from a company's Lever job board
func fetchLeverJobs(httpClient *http.Client, baseURL, searchTerm string, debug bool) ([]LeverJob, error) {
	// First, fetch the job board page to get the workspaceID
	req, err := http.NewRequest("GET", baseURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %v", err)
	}

	// Add headers to mimic browser behavior
	headers := client.GetRandomHeaders()
	for key, values := range headers {
		req.Header[key] = values
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch job board: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("received non-200 status code: %d", resp.StatusCode)
	}

	// Read and parse the response body
	body, err := client.ReadResponseBody(resp)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %v", err)
	}

	if debug {
		fmt.Printf("Job board response body:\n%s\n", string(body))
	}

	// Extract workspaceID from the page
	workspaceID := extractWorkspaceID(string(body))
	if workspaceID == "" {
		return nil, fmt.Errorf("failed to extract workspace ID")
	}

	// Now fetch the jobs using the workspaceID
	jobsURL := fmt.Sprintf("%s/v1/workspaces/%s/postings/search?q=%s", leverBaseURL, workspaceID, searchTerm)
	
	req, err = http.NewRequest("GET", jobsURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create jobs request: %v", err)
	}

	// Add headers
	for key, values := range headers {
		req.Header[key] = values
	}

	resp, err = httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch jobs: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("received non-200 status code from jobs API: %d", resp.StatusCode)
	}

	// Read and parse the jobs response
	body, err = client.ReadResponseBody(resp)
	if err != nil {
		return nil, fmt.Errorf("failed to read jobs response body: %v", err)
	}

	if debug {
		fmt.Printf("Jobs API response body:\n%s\n", string(body))
	}

	var jobs []LeverJob
	if err := json.Unmarshal(body, &jobs); err != nil {
		return nil, fmt.Errorf("failed to parse JSON response: %v", err)
	}

	return jobs, nil
}

// extractWorkspaceID extracts the workspace ID from the job board HTML
func extractWorkspaceID(html string) string {
	// Look for a pattern like 'workspaceId: "abc123"'
	start := strings.Index(html, `workspaceId: "`)
	if start == -1 {
		return ""
	}
	start += len(`workspaceId: "`)
	end := strings.Index(html[start:], `"`)
	if end == -1 {
		return ""
	}
	return html[start : start+end]
} 