package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"os"
	"os/exec"

	"math/rand"
	"time"
//	"math"

	"github.com/pterm/pterm"
	"github.com/PuerkitoBio/goquery"
	"github.com/cheggaaa/pb/v3"
    "github.com/dustin/go-humanize"
	"crypto/tls"
)

const bannerText = `
▄▄███▄▄· █████╗ ██╗      █████╗ ██████╗ ██╗   ██╗    ▄▄███▄▄·██╗     ███████╗██╗   ██╗████████╗██╗  ██╗
██╔════╝██╔══██╗██║     ██╔══██╗██╔══██╗╚██╗ ██╔╝    ██╔════╝██║     ██╔════╝██║   ██║╚══██╔══╝██║  ██║
███████╗███████║██║     ███████║██████╔╝ ╚████╔╝     ███████╗██║     █████╗  ██║   ██║   ██║   ███████║
╚════██║██╔══██║██║     ██╔══██║██╔══██╗  ╚██╔╝      ╚════██║██║     ██╔══╝  ██║   ██║   ██║   ██╔══██║
███████║██║  ██║███████╗██║  ██║██║  ██║   ██║       ███████║███████╗███████╗╚██████╔╝   ██║   ██║  ██║
╚═▀▀▀══╝╚═╝  ╚═╝╚══════╝╚═╝  ╚═╝╚═╝  ╚═╝   ╚═╝       ╚═▀▀▀══╝╚══════╝╚══════╝ ╚═════╝    ╚═╝   ╚═╝  ╚═╝
 @fr4nk3nst1ner                                                                                                        
`

const (
	maxRetries = 5
	minDelay   = 200 * time.Millisecond
	maxDelay   = 500 * time.Millisecond
	timeout    = 10 * time.Second
	maxWorkers = 5
)

func colorizeText(text string) string {
	source := rand.NewSource(time.Now().UnixNano())
	random := rand.New(source)

	startColor := pterm.NewRGB(uint8(random.Intn(256)), uint8(random.Intn(256)), uint8(random.Intn(256)))
	firstPoint := pterm.NewRGB(uint8(random.Intn(256)), uint8(random.Intn(256)), uint8(random.Intn(256)))

	strs := strings.Split(text, "")

	var coloredText string
	for i := 0; i < len(text); i++ {
		if i < len(strs) {
			coloredText += startColor.Fade(0, float32(len(text)), float32(i%(len(text)/2)), firstPoint).Sprint(strs[i])
		}
	}

	return coloredText
}

func printBanner(silence bool) {
	if !silence {
		coloredBanner := colorizeText(bannerText)
		fmt.Println(coloredBanner)
	}
}

type SalaryInfo struct {
	Company     string
	Title       string
	Location    string
	URL         string
	SalaryRange string
	LevelSalary string
	Source      string
}

type Salary struct {
	BaseSalary struct {
		Value struct {
			MinValue float64 `json:"minValue"`
			MaxValue float64 `json:"maxValue"`
		} `json:"value"`
	} `json:"baseSalary"`
}

const userAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36"

func createProxyHTTPClient(proxyURL string) *http.Client {
	// Parse the proxy URL
	proxy, err := url.Parse(proxyURL)
	if err != nil {
		log.Printf("Error parsing proxy URL: %v", err)
		return createHTTPClient("") // Fallback to regular client
	}

	// Create transport with proxy and TLS config that skips verification
	transport := &http.Transport{
		Proxy: http.ProxyURL(proxy),
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true, // Skip certificate verification
		},
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 100,
		IdleConnTimeout:     90 * time.Second,
	}

	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
	}
}

func createHTTPClient(proxyURL string) *http.Client {
	if proxyURL != "" {
		return createProxyHTTPClient(proxyURL)
	}

	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			MaxIdleConns:        100,
			MaxIdleConnsPerHost: 100,
			IdleConnTimeout:     90 * time.Second,
		},
	}
}

