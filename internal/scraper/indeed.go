package scraper

import (
	"fmt"
	"math/rand"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/fr4nk3nst1ner/salarysleuth/internal/client"
	"github.com/fr4nk3nst1ner/salarysleuth/internal/models"
	"github.com/fr4nk3nst1ner/salarysleuth/internal/utils"
)

const (
	indeedBaseURL        = "https://www.indeed.com"
	indeedResultsPerPage = 10
	minIndeedDelay       = 3 * time.Second
	maxIndeedDelay       = 7 * time.Second
	indeedMaxRetries     = 3
)

// ScrapeIndeed scrapes job listings from Indeed.com
// Note: Indeed uses Cloudflare protection which may block automated requests.
// This scraper attempts HTML parsing but may return empty results if blocked.
func ScrapeIndeed(description string, pages int, debug bool, proxyURL string, progress *models.ScrapeProgress, topPayOnly bool) ([]models.SalaryInfo, error) {
	httpClient := client.CreateProxyHTTPClient(proxyURL)
	var results []models.SalaryInfo

	if debug {
		fmt.Printf("Searching Indeed for jobs with description: %s\n", description)
		fmt.Println("Note: Indeed uses Cloudflare protection - results may be limited")
	}

	// Format search query
	query := url.QueryEscape(description)

	for page := 0; page < pages; page++ {
		// Build search URL with pagination
		start := page * indeedResultsPerPage
		searchURL := fmt.Sprintf("%s/jobs?q=%s&l=&start=%d", indeedBaseURL, query, start)

		if debug {
			fmt.Printf("Fetching page %d: %s\n", page+1, searchURL)
		}

		// Get the search results with retries
		var pageResults []models.SalaryInfo
		var err error

		for retry := 0; retry < indeedMaxRetries; retry++ {
			pageResults, err = fetchIndeedPage(httpClient, searchURL, topPayOnly, debug)
			if err == nil {
				break
			}

			if strings.Contains(err.Error(), "cloudflare") || strings.Contains(err.Error(), "blocked") {
				if debug {
					fmt.Printf("Indeed appears to be blocking requests (Cloudflare protection)\n")
				}
				// Return what we have so far instead of failing completely
				fmt.Println("Warning: Indeed is using Cloudflare protection. Consider using other sources.")
				return results, nil
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
			if debug {
				fmt.Printf("Error fetching page %d: %v\n", page+1, err)
			}
			// Continue to next page instead of failing
			continue
		}

		results = append(results, pageResults...)
		progress.FoundJobs = len(results)

		if debug {
			fmt.Printf("Found %d jobs on page %d (total: %d)\n", len(pageResults), page+1, len(results))
		}

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

// fetchIndeedPage fetches and parses a single page of Indeed search results
func fetchIndeedPage(httpClient *http.Client, searchURL string, topPayOnly bool, debug bool) ([]models.SalaryInfo, error) {
	req, err := http.NewRequest("GET", searchURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %v", err)
	}

	// Use browser-like headers
	headers := client.GetRandomHeaders()
	for key, values := range headers {
		req.Header[key] = values
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch page: %v", err)
	}
	defer resp.Body.Close()

	// Check for Cloudflare protection
	if resp.StatusCode == 403 || resp.StatusCode == 503 {
		return nil, fmt.Errorf("blocked by cloudflare (status: %d)", resp.StatusCode)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("received non-200 status code: %d", resp.StatusCode)
	}

	// Read and parse the response body
	body, err := client.ReadResponseBody(resp)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %v", err)
	}

	bodyStr := string(body)

	// Check for Cloudflare challenge page indicators
	if strings.Contains(bodyStr, "Just a moment") ||
		strings.Contains(bodyStr, "Cloudflare") ||
		strings.Contains(bodyStr, "cf-browser-verification") ||
		strings.Contains(bodyStr, "Additional Verification Required") {
		return nil, fmt.Errorf("blocked by cloudflare (challenge page)")
	}

	if debug {
		// Only print first 1000 chars to avoid overwhelming output
		preview := bodyStr
		if len(preview) > 1000 {
			preview = preview[:1000] + "..."
		}
		fmt.Printf("Indeed response preview:\n%s\n", preview)
	}

	// Parse the HTML
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(bodyStr))
	if err != nil {
		return nil, fmt.Errorf("failed to parse HTML: %v", err)
	}

	var results []models.SalaryInfo

	// Indeed uses various selectors for job cards - try multiple patterns
	jobCardSelectors := []string{
		"div.job_seen_beacon",
		"div.jobsearch-ResultsList div.cardOutline",
		"div[data-jk]",
		"div.result",
		"td.resultContent",
	}

	for _, selector := range jobCardSelectors {
		doc.Find(selector).Each(func(i int, s *goquery.Selection) {
			// Extract job information using various possible selectors
			title := extractIndeedText(s, []string{
				"h2.jobTitle span[title]",
				"h2.jobTitle a",
				"h2.jobTitle",
				"a.jcs-JobTitle span",
				"a.jcs-JobTitle",
			})

			company := extractIndeedText(s, []string{
				"span.companyName",
				"span[data-testid='company-name']",
				"div.companyInfo span.companyName",
				"span.company",
			})

			location := extractIndeedText(s, []string{
				"div.companyLocation",
				"div[data-testid='text-location']",
				"span.companyLocation",
				"span.location",
			})

			// Skip if essential fields are missing
			if title == "" || company == "" {
				return
			}

			// Check if company is in top paying list if filter is enabled
			if topPayOnly && !utils.IsTopPayingCompany(company, debug) {
				return
			}

			// Extract job URL
			jobURL := ""
			if href, exists := s.Find("a.jcs-JobTitle").Attr("href"); exists {
				jobURL = indeedBaseURL + href
			} else if href, exists := s.Find("h2.jobTitle a").Attr("href"); exists {
				jobURL = indeedBaseURL + href
			} else if jk, exists := s.Attr("data-jk"); exists {
				jobURL = fmt.Sprintf("%s/viewjob?jk=%s", indeedBaseURL, jk)
			}

			// Extract salary information
			salary := extractIndeedText(s, []string{
				"div.salary-snippet-container",
				"div[data-testid='attribute_snippet_testid']",
				"span.salaryText",
				"div.metadata.salary-snippet-container",
			})

			if salary == "" {
				salary = "Not Available"
			}

			// Also try to find salary in the job snippet
			if salary == "Not Available" {
				snippet := extractIndeedText(s, []string{
					"div.job-snippet",
					"div.job-snippet ul",
					"table.jobCardShelfContainer",
				})
				if match := utils.FindSalaryInText(snippet); match != "" {
					salary = match
				}
			}

			jobInfo := models.SalaryInfo{
				Company:     strings.TrimSpace(company),
				Title:       strings.TrimSpace(title),
				Location:    strings.TrimSpace(location),
				URL:         jobURL,
				SalaryRange: salary,
				Source:      "indeed",
			}

			results = append(results, jobInfo)
			if debug {
				fmt.Printf("Added job: %s at %s (%s)\n", title, company, location)
			}
		})

		// If we found results with this selector, don't try others
		if len(results) > 0 {
			break
		}
	}

	return results, nil
}

// extractIndeedText tries multiple selectors and returns the first non-empty text
func extractIndeedText(s *goquery.Selection, selectors []string) string {
	for _, selector := range selectors {
		if text := strings.TrimSpace(s.Find(selector).First().Text()); text != "" {
			return text
		}
	}
	return ""
}
