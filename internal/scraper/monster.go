package scraper

import (
	"fmt"
	"net/http"
	"strings"
	"time"
	"math/rand"
	//"regexp"
	"net/url"

	"github.com/PuerkitoBio/goquery"
	"github.com/fr4nk3nst1ner/salarysleuth/internal/models"
	"github.com/fr4nk3nst1ner/salarysleuth/internal/client"
	"github.com/fr4nk3nst1ner/salarysleuth/internal/utils"
)

const (
	monsterBaseURL = "https://www.monster.com"
	monsterSearchURL = "https://www.monster.com/jobs/search"
	maxResultsPerPage = 25
	minMonsterDelay = 2 * time.Second
	maxMonsterDelay = 5 * time.Second
)

// ScrapeMonster scrapes job listings from Monster.com
func ScrapeMonster(description string, pages int, debug bool, proxyURL string, progress *models.ScrapeProgress, topPayOnly bool) ([]models.SalaryInfo, error) {
	httpClient := client.CreateProxyHTTPClient(proxyURL)
	var results []models.SalaryInfo

	if debug {
		fmt.Printf("Searching for jobs with description: %s\n", description)
	}

	// Format search query
	query := strings.ReplaceAll(description, " ", "-")

	for page := 1; page <= pages; page++ {
		// Build search URL
		searchURL := fmt.Sprintf("%s?q=%s&page=%d", monsterSearchURL, url.QueryEscape(query), page)
		
		if debug {
			fmt.Printf("Fetching page %d: %s\n", page, searchURL)
		}

		// Get the search results page
		doc, err := fetchMonsterPage(httpClient, searchURL, debug)
		if err != nil {
			if debug {
				fmt.Printf("Error fetching page %d: %v\n", page, err)
			}
			continue
		}

		// Find all job listings on the page
		doc.Find("div.flex-row").Each(func(i int, s *goquery.Selection) {
			// Extract job information
			title := strings.TrimSpace(s.Find("h2.title").Text())
			company := strings.TrimSpace(s.Find("div.company").Text())
			location := strings.TrimSpace(s.Find("div.location").Text())
			jobURL, _ := s.Find("a[data-bypass='true']").Attr("href")
			
			// Check if company is in top paying list if filter is enabled
			if topPayOnly && !utils.IsTopPayingCompany(company, debug) {
				if debug {
					fmt.Printf("Skipping %s - not in top paying companies list\n", company)
				}
				return
			}
			
			// Extract salary information
			salary := "Not Available"
			salaryElem := s.Find("div.job-salary")
			if salaryElem.Length() > 0 {
				salary = strings.TrimSpace(salaryElem.Text())
			}

			// If no salary in the card, try to get it from the job description
			if salary == "Not Available" && jobURL != "" {
				if desc, err := fetchJobDescription(httpClient, jobURL, debug); err == nil {
					if match := utils.FindSalaryInText(desc); match != "" {
						salary = match
					}
				}
			}

			jobInfo := models.SalaryInfo{
				Company:     company,
				Title:      title,
				Location:   location,
				URL:        jobURL,
				SalaryRange: salary,
				Source:     "monster",
			}

			results = append(results, jobInfo)
			if debug {
				fmt.Printf("Added job: %s at %s (%s)\n", title, company, location)
			}
		})

		progress.FoundJobs = len(results)

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

// fetchMonsterPage fetches and parses a Monster.com page
func fetchMonsterPage(httpClient *http.Client, url string, debug bool) (*goquery.Document, error) {
	req, err := http.NewRequest("GET", url, nil)
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

	// Parse the HTML
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(string(body)))
	if err != nil {
		return nil, fmt.Errorf("failed to parse HTML: %v", err)
	}

	return doc, nil
}

// fetchJobDescription fetches the full job description from a job's URL
func fetchJobDescription(httpClient *http.Client, jobURL string, debug bool) (string, error) {
	doc, err := fetchMonsterPage(httpClient, jobURL, debug)
	if err != nil {
		return "", err
	}

	description := doc.Find("div#JobDescription").Text()
	return strings.TrimSpace(description), nil
} 