func extractSalaryFromJobPage(jobURL string, debug bool, proxyURL string) (string, error) {
	client := createHTTPClient(proxyURL)
	
	for attempt := 0; attempt < maxRetries; attempt++ {
		// Add random delay between requests
		delay := time.Duration(rand.Float64()*(maxDelay.Seconds()-minDelay.Seconds())+minDelay.Seconds()) * time.Second
		time.Sleep(delay)
		
		req, err := http.NewRequest("GET", jobURL, nil)
		if err != nil {
			continue
		}
		
		// Rotate user agents
		req.Header.Set("User-Agent", getRandomUserAgent())
		req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/webp,*/*;q=0.8")
		req.Header.Set("Accept-Language", "en-US,en;q=0.5")
		req.Header.Set("Connection", "keep-alive")
		
		resp, err := client.Do(req)
		if err != nil {
			if attempt == maxRetries-1 {
				return "", err
			}
			continue
		}
		defer resp.Body.Close()
		
		// Check if we're being rate limited
		if resp.StatusCode == 429 || resp.StatusCode == 403 {
			if debug {
				log.Printf("Rate limited (attempt %d/%d). Waiting before retry...", attempt+1, maxRetries)
			}
			time.Sleep(delay * 2) // Now delay is defined
			continue
		}
		
		doc, err := goquery.NewDocumentFromReader(resp.Body)
		if err != nil {
			continue
		}
		
		// Extract salary range directly from the div with class "salary compensation__salary"
		var salaryText string
		doc.Find("div.salary.compensation__salary").Each(func(i int, s *goquery.Selection) {
			salaryText = strings.TrimSpace(s.Text())
			if debug {
				fmt.Printf("Extracted Salary Text: %s\n", salaryText)
			}
		})

		// Fallback to extracting salary from JSON-LD script tag if not found in the direct salary div
		if salaryText == "" {
			doc.Find("script[type='application/ld+json']").Each(func(i int, s *goquery.Selection) {
				jsonContent := s.Text()

				if strings.Contains(jsonContent, "baseSalary") {
					if debug {
						fmt.Println("Extracted JSON-LD content:", jsonContent)
					}
					var salaryData Salary
					if err := json.Unmarshal([]byte(jsonContent), &salaryData); err != nil {
						log.Println("Error parsing JSON:", err)
						return
					}

					salaryText = fmt.Sprintf("$%.2f - $%.2f", salaryData.BaseSalary.Value.MinValue, salaryData.BaseSalary.Value.MaxValue)
				}
			})
		}

		if salaryText != "" {
			return salaryText, nil
		}
	}
	
	return "Not specified", nil
}

type LevelsAPIResponse struct {
	PageProps struct {
		Percentiles struct {
			Tc struct {
				P50 float64 `json:"p50"`
			} `json:"tc"`
		} `json:"percentiles"`
	} `json:"pageProps"`
}

func getSalaryFromLevelsFyi(companyName string, proxyURL string) (string, error) {
	// Format company name: lowercase and replace spaces with hyphens
	formattedCompany := strings.ToLower(strings.ReplaceAll(companyName, " ", "-"))
	
	// Construct the URL for the levels.fyi company data
	baseURL := "https://www.levels.fyi/_next/data/nUglqYzRFa6Ao7RJX3blx/companies"
	apiURL := fmt.Sprintf("%s/%s/salaries/software-engineer.json?company=%s&job-family=software-engineer", 
		baseURL, formattedCompany, formattedCompany)

	// Create request
	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return "", err
	}

	// Set headers
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Origin", "https://www.levels.fyi")
	req.Header.Set("Referer", "https://www.levels.fyi/")

	// Make request
	client := createHTTPClient(proxyURL)
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	// Check status code
	if resp.StatusCode != http.StatusOK {
		return "No Data", nil
	}

	// Parse response
	var apiResp LevelsAPIResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return "", err
	}

	// Get median total compensation
	medianTC := apiResp.PageProps.Percentiles.Tc.P50
	if medianTC <= 0 {
		return "No Data", nil
	}

	// Format the salary with comma separators
	return fmt.Sprintf("$%s", humanize.Comma(int64(medianTC))), nil
}

func processBatchJobs(jobs []SalaryInfo, debug bool, bar *pb.ProgressBar) {
	semaphore := make(chan struct{}, maxWorkers)
	var wg sync.WaitGroup

	for i := range jobs {
		wg.Add(1)
		go func(job *SalaryInfo) {
			defer wg.Done()
			semaphore <- struct{}{} // Acquire semaphore
			defer func() {
				<-semaphore // Release semaphore
				bar.Increment()
			}()

			// Extract salary from LinkedIn
			salary, err := extractSalaryFromJobPage(job.URL, debug, "")
			if err == nil && salary != "" {
				job.SalaryRange = salary
			} else {
				job.SalaryRange = "Not specified"
			}

			// Cross-reference with levels.fyi
			levelSalary, err := getSalaryFromLevelsFyi(strings.ToLower(strings.ReplaceAll(job.Company, " ", "-")), "")
			if err == nil && levelSalary != "" {
				job.LevelSalary = levelSalary
			} else {
				job.LevelSalary = "No Data"
			}
		}(&jobs[i])
	}

	wg.Wait()
}

type ScrapeProgress struct {
	PageBar    *pb.ProgressBar
	JobBar     *pb.ProgressBar
	TotalJobs  int
	FoundJobs  int
	mu         sync.Mutex
}

func constructGoogleSearchURL(description string) string {
	// Format the search query similar to the Python script
	searchQuery := url.QueryEscape(fmt.Sprintf("site:lever.co OR site:greenhouse.io %s", description))
	return fmt.Sprintf("https://www.google.com/search?q=%s", searchQuery)
}

func extractJobURLs(doc *goquery.Document) []string {
	var urls []string
	doc.Find("a").Each(func(i int, s *goquery.Selection) {
		href, exists := s.Attr("href")
		if !exists {
			return
		}

		// Extract actual URL from Google's redirect URL
		if strings.HasPrefix(href, "/url?q=") {
			href = strings.Split(href, "&")[0]
			href = strings.TrimPrefix(href, "/url?q=")
			
			// Only include lever.co and greenhouse.io URLs
			if strings.Contains(href, "lever.co") || strings.Contains(href, "greenhouse.io") {
				decodedURL, err := url.QueryUnescape(href)
				if err == nil {
					urls = append(urls, decodedURL)
				}
			}
		}
	})
	return urls
}

func scrapeJobBoard(jobURL string, debug bool, proxyURL string) (*SalaryInfo, error) {
	// Clean up the URL to ensure we're using the direct boards.greenhouse.io URL
	if !strings.Contains(jobURL, "boards.greenhouse.io") {
		// Extract job ID from URL
		re := regexp.MustCompile(`(?:gh_jid=|jobs/|job_app\?.*token=)(\d+)`)
		matches := re.FindStringSubmatch(jobURL)
		if len(matches) > 1 {
			jobID := matches[1]
			// Try to find the company name from the URL
			companyRe := regexp.MustCompile(`(?:greenhouse\.io/|boards\.greenhouse\.io/)([^/]+)`)
			companyMatches := companyRe.FindStringSubmatch(jobURL)
			if len(companyMatches) > 1 {
				company := companyMatches[1]
				jobURL = fmt.Sprintf("https://boards.greenhouse.io/%s/jobs/%s", company, jobID)
			}
		}
	}

	if debug {
		log.Printf("Scraping job page: %s", jobURL)
	}

	client := createHTTPClient(proxyURL)
	req, err := http.NewRequest("GET", jobURL, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("User-Agent", getRandomUserAgent())
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/webp,*/*;q=0.8")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// Handle redirects
	if resp.StatusCode == http.StatusFound || resp.StatusCode == http.StatusMovedPermanently {
		redirectURL := resp.Header.Get("Location")
		if debug {
			log.Printf("Following redirect to: %s", redirectURL)
		}

		// Create new request for redirect URL
		redirectReq, err := http.NewRequest("GET", redirectURL, nil)
		if err != nil {
			return nil, err
		}
		redirectReq.Header = req.Header

		// Make request to redirect URL
		redirectResp, err := client.Do(redirectReq)
		if err != nil {
			return nil, err
		}
		defer redirectResp.Body.Close()

		if redirectResp.StatusCode != http.StatusOK {
			if debug {
				log.Printf("Got status code %d for redirect URL: %s", redirectResp.StatusCode, redirectURL)
			}
			return nil, fmt.Errorf("status code: %d", redirectResp.StatusCode)
		}

		resp = redirectResp
	} else if resp.StatusCode != http.StatusOK {
		if debug {
			log.Printf("Got status code %d for URL: %s", resp.StatusCode, jobURL)
		}
		return nil, fmt.Errorf("status code: %d", resp.StatusCode)
	}

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return nil, err
	}

	var jobInfo SalaryInfo
	jobInfo.URL = jobURL
	jobInfo.Source = "greenhouse"

	// Extract company name from URL
	if strings.Contains(jobURL, "greenhouse.io") {
		parts := strings.Split(jobURL, "greenhouse.io/")
		if len(parts) > 1 {
			companyPath := strings.Split(parts[1], "/")[0]
			jobInfo.Company = strings.ReplaceAll(companyPath, "-", " ")
		}
	}

	// Enhanced job title extraction with multiple selectors
	titleSelectors := []string{
		"h1.app-title",
		"h1.job-title",
		"h1#gh-job-title",
		"div.heading h1",
		"div.job-header h1",
		"div#header h1",
		"div#content h1",
		"meta[property='og:title']",
		"div.opening-title",      // Added more selectors
		"div.job-title",
		"h1.position-title",
	}

	for _, selector := range titleSelectors {
		var title string
		if strings.HasPrefix(selector, "meta") {
			title, _ = doc.Find(selector).Attr("content")
		} else {
			title = doc.Find(selector).First().Text()
		}
		title = strings.TrimSpace(title)
		if title != "" {
			jobInfo.Title = title
			break
		}
	}

	// Enhanced location extraction with multiple selectors
	locationSelectors := []string{
		"div.location",
		"div.job-location",
		"span.location",
		"div.company-location",
		"div#location",
		"meta[name='job-location']",
		"div.job-info div:contains('Location')",
		"div.section-wrapper div:contains('Location')",
	}

	for _, selector := range locationSelectors {
		var location string
		if strings.HasPrefix(selector, "meta") {
			location, _ = doc.Find(selector).Attr("content")
		} else {
			location = doc.Find(selector).First().Text()
		}
		location = strings.TrimSpace(location)
		location = strings.TrimPrefix(location, "Location")
		location = strings.TrimPrefix(location, ":")
		location = strings.TrimSpace(location)
		
		if location != "" {
			jobInfo.Location = location
			break
		}
	}

	if debug {
		log.Printf("Extracted job info:\nTitle: %q\nCompany: %q\nLocation: %q\nSource: %s\nURL: %s\n",
			jobInfo.Title, jobInfo.Company, jobInfo.Location, jobInfo.Source, jobInfo.URL)
	}

	return &jobInfo, nil
}

