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

	"math/rand"
	"time"
//	"math"

	"github.com/pterm/pterm"
	"github.com/PuerkitoBio/goquery"
	"github.com/cheggaaa/pb/v3"
    "github.com/dustin/go-humanize"
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

func createHTTPClient() *http.Client {
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			MaxIdleConns:        100,
			MaxIdleConnsPerHost: 100,
			IdleConnTimeout:     90 * time.Second,
		},
	}
}

func extractSalaryFromJobPage(jobURL string, debug bool) (string, error) {
	client := createHTTPClient()
	
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

func getSalaryFromLevelsFyi(companyName string) (string, error) {
	url := fmt.Sprintf("https://www.levels.fyi/companies/%s/salaries/", companyName)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", userAgent)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return "", err
	}

	// Parse the salary from the levels.fyi page
	salaryElem := doc.Find("td:contains('Software Engineer Salary')").Next().Text()
	if salaryElem == "" {
		return "No Data", nil
	}

	return salaryElem, nil
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
			salary, err := extractSalaryFromJobPage(job.URL, debug)
			if err == nil && salary != "" {
				job.SalaryRange = salary
			} else {
				job.SalaryRange = "Not specified"
			}

			// Cross-reference with levels.fyi
			levelSalary, err := getSalaryFromLevelsFyi(strings.ToLower(strings.ReplaceAll(job.Company, " ", "-")))
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

func scrapeLinkedIn(description, city, titleKeyword string, remoteOnly bool, internshipsOnly bool, pages int, debug bool) ([]SalaryInfo, error) {
	var jobs []SalaryInfo
	var jobsMutex sync.Mutex
	
	// Create progress tracking
	progress := &ScrapeProgress{
		PageBar: pb.New(pages),
		JobBar:  pb.New(0),
	}
	
	// Set the total for both bars
	progress.PageBar.SetTotal(int64(pages))
	
	// Create progress bar pool
	pool, err := pb.StartPool(progress.PageBar, progress.JobBar)
	if err != nil {
		return nil, fmt.Errorf("failed to create progress bars: %v", err)
	}
	defer pool.Stop()

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

				pageJobs := scrapeLinkedInPage(pageNum, description, city, titleKeyword, remoteOnly, internshipsOnly, debug)
				
				if len(pageJobs) > 0 {
					jobsMutex.Lock()
					jobs = append(jobs, pageJobs...)
					progress.mu.Lock()
					progress.FoundJobs += len(pageJobs)
					progress.JobBar.SetTotal(int64(progress.FoundJobs))
					progress.mu.Unlock()
					jobsMutex.Unlock()
				}
				
				progress.PageBar.Increment()
			}
		}()
	}

	wg.Wait()

	// Update job progress bar total
	progress.JobBar.SetTotal(int64(progress.FoundJobs))

	// Process all jobs concurrently using the new batch processor
	if len(jobs) > 0 {
		processBatchJobs(jobs, debug, progress.JobBar)
	}

	return jobs, nil
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

func scrapeLinkedInPage(pageNum int, description, city, titleKeyword string, remoteOnly, internshipsOnly, debug bool) []SalaryInfo {
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

		client := createHTTPClient()
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

func main() {
	description := flag.String("d", "", "Job characteristic or keyword to search for in the job description on LinkedIn")
	city := flag.String("l", "United States", "City name to search for jobs on LinkedIn, or 'United States' for nationwide search")
	titleKeyword := flag.String("t", "", "Keyword to search for in job titles")
	remoteOnly := flag.Bool("r", false, "Include only remote jobs in the search results")
	internshipsOnly := flag.Bool("internships", false, "Include only internships in the search results")
	silence := flag.Bool("s", false, "Silence the banner")
	pages := flag.Int("p", 5, "Number of pages to search (default: 5)")
	debug := flag.Bool("debug", false, "Enable debug output")
	table := flag.Bool("table", false, "Re-organize output into a table in ascending order based on median salary")

	flag.Usage = func() {
		printBanner(*silence)
		fmt.Println("Usage:")
		flag.PrintDefaults()
	}

	flag.Parse()

	if *description == "" && *titleKeyword == "" {
		//printBanner(*silence)
		flag.Usage()
		fmt.Println("\nPlease provide a job title keyword to search for with -t, or a job description keyword with -d.")
		os.Exit(0) 
	}

	if !*silence {
		printBanner(*silence)
	}

	// Pull the job listings based on the provided arguments
	jobs, err := scrapeLinkedIn(*description, *city, *titleKeyword, *remoteOnly, *internshipsOnly, *pages, *debug)
	if err != nil {
		fmt.Println("Error scraping LinkedIn:", err)
		return
	}

	if len(jobs) == 0 {
		fmt.Println("No jobs found matching your criteria.")
		return
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
		fmt.Printf("\033[1m%-25s %-25s %-50s %-50s\033[0m\n", "Company Name", "Median Salary", "Job Title", "Job URL")
		for _, job := range filteredJobs {
			coloredSalary := colorizeSalary(job.LevelSalary)
			fmt.Printf("\033[35m%-25s\033[0m %-25s %-50s %-50s\n",
				job.Company, coloredSalary, job.Title, job.URL)
		}
	} else {
		// Default output logic
		for _, job := range jobs {
			fmt.Printf("Company: \033[35m%s\033[0m\n", job.Company)
			fmt.Printf("Job Title: \033[35m%s\033[0m\n", job.Title)
			fmt.Printf("Location: \033[35m%s\033[0m\n", job.Location)
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
