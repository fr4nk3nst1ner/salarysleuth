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
	monsterBaseURL    = "https://www.monster.com"
	monsterSearchURL  = "https://www.monster.com/jobs/search"
	minMonsterDelay   = 3 * time.Second
	maxMonsterDelay   = 6 * time.Second
	monsterMaxRetries = 3
)

// ScrapeMonster scrapes job listings from Monster.com
// Note: Monster uses bot protection (CAPTCHA) which may block automated requests.
// This scraper attempts HTML parsing but may return empty results if blocked.
func ScrapeMonster(description string, pages int, debug bool, proxyURL string, progress *models.ScrapeProgress, topPayOnly bool) ([]models.SalaryInfo, error) {
	httpClient := client.CreateProxyHTTPClient(proxyURL)
	var results []models.SalaryInfo

	if debug {
		fmt.Printf("Searching Monster for jobs with description: %s\n", description)
		fmt.Println("Note: Monster uses bot protection - results may be limited")
	}

	// Format search query
	query := url.QueryEscape(description)

	for page := 1; page <= pages; page++ {
		// Build search URL
		searchURL := fmt.Sprintf("%s?q=%s&page=%d", monsterSearchURL, query, page)

		if debug {
			fmt.Printf("Fetching page %d: %s\n", page, searchURL)
		}

		// Get the search results page with retries
		var pageResults []models.SalaryInfo
		var err error

		for retry := 0; retry < monsterMaxRetries; retry++ {
			pageResults, err = fetchMonsterSearchPage(httpClient, searchURL, topPayOnly, debug)
			if err == nil {
				break
			}

			if strings.Contains(err.Error(), "blocked") || strings.Contains(err.Error(), "captcha") {
				if debug {
					fmt.Printf("Monster appears to be blocking requests (bot protection)\n")
				}
				// Return what we have so far instead of failing completely
				fmt.Println("Warning: Monster is using bot protection. Consider using other sources.")
				return results, nil
			}

			if retry < monsterMaxRetries-1 {
				delay := time.Duration(rand.Int63n(int64(maxMonsterDelay-minMonsterDelay))) + minMonsterDelay
				if debug {
					fmt.Printf("Retry %d: waiting %v before retry\n", retry+1, delay)
				}
				time.Sleep(delay)
			}
		}

		if err != nil {
			if debug {
				fmt.Printf("Error fetching page %d: %v\n", page, err)
			}
			continue
		}

		results = append(results, pageResults...)
		progress.FoundJobs = len(results)

		if debug {
			fmt.Printf("Found %d jobs on page %d (total: %d)\n", len(pageResults), page, len(results))
		}

		// Add delay between pages
		if page < pages {
			delay := time.Duration(rand.Int63n(int64(maxMonsterDelay-minMonsterDelay))) + minMonsterDelay
			if debug {
				fmt.Printf("Waiting %v before next page\n", delay)
			}
			time.Sleep(delay)
		}
	}

	return results, nil
}

// fetchMonsterSearchPage fetches and parses a Monster.com search page
func fetchMonsterSearchPage(httpClient *http.Client, searchURL string, topPayOnly bool, debug bool) ([]models.SalaryInfo, error) {
	req, err := http.NewRequest("GET", searchURL, nil)
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
		return nil, fmt.Errorf("failed to fetch page: %v", err)
	}
	defer resp.Body.Close()

	// Check for blocked requests
	if resp.StatusCode == 403 || resp.StatusCode == 503 {
		return nil, fmt.Errorf("blocked by bot protection (status: %d)", resp.StatusCode)
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

	// Check for bot protection/CAPTCHA indicators
	if strings.Contains(bodyStr, "Verification Required") ||
		strings.Contains(bodyStr, "captcha") ||
		strings.Contains(bodyStr, "unusual activity") ||
		strings.Contains(bodyStr, "Slide right to complete the puzzle") {
		return nil, fmt.Errorf("blocked by captcha (verification page)")
	}

	if debug {
		// Only print first 1000 chars to avoid overwhelming output
		preview := bodyStr
		if len(preview) > 1000 {
			preview = preview[:1000] + "..."
		}
		fmt.Printf("Monster response preview:\n%s\n", preview)
	}

	// Parse the HTML
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(bodyStr))
	if err != nil {
		return nil, fmt.Errorf("failed to parse HTML: %v", err)
	}

	var results []models.SalaryInfo

	// Monster uses various selectors for job cards - try multiple patterns
	jobCardSelectors := []string{
		"div[data-testid='svx_jobCard']",
		"div.job-cardstyle__JobCardComponent",
		"div.card-content",
		"div.flex-row",
		"article.job-cardstyle",
		"div.results-card",
	}

	for _, selector := range jobCardSelectors {
		doc.Find(selector).Each(func(i int, s *goquery.Selection) {
			// Extract job information using various possible selectors
			title := extractMonsterText(s, []string{
				"h2[data-testid='svx_jobCard-title']",
				"h2.title a",
				"h2.title",
				"a.job-cardstyle__jobTitleLink",
				"h3.job-title",
			})

			company := extractMonsterText(s, []string{
				"span[data-testid='svx_jobCard-companyName']",
				"div.company span",
				"div.company",
				"span.company-name",
				"a.company",
			})

			location := extractMonsterText(s, []string{
				"span[data-testid='svx_jobCard-location']",
				"div.location span",
				"div.location",
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
			if href, exists := s.Find("a[data-testid='svx_jobCard-title']").Attr("href"); exists {
				if strings.HasPrefix(href, "/") {
					jobURL = monsterBaseURL + href
				} else {
					jobURL = href
				}
			} else if href, exists := s.Find("h2.title a").Attr("href"); exists {
				if strings.HasPrefix(href, "/") {
					jobURL = monsterBaseURL + href
				} else {
					jobURL = href
				}
			} else if href, exists := s.Find("a").First().Attr("href"); exists {
				if strings.HasPrefix(href, "/") {
					jobURL = monsterBaseURL + href
				} else {
					jobURL = href
				}
			}

			// Extract salary information
			salary := extractMonsterText(s, []string{
				"span[data-testid='svx_jobCard-salary']",
				"div.job-salary",
				"div.salary span",
				"span.salary",
			})

			if salary == "" {
				salary = "Not Available"
			}

			// Also try to find salary in any visible text
			if salary == "Not Available" {
				cardText := s.Text()
				if match := utils.FindSalaryInText(cardText); match != "" {
					salary = match
				}
			}

			jobInfo := models.SalaryInfo{
				Company:     strings.TrimSpace(company),
				Title:       strings.TrimSpace(title),
				Location:    strings.TrimSpace(location),
				URL:         jobURL,
				SalaryRange: salary,
				Source:      "monster",
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

// extractMonsterText tries multiple selectors and returns the first non-empty text
func extractMonsterText(s *goquery.Selection, selectors []string) string {
	for _, selector := range selectors {
		if text := strings.TrimSpace(s.Find(selector).First().Text()); text != "" {
			return text
		}
	}
	return ""
}