func normalizeGreenhouseURL(jobURL string) string {
	// Extract job ID and company from various Greenhouse URL formats
	var jobID, company string
	
	// Match patterns like "jobs/1234567" or "gh_jid=1234567"
	jobIDRegex := regexp.MustCompile(`(?:jobs/|gh_jid=)(\d+)`)
	jobMatch := jobIDRegex.FindStringSubmatch(jobURL)
	
	// Match company name from URL
	companyRegex := regexp.MustCompile(`(?:boards\.greenhouse\.io/|job-boards\.greenhouse\.io/|careers\.)([^/\?]+)`)
	companyMatch := companyRegex.FindStringSubmatch(jobURL)
	
	if len(jobMatch) > 1 {
		jobID = jobMatch[1]
	}
	
	if len(companyMatch) > 1 {
		company = companyMatch[1]
	}
	
	// If we have both company and job ID, construct the canonical URL
	if company != "" && jobID != "" {
		return fmt.Sprintf("https://boards.greenhouse.io/%s/jobs/%s", company, jobID)
	}
	
	return jobURL
}

func getJobURLs(description string, pages int, debug bool, proxyURL string, source string) ([]string, error) {
	// Construct the query based on the source
	var query string
	if source == "" {
		query = fmt.Sprintf("site:lever.co OR site:greenhouse.io %s", description)
	} else if source == "greenhouse" {
		query = fmt.Sprintf("site:greenhouse.io %s", description)
	} else if source == "lever" {
		query = fmt.Sprintf("site:lever.co %s", description)
	}
	
	// Create command with base arguments
	args := []string{"-e", "google", "-p", fmt.Sprintf("%d", pages), "-s", "-q", query}
	
	// Add proxy argument if provided
	if proxyURL != "" {
		args = append(args, "-x", proxyURL)
	}
	
	cmd := exec.Command("go-dork", args...)

	if debug {
		log.Printf("Running command: go-dork %s", strings.Join(args, " "))
	}

	// Capture the output
	output, err := cmd.CombinedOutput()
	if err != nil {
		if debug {
			log.Printf("go-dork error: %v\nOutput: %s", err, string(output))
		}
		return nil, err
	}

	// Extract URLs using regex based on source
	var re *regexp.Regexp
	if source == "" {
		re = regexp.MustCompile(`https?://[^\s"]+(?:greenhouse\.io|lever\.co)[^\s"]*`)
	} else if source == "greenhouse" {
		re = regexp.MustCompile(`https?://[^\s"]+greenhouse\.io[^\s"]*(?:jobs/\d+|gh_jid=\d+)[^\s"]*`)
	} else if source == "lever" {
		re = regexp.MustCompile(`https?://jobs\.lever\.co/[^/]+/[a-f0-9-]+(?:\?[^"\s]*)?`)
	}
	
	urls := re.FindAllString(string(output), -1)
	
	// Normalize and deduplicate URLs
	seen := make(map[string]bool)
	var normalizedURLs []string
	
	for _, url := range urls {
		if source == "greenhouse" || (source == "" && strings.Contains(url, "greenhouse.io")) {
			normalizedURL := normalizeGreenhouseURL(url)
			if !seen[normalizedURL] {
				seen[normalizedURL] = true
				normalizedURLs = append(normalizedURLs, normalizedURL)
			}
		} else if source == "lever" || (source == "" && strings.Contains(url, "lever.co")) {
			if !seen[url] {
				seen[url] = true
				normalizedURLs = append(normalizedURLs, url)
			}
		}
	}

	if debug {
		log.Printf("Found %d unique URLs from go-dork for source %s", len(normalizedURLs), source)
		for i, url := range normalizedURLs {
			log.Printf("URL %d: %s", i+1, url)
		}
	}

	return normalizedURLs, nil
}

