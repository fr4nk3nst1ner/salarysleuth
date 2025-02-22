package scraper

import (
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"
	"math/rand"
	"sync"
//	"regexp"

	"github.com/PuerkitoBio/goquery"
	"github.com/fr4nk3nst1ner/salarysleuth/internal/models"
	"github.com/fr4nk3nst1ner/salarysleuth/internal/client"
	"github.com/fr4nk3nst1ner/salarysleuth/internal/utils"
)

const (
	linkedinJobSearchURL = "https://www.linkedin.com/jobs-guest/jobs/api/seeMoreJobPostings/search"
	linkedinBaseURL     = "https://www.linkedin.com"
	levelsBaseURL      = "https://www.levels.fyi/js/salaryData.json"
	maxRetries         = 3
	minDelay          = 2 * time.Second
	maxDelay          = 4 * time.Second
	rateLimitDelay    = 15 * time.Second
	maxConcurrent     = 3
)

// getLinkedInHeaders returns headers that mimic a mobile browser
func getLinkedInHeaders(referer string) http.Header {
	headers := http.Header{}
	
	// Use mobile user agent
	headers.Set("User-Agent", client.MobileUserAgents[rand.Intn(len(client.MobileUserAgents))])
	
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
	
	// Add referer if provided
	if referer != "" {
		headers.Set("Referer", referer)
	}
	
	return headers
}

// ScrapeLinkedIn scrapes job listings from LinkedIn
func ScrapeLinkedIn(description, city, titleKeyword string, remoteOnly, internshipsOnly bool, pages int, debug bool, proxyURL string, progress *models.ScrapeProgress) ([]models.SalaryInfo, error) {
	// Create HTTP client with cookie support
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create cookie jar: %v", err)
	}
	
	httpClient := client.CreateProxyHTTPClient(proxyURL)
	httpClient.Jar = jar

	var results []models.SalaryInfo
	jobsPerPage := 25 // LinkedIn's default jobs per page
	resultsChan := make(chan []models.SalaryInfo, pages)
	errorsChan := make(chan error, pages)
	semaphore := make(chan struct{}, maxConcurrent)

	// Build base search parameters
	params := url.Values{}
	params.Add("keywords", description)
	if city != "" {
		params.Add("location", city)
	}
	if remoteOnly {
		params.Add("f_WT", "2") // LinkedIn's remote work filter
	}
	params.Add("position", "1")
	params.Add("pageNum", "0")
	params.Add("f_TPR", "") // Anytime 
	params.Add("f_E", "") // Experience level: All 
	params.Add("sortBy", "DD") // Sort by date

	var wg sync.WaitGroup
	for page := 0; page < pages; page++ {
		wg.Add(1)
		go func(pageNum int) {
			defer wg.Done()
			semaphore <- struct{}{} // Acquire semaphore
			defer func() { <-semaphore }() // Release semaphore

			// Add start parameter for pagination
			pageParams := params
			start := pageNum * jobsPerPage
			if start > 0 {
				pageParams.Set("start", fmt.Sprintf("%d", start))
			}

			searchURL := fmt.Sprintf("%s?%s", linkedinJobSearchURL, pageParams.Encode())
			if debug {
				fmt.Printf("Scraping page %d: %s\n", pageNum+1, searchURL)
			}

			// Retry logic for fetching the page
			var doc *goquery.Document
			var fetchErr error
			for retry := 0; retry < maxRetries; retry++ {
				if retry > 0 && debug {
					fmt.Printf("Retry %d for page %d\n", retry+1, pageNum+1)
				}

				// Add shorter random delay between requests
				delay := time.Duration(rand.Int63n(int64(maxDelay-minDelay))) + minDelay
				if debug {
					fmt.Printf("Waiting %v before request\n", delay)
				}
				time.Sleep(delay)

				doc, fetchErr = fetchPage(httpClient, searchURL, pageNum > 0, debug)
				if fetchErr == nil {
					break
				}

				if strings.Contains(fetchErr.Error(), "429") {
					if debug {
						fmt.Printf("Rate limited, waiting %v before retry\n", rateLimitDelay)
					}
					time.Sleep(rateLimitDelay)
					continue
				}
			}

			if fetchErr != nil {
				errorsChan <- fmt.Errorf("failed to fetch page %d after %d retries: %v", pageNum+1, maxRetries, fetchErr)
				return
			}

			// Find job listings
			var pageResults []models.SalaryInfo
			doc.Find("div.base-card").Each(func(i int, s *goquery.Selection) {
				// Skip blurred content
				if s.HasClass("blurred-content") || s.Parent().HasClass("blurred-content") || s.Find("div.blurred-content").Length() > 0 {
					if debug {
						fmt.Printf("Skipping blurred job card\n")
					}
					return
				}

				title := strings.TrimSpace(s.Find("h3.base-search-card__title").Text())
				company := strings.TrimSpace(s.Find("h4.base-search-card__subtitle").Text())
				location := strings.TrimSpace(s.Find("span.job-search-card__location").Text())
				jobURL, _ := s.Find("a.base-card__full-link").Attr("href")

				if !utils.IsValidJob(title, location, titleKeyword, remoteOnly, internshipsOnly) {
					return
				}

				// Clean up company name
				companyName := strings.Split(company, "\n")[0]
				companyName = strings.TrimSpace(companyName)

				// Look for salary information
				var salary string
				
				// Try the dedicated salary element first
				salaryElem := s.Find("span.job-search-card__salary-info")
				if salaryElem.Length() > 0 {
					salary = strings.TrimSpace(salaryElem.Text())
				}
				
				// If no salary found, try other sections
				if salary == "" {
					salary = findSalaryInJobCard(s)
				}
				
				// If no salary found, mark as not available
				if salary == "" {
					salary = "Not Available"
				}

				jobInfo := models.SalaryInfo{
					Company:     companyName,
					Title:      title,
					Location:   location,
					URL:        jobURL,
					SalaryRange: salary,
					Source:     "linkedin",
				}

				pageResults = append(pageResults, jobInfo)
				if debug {
					fmt.Printf("Added job: %s at %s (%s)\n", title, company, location)
				}
			})

			if len(pageResults) > 0 {
				resultsChan <- pageResults
			} else if debug {
				fmt.Printf("No jobs found on page %d\n", pageNum+1)
			}
		}(page)
	}

	// Wait for all goroutines to complete
	go func() {
		wg.Wait()
		close(resultsChan)
		close(errorsChan)
	}()

	// Collect results and errors
	for pageResults := range resultsChan {
		results = append(results, pageResults...)
		progress.FoundJobs = len(results)
	}

	// Check for errors
	var errors []error
	for err := range errorsChan {
		errors = append(errors, err)
	}

	if len(errors) > 0 {
		return results, fmt.Errorf("encountered errors while scraping: %v", errors)
	}

	return results, nil
}

