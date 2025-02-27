package scraper

import (
//	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

//	"github.com/PuerkitoBio/goquery"
	"github.com/fr4nk3nst1ner/salarysleuth/internal/models"
	"github.com/fr4nk3nst1ner/salarysleuth/internal/client"
	"github.com/fr4nk3nst1ner/salarysleuth/internal/utils"
	"github.com/tidwall/gjson"
)

const (
	indeedAPIURL        = "https://www.indeed.com/m/basecamp/viewjob"
	indeedMobileURL     = "https://www.indeed.com/m/jobs"
	indeedMaxRetries    = 5
	indeedResultsPerPage = 10
	minIndeedDelay      = 5 * time.Second
	maxIndeedDelay      = 10 * time.Second
)

// getIndeedHeaders returns headers that mimic Indeed's mobile app
func getIndeedHeaders() http.Header {
	headers := http.Header{}
	
	// Use mobile user agent
	headers.Set("User-Agent", client.MobileUserAgents[rand.Intn(len(client.MobileUserAgents))])
	
	// Indeed-specific headers
	headers.Set("Indeed-Client-App", "MOBILE_WEB")
	headers.Set("X-Indeed-Client", "MOBILE_WEB")
	headers.Set("X-Indeed-Version", "2.0")
	
	// Common mobile headers
	headers.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	headers.Set("Accept-Language", "en-US,en;q=0.9")
	headers.Set("Accept-Encoding", "gzip, deflate, br")
	headers.Set("Connection", "keep-alive")
	headers.Set("Upgrade-Insecure-Requests", "1")
	headers.Set("Sec-Fetch-Site", "same-origin")
	headers.Set("Sec-Fetch-Mode", "navigate")
	headers.Set("Sec-Fetch-User", "?1")
	headers.Set("Sec-Fetch-Dest", "document")
	headers.Set("Sec-Ch-Ua-Mobile", "?1")
	headers.Set("Sec-Ch-Ua-Platform", `"Android"`)
	
	return headers
}

// ScrapeIndeed scrapes job listings from Indeed.com
func ScrapeIndeed(description string, pages int, debug bool, proxyURL string, progress *models.ScrapeProgress, topPayOnly bool) ([]models.SalaryInfo, error) {
	httpClient := client.CreateProxyHTTPClient(proxyURL)
	var results []models.SalaryInfo

	if debug {
		fmt.Printf("Searching for jobs with description: %s\n", description)
	}

	// Format search query
	query := strings.Join(strings.Fields(description), "+")

	for page := 0; page < pages; page++ {
		// Build search URL with pagination
		start := page * indeedResultsPerPage
		searchURL := fmt.Sprintf("%s/jobs/search/api?q=%s&start=%d&limit=%d&fromage=any&filter=0", 
			indeedMobileURL,
			url.QueryEscape(query),
			start,
			indeedResultsPerPage,
		)

		if debug {
			fmt.Printf("Fetching page %d: %s\n", page+1, searchURL)
		}

		// Get the search results with retries
		var jobData []gjson.Result
		var err error
		for retry := 0; retry < indeedMaxRetries; retry++ {
			jobData, err = fetchIndeedJobData(httpClient, searchURL, debug)
			if err == nil {
				break
			}
			if retry < indeedMaxRetries-1 {
				delay := time.Duration(rand.Int63n(int64(maxIndeedDelay-minIndeedDelay))) + minIndeedDelay
				if debug {
					fmt.Printf("Retry %d: waiting %v before retry\n", retry+1, delay)
				}
				time.Sleep(delay)
			}
		}
		if err != nil {
			return nil, fmt.Errorf("failed to fetch page %d after %d retries: %v", page+1, indeedMaxRetries, err)
		}

		// Process each job listing
		for _, job := range jobData {
			title := job.Get("title").String()
			company := job.Get("company").String()
			location := job.Get("location").String()
			jobKey := job.Get("jobkey").String()
			
			// Check if company is in top paying list if filter is enabled
			if topPayOnly && !utils.IsTopPayingCompany(company, debug) {
				if debug {
					fmt.Printf("Skipping %s - not in top paying companies list\n", company)
				}
				continue
			}
			
			// Build job URL
			jobURL := fmt.Sprintf("%s/viewjob?jk=%s", indeedMobileURL, jobKey)

			// Extract salary information
			salary := "Not Available"
			if salarySnippet := job.Get("salary"); salarySnippet.Exists() {
				salary = salarySnippet.String()
			}

			// If no salary in the API response, try to get it from the job description
			if salary == "Not Available" {
				description := job.Get("snippet").String()
				if match := utils.FindSalaryInText(description); match != "" {
					salary = match
				}
			}

			jobInfo := models.SalaryInfo{
				Company:     company,
				Title:      title,
				Location:   location,
				URL:        jobURL,
				SalaryRange: salary,
				Source:     "indeed",
			}

			results = append(results, jobInfo)
			if debug {
				fmt.Printf("Added job: %s at %s (%s)\n", title, company, location)
			}
		}

		progress.FoundJobs = len(results)

		// Add delay between pages
		if page < pages-1 {
			delay := time.Duration(rand.Int63n(int64(maxIndeedDelay-minIndeedDelay))) + minIndeedDelay
			if debug {
				fmt.Printf("Waiting %v before next page\n", delay)
			}
			time.Sleep(delay)
		}
	}

	return results, nil
}

// fetchIndeedJobData fetches job data from Indeed's mobile API
func fetchIndeedJobData(httpClient *http.Client, searchURL string, debug bool) ([]gjson.Result, error) {
	req, err := http.NewRequest("GET", searchURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %v", err)
	}

	// Use Indeed mobile app headers
	headers := getIndeedHeaders()
	for key, values := range headers {
		req.Header[key] = values
	}

	// Add random query parameters
	q := req.URL.Query()
	q.Add("client", "ios")
	q.Add("v", "1276")
	q.Add("deviceid", generateDeviceToken())
	q.Add("t", strconv.FormatInt(time.Now().Unix(), 10))
	req.URL.RawQuery = q.Encode()

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch jobs: %v", err)
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
		fmt.Printf("Response body:\n%s\n", string(body))
	}

	// Parse the JSON response
	jsonData := gjson.Parse(string(body))
	results := jsonData.Get("results").Array()
	if len(results) == 0 {
		return nil, fmt.Errorf("no job results found in response")
	}

	return results, nil
}

// generateDeviceToken creates a random device token similar to Indeed's format
func generateDeviceToken() string {
	const chars = "abcdef0123456789"
	b := make([]byte, 32)
	for i := range b {
		b[i] = chars[rand.Intn(len(chars))]
	}
	return string(b)
} 