func scrapeGreenhouse(description string, pages int, debug bool, proxyURL string, progress *ScrapeProgress) ([]SalaryInfo, error) {
	var allJobs []SalaryInfo
	var jobsMutex sync.Mutex

	// Get job URLs from go-dork
	jobURLs, err := getJobURLs(description, pages, debug, proxyURL, "greenhouse")
	if err != nil {
		if debug {
			log.Printf("Error getting Greenhouse job URLs: %v", err)
		}
		return nil, err
	}

	// Create a channel for job processing
	jobChan := make(chan string, len(jobURLs))
	for _, url := range jobURLs {
		jobChan <- url
	}
	close(jobChan)

	// Process jobs concurrently with limited workers
	var jobWg sync.WaitGroup
	jobWorkers := 5
	for i := 0; i < jobWorkers; i++ {
		jobWg.Add(1)
		go func() {
			defer jobWg.Done()
			for jobURL := range jobChan {
				if debug {
					log.Printf("Processing Greenhouse job URL: %s", jobURL)
				}

				jobInfo, err := scrapeJobBoard(jobURL, debug, proxyURL)
				if err != nil {
					if debug {
						log.Printf("Error scraping Greenhouse job %s: %v", jobURL, err)
					}
					continue
				}

				if jobInfo != nil {
					jobsMutex.Lock()
					allJobs = append(allJobs, *jobInfo)
					progress.mu.Lock()
					progress.FoundJobs++
					progress.JobBar.SetTotal(int64(progress.FoundJobs))
					progress.mu.Unlock()
					jobsMutex.Unlock()
				}
			}
		}()
	}

	jobWg.Wait()
	progress.PageBar.Increment()

	return allJobs, nil
}

