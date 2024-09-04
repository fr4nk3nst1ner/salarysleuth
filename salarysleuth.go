package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"

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

func printBanner(silence bool) {
	if !silence {
		fmt.Println(banner)
	}
}

func extractSalaryFromJobPage(jobURL string) (string, error) {
	res, err := http.Get(jobURL)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()

	doc, err := goquery.NewDocumentFromReader(res.Body)
	if err != nil {
		return "", err
	}

	// extract salary information from `JSON-LD`` script tag
	var salaryRange string
	doc.Find("script[type='application/ld+json']").Each(func(i int, s *goquery.Selection) {
		jsonContent := s.Text()

		if strings.Contains(jsonContent, "baseSalary") {
			var salaryData Salary
			if err := json.Unmarshal([]byte(jsonContent), &salaryData); err != nil {
				log.Println("Error parsing JSON:", err)
				return
			}

			salaryRange = fmt.Sprintf("$%.2f - $%.2f", salaryData.BaseSalary.Value.MinValue, salaryData.BaseSalary.Value.MaxValue)
		}
	})

	if salaryRange == "" {
		salaryRange = "Not specified"
	}

	return salaryRange, nil
}

func getSalaryFromLevelsFyi(companyName string) (string, error) {
	url := fmt.Sprintf("https://www.levels.fyi/companies/%s/salaries/", companyName)

	resp, err := http.Get(url)
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

func scrapeLinkedIn(description, city, keyword string, remoteOnly bool, internshipsOnly bool) ([]SalaryInfo, error) {
	descriptionEncoded := url.QueryEscape(description)
	cityEncoded := url.QueryEscape(city)

	linkedInURL := fmt.Sprintf("https://www.linkedin.com/jobs/search?keywords=%s&location=%s&pageNum=0", descriptionEncoded, cityEncoded)

	resp, err := http.Get(linkedInURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return nil, err
	}

	var jobs []SalaryInfo
	jobCount := doc.Find("div.job-search-card").Length()
	bar := pb.StartNew(jobCount)

	doc.Find("div.job-search-card").Each(func(i int, s *goquery.Selection) {
		title := s.Find("h3.base-search-card__title").Text()
		company := s.Find("a.hidden-nested-link").Text()
		location := s.Find("span.job-search-card__location").Text()
		link, exists := s.Find("a.base-card__full-link").Attr("href")

		if exists {
			// Apply internship filter logic
			if internshipsOnly {
				if !strings.Contains(strings.ToLower(title), "intern") {
					bar.Increment()
					return
				}
			} else {
				if strings.Contains(strings.ToLower(title), "intern") {
					bar.Increment()
					return
				}
			}

			// filter jobs based on the location and remote-only flag if applicable
			isRemote := strings.Contains(strings.ToLower(location), "remote") || strings.Contains(strings.ToLower(location), "united states")
			if (!remoteOnly || isRemote) && (keyword == "" || strings.Contains(strings.ToLower(title), strings.ToLower(keyword))) {
				job := SalaryInfo{
					Title:    strings.TrimSpace(title),
					Company:  strings.TrimSpace(company),
					Location: strings.TrimSpace(location),
					URL:      strings.TrimSpace(link),
				}

				// extract salary from LinkedIn  
				salary, err := extractSalaryFromJobPage(job.URL)
				if err == nil && salary != "" {
					job.SalaryRange = salary
				} else {
					job.SalaryRange = "Not specified"
				}

				// cross-reference with levels.fyi
				levelSalary, err := getSalaryFromLevelsFyi(strings.ToLower(strings.ReplaceAll(job.Company, " ", "-")))
				if err == nil && levelSalary != "" {
					job.LevelSalary = levelSalary
				} else {
					job.LevelSalary = "No Data"
				}

				jobs = append(jobs, job)
			}
		}
		bar.Increment()
	})

	bar.Finish()

	return jobs, nil
}

func main() {
	description := flag.String("d", "", "Job characteristic or keyword to search for on LinkedIn")
	city := flag.String("l", "United States", "City name to search for jobs on LinkedIn, or 'United States' for nationwide search")
	keyword := flag.String("t", "", "Keyword to search for in job titles")
	remoteOnly := flag.Bool("r", false, "Include only remote jobs in the search results")
	internshipsOnly := flag.Bool("internships", false, "Include only internships in the search results")
	silence := flag.Bool("s", false, "Silence the banner")

	flag.Parse()

	if *description == "" && *keyword == "" {
		fmt.Println("Please provide a job description or keyword to search for. Use --help for usage details.")
		return
	}

	printBanner(*silence)

	// pull the job listings based on the provided arguments
	jobs, err := scrapeLinkedIn(*description, *city, *keyword, *remoteOnly, *internshipsOnly)
	if err != nil {
		fmt.Println("Error scraping LinkedIn:", err)
		return
	}

	if len(jobs) == 0 {
		fmt.Println("No jobs found matching your criteria.")
		return
	}

	// return the job listings
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
}
