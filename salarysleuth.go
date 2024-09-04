package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/PuerkitoBio/goquery"
	"github.com/cheggaaa/pb/v3"
)

const banner = `
$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$
$$$                                                      $$$
$$$                     $alary $leuth                    $$$
$$$                     @fr4nk3nst1ner                   $$$
$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$
`

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

func printBanner(silence bool) {
	if !silence {
		fmt.Println(banner)
	}
}

func extractSalaryFromJobPage(jobURL string, debug bool) (string, error) {
	// Create a new request with a custom user agent
	req, err := http.NewRequest("GET", jobURL, nil)
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

	if salaryText == "" {
		salaryText = "Not specified"
	}

	return salaryText, nil
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

func scrapeLinkedIn(description, city, titleKeyword string, remoteOnly bool, internshipsOnly bool, pages int, debug bool) ([]SalaryInfo, error) {
	// Use the title keyword to directly search LinkedIn if no description is provided
	searchTerm := description
	if description == "" {
		searchTerm = titleKeyword
	}

	descriptionEncoded := url.QueryEscape(searchTerm)
	cityEncoded := url.QueryEscape(city)

	var jobs []SalaryInfo
	var wg sync.WaitGroup
	bar := pb.StartNew(pages)

	for pageNum := 0; pageNum < pages; pageNum++ {
		// Construct the LinkedIn URL, adding "f_WT=2" if remoteOnly is true
		linkedInURL := fmt.Sprintf("https://www.linkedin.com/jobs/search?keywords=%s&location=%s&pageNum=%d", descriptionEncoded, cityEncoded, pageNum)
		if remoteOnly {
			linkedInURL += "&f_WT=2"
		}

		// Print the generated LinkedIn URL for debugging purposes
		if debug {
			fmt.Printf("Searching LinkedIn with URL: %s\n", linkedInURL)
		}

		req, err := http.NewRequest("GET", linkedInURL, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", userAgent)

		client := &http.Client{}
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()

		doc, err := goquery.NewDocumentFromReader(resp.Body)
		if err != nil {
			return nil, err
		}

		doc.Find("div.job-search-card").Each(func(i int, s *goquery.Selection) {
			title := s.Find("h3.base-search-card__title").Text()
			company := s.Find("a.hidden-nested-link").Text()
			location := s.Find("span.job-search-card__location").Text()
			link, exists := s.Find("a.base-card__full-link").Attr("href")

			if exists {
				// Filter out internships unless the --internships flag is passed
				isInternship := strings.Contains(strings.ToLower(title), "intern")
				if !internshipsOnly && isInternship {
					bar.Increment()
					return
				}

				// Check if title matches the titleKeyword, if provided
				if titleKeyword != "" && !strings.Contains(strings.ToLower(title), strings.ToLower(titleKeyword)) {
					bar.Increment()
					return
				}

				// Filter jobs based on the location and remote-only flag if applicable
				isRemote := strings.Contains(strings.ToLower(location), "remote") || strings.Contains(strings.ToLower(location), "united states")
				if remoteOnly && !isRemote {
					bar.Increment()
					return
				}

				job := SalaryInfo{
					Title:    strings.TrimSpace(title),
					Company:  strings.TrimSpace(company),
					Location: strings.TrimSpace(location),
					URL:      strings.TrimSpace(link),
				}

				jobs = append(jobs, job)
				wg.Add(1)
				go processJob(&jobs[len(jobs)-1], &wg, bar, debug)
			}
		})
	}

	wg.Wait()
	bar.Finish()

	return jobs, nil
}

func processJob(salaryInfo *SalaryInfo, wg *sync.WaitGroup, bar *pb.ProgressBar, debug bool) {
	defer wg.Done()
	defer bar.Increment()

	// Extract salary from LinkedIn
	salary, err := extractSalaryFromJobPage(salaryInfo.URL, debug)
	if err == nil && salary != "" {
		salaryInfo.SalaryRange = salary
	} else {
		salaryInfo.SalaryRange = "Not specified"
	}

	// Cross-reference with levels.fyi
	levelSalary, err := getSalaryFromLevelsFyi(strings.ToLower(strings.ReplaceAll(salaryInfo.Company, " ", "-")))
	if err == nil && levelSalary != "" {
		salaryInfo.LevelSalary = levelSalary
	} else {
		salaryInfo.LevelSalary = "No Data"
	}
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

	flag.Parse()

	if *description == "" && *titleKeyword == "" {
		fmt.Println("Please provide a job title keyword to search for with -t, or a job description keyword with -d. Use --help for usage details.")
		return
	}

	printBanner(*silence)

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

	// Return the job listings
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
			fmt.Printf("Levels.fyi Salary: \033[32m%s\033[0m\n", job.LevelSalary)
		} else {
			fmt.Println("Levels.fyi Salary: No Data")
		}
		fmt.Printf("Job URL: %s\n", job.URL)
		fmt.Println(strings.Repeat("-", 50))
	}

	// Print the total number of jobs found
	fmt.Printf("Total jobs found: %d\n", len(jobs))
}