func scrapeLever(description string, pages int, debug bool, proxyURL string, progress *ScrapeProgress) ([]SalaryInfo, error) {
	var allJobs []SalaryInfo
	var jobsMutex sync.Mutex

	// Get job URLs from go-dork
	jobURLs, err := getJobURLs(description, pages, debug, proxyURL, "lever")
	if err != nil {
		if debug {
			log.Printf("Error getting Lever job URLs: %v", err)
		}
		return nil, err
	}

	// Create a channel for job processing
	jobChan := make(chan string, len(jobURLs))
	for _, url := range jobURLs {
		// Only process actual job listings
		if strings.Contains(url, "jobs.lever.co") && strings.Count(url, "/") >= 4 {
			jobChan <- url
		}
	}
	close(jobChan)

	// Process jobs concurrently with limited workers
	var jobWg sync.WaitGroup
	jobWorkers := 5
	for i := 0; i < jobWorkers; i++ {
		jobWg.Add(1)
		go func() {
			defer jobWg.Done()
			for jobURL := range jobChan {
				if debug {
					log.Printf("Processing Lever job URL: %s", jobURL)
				}

				client := createHTTPClient(proxyURL)
				req, err := http.NewRequest("GET", jobURL, nil)
				if err != nil {
					if debug {
						log.Printf("Error creating request for %s: %v", jobURL, err)
					}
					continue
				}

				req.Header.Set("User-Agent", getRandomUserAgent())
				req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/webp,*/*;q=0.8")

				resp, err := client.Do(req)
				if err != nil {
					if debug {
						log.Printf("Error fetching %s: %v", jobURL, err)
					}
					continue
				}
				defer resp.Body.Close()

				if resp.StatusCode != http.StatusOK {
					if debug {
						log.Printf("Got status %d for %s", resp.StatusCode, jobURL)
					}
					continue
				}

				doc, err := goquery.NewDocumentFromReader(resp.Body)
				if err != nil {
					if debug {
						log.Printf("Error parsing HTML for %s: %v", jobURL, err)
					}
					continue
				}

				var jobInfo SalaryInfo
				jobInfo.URL = jobURL
				jobInfo.Source = "lever"

				// Extract company name from URL
				parts := strings.Split(jobURL, "jobs.lever.co/")
				if len(parts) > 1 {
					companyPath := strings.Split(parts[1], "/")[0]
					jobInfo.Company = strings.ReplaceAll(companyPath, "-", " ")
				}

				// Extract job title
				jobInfo.Title = strings.TrimSpace(doc.Find("div.posting-headline h2").Text())

				// Extract location
				jobInfo.Location = strings.TrimSpace(doc.Find("div.posting-categories .location").Text())

				// Only add job if we got a title
				if jobInfo.Title != "" {
					jobsMutex.Lock()
					allJobs = append(allJobs, jobInfo)
					progress.mu.Lock()
					progress.FoundJobs++
					progress.JobBar.SetTotal(int64(progress.FoundJobs))
					progress.mu.Unlock()
					jobsMutex.Unlock()
				}
			}
		}()
	}

	jobWg.Wait()
	progress.PageBar.Increment()

	return allJobs, nil
}

func scrapeLinkedIn(description, city, titleKeyword string, remoteOnly bool, internshipsOnly bool, pages int, debug bool, proxyURL string, source string) ([]SalaryInfo, error) {
	var allJobs []SalaryInfo
	var jobsMutex sync.Mutex
	
	// Create progress tracking with appropriate number of pages
	totalPages := pages
	if source == "" {
		totalPages *= 3 // LinkedIn, Greenhouse, and Lever
	} else {
		totalPages *= 1 // Only one source
	}
	
	progress := &ScrapeProgress{
		PageBar: pb.New(totalPages),
		JobBar:  pb.New(0),
	}
	
	pool, err := pb.StartPool(progress.PageBar, progress.JobBar)
	if err != nil {
		return nil, fmt.Errorf("failed to create progress bars: %v", err)
	}
	defer pool.Stop()

	var wg sync.WaitGroup

	// Scrape LinkedIn jobs if requested
	if source == "" || source == "linkedin" {
		wg.Add(1)
		go func() {
			defer wg.Done()
			linkedInJobs, err := scrapeLinkedInPages(description, city, titleKeyword, remoteOnly, internshipsOnly, pages, debug, progress, proxyURL)
			if err == nil && len(linkedInJobs) > 0 {
				jobsMutex.Lock()
				allJobs = append(allJobs, linkedInJobs...)
				jobsMutex.Unlock()
			}
		}()
	}

	// Scrape Greenhouse jobs if requested
	if source == "" || source == "greenhouse" {
		wg.Add(1)
		go func() {
			defer wg.Done()
			greenhouseJobs, err := scrapeGreenhouse(description, pages, debug, proxyURL, progress)
			if err == nil && len(greenhouseJobs) > 0 {
				jobsMutex.Lock()
				allJobs = append(allJobs, greenhouseJobs...)
				jobsMutex.Unlock()
			}
		}()
	}

	// Scrape Lever jobs if requested
	if source == "" || source == "lever" {
		wg.Add(1)
		go func() {
			defer wg.Done()
			leverJobs, err := scrapeLever(description, pages, debug, proxyURL, progress)
			if err == nil && len(leverJobs) > 0 {
				jobsMutex.Lock()
				allJobs = append(allJobs, leverJobs...)
				jobsMutex.Unlock()
			}
		}()
	}

	wg.Wait()

	if len(allJobs) > 0 {
		processBatchJobs(allJobs, debug, progress.JobBar)
	}

	return allJobs, nil
}

func addRandomQueryParams(baseURL string) string {
	params := []string{
		fmt.Sprintf("trk=%s", randomString(8)),
		fmt.Sprintf("sessionId=%s", randomString(16)),
		fmt.Sprintf("geoId=%d", rand.Intn(100000)),
	}
	if strings.Contains(baseURL, "?") {
		return baseURL + "&" + strings.Join(params, "&")
	}
	return baseURL + "?" + strings.Join(params, "&")
}

func randomString(n int) string {
	const letters = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, n)
	for i := range b {
		b[i] = letters[rand.Intn(len(letters))]
	}
	return string(b)
}

func scrapeLinkedInPage(pageNum int, description, city, titleKeyword string, remoteOnly, internshipsOnly, debug bool, proxyURL string) []SalaryInfo {
	var pageJobs []SalaryInfo
	searchTerm := description
	if description == "" {
		searchTerm = titleKeyword
	}

	descriptionEncoded := url.QueryEscape(searchTerm)
	cityEncoded := url.QueryEscape(city)

	baseURL := fmt.Sprintf("https://www.linkedin.com/jobs/search?keywords=%s&location=%s&pageNum=%d", 
		descriptionEncoded, cityEncoded, pageNum)
	if remoteOnly {
		baseURL += "&f_WT=2"
	}
	
	// Add random query parameters
	linkedInURL := addRandomQueryParams(baseURL)

	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(attempt*500) * time.Millisecond
			time.Sleep(backoff)
		}

		client := createHTTPClient(proxyURL)
		req, err := http.NewRequest("GET", linkedInURL, nil)
		if err != nil {
			continue
		}

		// Add more headers to look like a real browser
		req.Header = getRandomHeaders()

		resp, err := client.Do(req)
		if err != nil || resp.StatusCode != 200 {
			if debug {
				log.Printf("Error on page %d (attempt %d/%d): Status: %d, Error: %v", 
					pageNum, attempt+1, maxRetries, resp.StatusCode, err)
			}
			continue
		}
		defer resp.Body.Close()

		// Reduce parsing delay
		time.Sleep(time.Duration(rand.Intn(200)) * time.Millisecond)

		doc, err := goquery.NewDocumentFromReader(resp.Body)
		if err != nil {
			continue
		}

		// Check if we're being blocked
		if isBlockedPage(doc) {
			if debug {
				log.Printf("Detected blocking page on attempt %d", attempt+1)
			}
			continue
		}

		foundJobs := false
		doc.Find("div.job-search-card").Each(func(i int, s *goquery.Selection) {
			foundJobs = true
			title := s.Find("h3.base-search-card__title").Text()
			company := s.Find("a.hidden-nested-link").Text()
			location := s.Find("span.job-search-card__location").Text()
			link, exists := s.Find("a.base-card__full-link").Attr("href")

			if exists && isValidJob(title, location, titleKeyword, remoteOnly, internshipsOnly) {
				pageJobs = append(pageJobs, SalaryInfo{
					Title:    strings.TrimSpace(title),
					Company:  strings.TrimSpace(company),
					Location: strings.TrimSpace(location),
					URL:      strings.TrimSpace(link),
					Source:   "linkedin",
				})
			}
		})

		if !foundJobs && debug {
			log.Printf("No job cards found on page %d, attempt %d", pageNum, attempt+1)
		}

		if len(pageJobs) > 0 {
			return pageJobs
		}
	}

	return pageJobs
}

func getRandomHeaders() http.Header {
	headers := http.Header{
		"User-Agent":      {getRandomUserAgent()},
		"Accept":         {"text/html,application/xhtml+xml,application/xml;q=0.9,image/webp,*/*;q=0.8"},
		"Accept-Language": {"en-US,en;q=0.5"},
		"Connection":     {"keep-alive"},
		"Cache-Control":  {"no-cache"},
		"Pragma":        {"no-cache"},
		"Sec-Fetch-Dest": {"document"},
		"Sec-Fetch-Mode": {"navigate"},
		"Sec-Fetch-Site": {"none"},
		"Sec-Fetch-User": {"?1"},
		"DNT":           {"1"},
		"Upgrade-Insecure-Requests": {"1"},
	}
	return headers
}

func isBlockedPage(doc *goquery.Document) bool {
	// Check for common blocking indicators
	blockedTexts := []string{
		"please verify you are a human",
		"unusual activity",
		"security verification",
		"check your network",
	}
	
	pageText := strings.ToLower(doc.Text())
	for _, text := range blockedTexts {
		if strings.Contains(pageText, text) {
			return true
		}
	}
	return false
}

func isValidJob(title, location, titleKeyword string, remoteOnly, internshipsOnly bool) bool {
	isInternship := strings.Contains(strings.ToLower(title), "intern")
	if !internshipsOnly && isInternship {
		return false
	}

	if titleKeyword != "" && !strings.Contains(strings.ToLower(title), strings.ToLower(titleKeyword)) {
		return false
	}

	isRemote := strings.Contains(strings.ToLower(location), "remote") || strings.Contains(strings.ToLower(location), "united states")
	if remoteOnly && !isRemote {
		return false
	}

	return true
}

func extractNumericValue(salaryStr string) int {
	// Remove non-numeric characters and convert to integer
	re := regexp.MustCompile("[^0-9]")
	salaryStr = re.ReplaceAllString(salaryStr, "")
	salary, _ := strconv.Atoi(salaryStr)
	return salary
}

func colorizeSalary(salary string) string {
    // Remove all non-numeric characters from the salary string
    re := regexp.MustCompile("[^0-9]")
    salaryStr := re.ReplaceAllString(salary, "")

    // Convert the cleaned string to an integer
    salaryInt, err := strconv.Atoi(salaryStr)
    if err != nil {
        return salary // Return the original string if conversion fails
    }

    // Format the salary with commas
    salaryFormatted := fmt.Sprintf("$%s", humanize.Comma(int64(salaryInt)))

    // Apply color based on the salary value
    switch {
    case salaryInt >= 300000:
        return fmt.Sprintf("\033[32m%s\033[0m", salaryFormatted) // Bright Green
    case salaryInt >= 200000:
        return fmt.Sprintf("\033[92m%s\033[0m", salaryFormatted) // Green
    case salaryInt >= 100000:
        return fmt.Sprintf("\033[93m%s\033[0m", salaryFormatted) // Yellow
    default:
        return fmt.Sprintf("\033[31m%s\033[0m", salaryFormatted) // Red
    }
}

func getRandomUserAgent() string {
	userAgents := []string{
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36",
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36",
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:89.0) Gecko/20100101 Firefox/89.0",
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/14.1.1 Safari/605.1.15",
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36 Edg/91.0.864.59",
	}
	return userAgents[rand.Intn(len(userAgents))]
}

func scrapeLinkedInPages(description, city, titleKeyword string, remoteOnly, internshipsOnly bool, pages int, debug bool, progress *ScrapeProgress, proxyURL string) ([]SalaryInfo, error) {
	var jobs []SalaryInfo
	var jobsMutex sync.Mutex

	// Create a channel for page processing with buffer
	pageChan := make(chan int, pages)
	for i := 0; i < pages; i++ {
		pageChan <- i
	}
	close(pageChan)

	var wg sync.WaitGroup
	// Process pages concurrently with limited workers
	pageWorkers := 5
	for worker := 0; worker < pageWorkers; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			
			for pageNum := range pageChan {
				if pageNum > 0 {
					time.Sleep(minDelay)
				}

				pageJobs := scrapeLinkedInPage(pageNum, description, city, titleKeyword, remoteOnly, internshipsOnly, debug, proxyURL)
				
				if len(pageJobs) > 0 {
					jobsMutex.Lock()
					jobs = append(jobs, pageJobs...)
					jobsMutex.Unlock()
				}
				
				progress.PageBar.Increment()
			}
		}()
	}

	wg.Wait()
	return jobs, nil
}