// findSalaryInJobCard looks for salary information in various parts of the job card
func findSalaryInJobCard(s *goquery.Selection) string {
	// Check benefits section
	benefitsText := s.Find("div.job-posting-benefits").Text()
	if match := utils.FindSalaryInText(benefitsText); match != "" {
		return match
	}

	// Check metadata section
	metadataText := s.Find("div.base-search-card__metadata").Text()
	if match := utils.FindSalaryInText(metadataText); match != "" {
		return match
	}

	// Check description preview
	descText := s.Find("div.job-search-card__description").Text()
	if match := utils.FindSalaryInText(descText); match != "" {
		return match
	}

	return ""
}

// fetchPage fetches a single page with proper headers and referrer
func fetchPage(httpClient *http.Client, url string, addReferrer bool, debug bool) (*goquery.Document, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %v", err)
	}

	// Use mobile headers
	referer := ""
	if addReferrer {
		referer = linkedinJobSearchURL
	}
	headers := getLinkedInHeaders(referer)
	for key, values := range headers {
		req.Header[key] = values
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch page: %v", err)
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

	// Create a new reader with the same content for goquery
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(string(body)))
	if err != nil {
		return nil, fmt.Errorf("failed to parse HTML: %v", err)
	}

	return doc, nil
}

// fetchLevelsSalary fetches salary data from job description
func fetchLevelsSalary(company string, debug bool) string {
	// For now, we'll return an empty string since we can't reliably get salary data
	// TODO: Implement alternative salary data sources or premium API access
	return ""
}

// visitHomepage visits LinkedIn homepage to get initial cookies
func visitHomepage(httpClient *http.Client, debug bool) error {
	req, err := http.NewRequest("GET", linkedinBaseURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %v", err)
	}

	headers := client.GetRandomHeaders()
	for key, values := range headers {
		req.Header[key] = values
	}
	
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to fetch homepage: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("received non-200 status code from homepage: %d", resp.StatusCode)
	}

	// Read and parse the response body
	body, err := client.ReadResponseBody(resp)
	if err != nil {
		return fmt.Errorf("failed to read homepage body: %v", err)
	}

	if debug {
		fmt.Printf("Homepage response body:\n%s\n", string(body))
	}

	return nil
} 