func isValidSource(source string) bool {
	validSources := map[string]bool{
		"lever": true,
		"linkedin": true,
		"greenhouse": true,
		"": true,  // empty string means all sources
	}
	return validSources[strings.ToLower(source)]
}

func main() {
	description := flag.String("d", "", "Job characteristic or keyword to search for in the job description on LinkedIn")
	city := flag.String("l", "United States", "City name to search for jobs on LinkedIn, or 'United States' for nationwide search")
	titleKeyword := flag.String("t", "", "Keyword to search for in job titles")
	remoteOnly := flag.Bool("r", false, "Include only remote jobs in the search results")
	internshipsOnly := flag.Bool("internships", false, "Include only internships in the search results")
	silence := flag.Bool("s", false, "Silence the banner")
	noBanner := flag.Bool("nobanner", false, "Silence the banner (alias for -s)")
	pages := flag.Int("p", 5, "Number of pages to search (default: 5)")
	debug := flag.Bool("debug", false, "Enable debug output")
	table := flag.Bool("table", false, "Re-organize output into a table in ascending order based on median salary")
	source := flag.String("source", "", "Filter jobs by source (lever|linkedin|greenhouse)")
	proxy := flag.String("proxy", "", "Proxy URL (e.g., http://proxy:port)")

	flag.Usage = func() {
		if !*silence && !*noBanner {
			printBanner(false)
		}
		fmt.Println("Usage:")
		flag.PrintDefaults()
	}

	flag.Parse()

	if *description == "" && *titleKeyword == "" {
			flag.Usage()
			fmt.Println("\nPlease provide a job title keyword to search for with -t, or a job description keyword with -d.")
			os.Exit(0) 
	}

	// Use either -s or -nobanner to silence the banner
	if !*silence && !*noBanner {
		printBanner(false)
	}

	// Validate source if provided
	if *source != "" && !isValidSource(*source) {
		fmt.Println("Invalid source. Must be one of: lever, linkedin, greenhouse")
		os.Exit(1)
	}

	// Validate proxy URL if provided
	if *proxy != "" {
		_, err := url.Parse(*proxy)
		if err != nil {
			fmt.Printf("Invalid proxy URL: %v\n", err)
			os.Exit(1)
		}
	}

	// Pull the job listings based on the provided arguments
	jobs, err := scrapeLinkedIn(*description, *city, *titleKeyword, *remoteOnly, *internshipsOnly, *pages, *debug, *proxy, *source)
	if err != nil {
		fmt.Println("Error scraping LinkedIn:", err)
		return
	}

	if len(jobs) == 0 {
		fmt.Println("No jobs found matching your criteria.")
		return
	}

	// Filter jobs by source if specified
	if *source != "" {
		filteredJobs := []SalaryInfo{}
		for _, job := range jobs {
			if strings.EqualFold(job.Source, *source) {
				filteredJobs = append(filteredJobs, job)
			}
		}
		jobs = filteredJobs
	}

	if *table {
		// Filter jobs with Levels.fyi salary data only
		filteredJobs := []SalaryInfo{}
		for _, job := range jobs {
			if job.LevelSalary != "" && job.LevelSalary != "No Data" {
				filteredJobs = append(filteredJobs, job)
			}
		}

		// Sort jobs by salary (assuming salary is formatted as "$200,000 - $300,000")
		sort.SliceStable(filteredJobs, func(i, j int) bool {
			// Extract numeric value for comparison
			salaryI := extractNumericValue(filteredJobs[i].LevelSalary)
			salaryJ := extractNumericValue(filteredJobs[j].LevelSalary)
			return salaryI > salaryJ
		})

		// Print the table header
		fmt.Printf("\033[1m%-25s %-25s %-50s %-15s %-50s\033[0m\n", 
			"Company Name", "Median Salary", "Job Title", "Source", "Job URL")
		for _, job := range filteredJobs {
			coloredSalary := colorizeSalary(job.LevelSalary)
			fmt.Printf("\033[35m%-25s\033[0m %-25s %-50s %-15s %-50s\n",
				job.Company, coloredSalary, job.Title, job.Source, job.URL)
		}
	} else {
		// Default output logic
		for _, job := range jobs {
			fmt.Printf("Company: \033[35m%s\033[0m\n", job.Company)
			fmt.Printf("Job Title: \033[35m%s\033[0m\n", job.Title)
			fmt.Printf("Location: \033[35m%s\033[0m\n", job.Location)
//			fmt.Printf("Source: \033[36m%s\033[0m\n", job.Source)
			if job.SalaryRange != "" {
				fmt.Printf("Salary Range: \033[32m%s\033[0m\n", job.SalaryRange)
			} else {
				fmt.Println("Salary Range: Not specified")
			}
			if job.LevelSalary != "" && job.LevelSalary != "No Data" {
				coloredSalary := colorizeSalary(job.LevelSalary)
				fmt.Printf("Levels.fyi Salary: %s\n", coloredSalary)
			} else {
				fmt.Println("Levels.fyi Salary: No Data")
			}
			fmt.Printf("Job URL: %s\n", job.URL)
			fmt.Println(strings.Repeat("-", 50))
		}
	}

	// Print the total number of jobs found
	fmt.Printf("Total jobs found: %d\n", len(jobs))
}
