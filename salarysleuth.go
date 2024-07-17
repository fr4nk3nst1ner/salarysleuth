package main

import (
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

const banner = `
$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$
$$$                                                      $$$
$$$                     $alary $leuth                    $$$
$$$                     @fr4nk3nst1ner                   $$$
$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$
`

type SalaryInfo struct {
	Company string `json:"company"`
	Salary  string `json:"salary"`
	Title   string `json:"title"`
	URL     string `json:"url"`
}

func printBanner(silence bool) {
	if !silence {
		fmt.Println(banner)
	}
}

func runGoDork(query string, numPages int, searchEngine string) (string, error) {
	cmd := fmt.Sprintf("go-dork -e %s -p %d -s -q \"%s\"", searchEngine, numPages, query)
	output, err := exec.Command("sh", "-c", cmd).Output()
	if err != nil {
		return "", err
	}
	return string(output), nil
}

func extractJobUrls(outputStr string, remoteOnly bool) ([]SalaryInfo, error) {
	var jobUrls []SalaryInfo
	re := regexp.MustCompile(`(https?://\S+)\s+([^\n]+)`)

	matches := re.FindAllStringSubmatch(outputStr, -1)
	if matches == nil {
		return nil, errors.New("no matches found")
	}

	for _, match := range matches {
		jobUrls = append(jobUrls, SalaryInfo{
			URL:   match[1],
			Title: strings.TrimSpace(match[2]),
		})
	}

	if remoteOnly {
		jobUrls = filterRemoteJobs(jobUrls)
	}

	return jobUrls, nil
}

func filterRemoteJobs(jobUrls []SalaryInfo) []SalaryInfo {
	var remoteJobUrls []SalaryInfo

	for _, job := range jobUrls {
		if strings.Contains(job.URL, "lever.co") {
			resp, err := http.Get(job.URL)
			if err != nil {
				continue
			}
			defer resp.Body.Close()

			doc, err := goquery.NewDocumentFromReader(resp.Body)
			if err != nil {
				continue
			}

			commitmentElem := doc.Find(".sort-by-commitment.posting-category.medium-category-label.commitment")
			locationElem := doc.Find(".location")
			titleElem := doc.Find("h2")
			if commitmentElem.Text() == "Remote" || strings.Contains(strings.ToLower(locationElem.Text()), "remote") {
				job.Title = titleElem.Text()
				remoteJobUrls = append(remoteJobUrls, job)
			}
		} else if strings.Contains(job.URL, "greenhouse.io") {
			resp, err := http.Get(job.URL)
			if err != nil {
				continue
			}
			defer resp.Body.Close()

			doc, err := goquery.NewDocumentFromReader(resp.Body)
			if err != nil {
				continue
			}

			locationElem := doc.Find(".location")
			titleElem := doc.Find("h1")
			if strings.Contains(strings.ToLower(locationElem.Text()), "remote") {
				job.Title = titleElem.Text()
				remoteJobUrls = append(remoteJobUrls, job)
			}
		} else {
			remoteJobUrls = append(remoteJobUrls, job)
		}
	}

	return remoteJobUrls
}

func getSalary(companyName string) (string, error) {
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

	salaryElem := doc.Find("td:contains('Software Engineer Salary')").Next().Text()
	if salaryElem == "" {
		return "", errors.New("no salary data found")
	}

	return salaryElem, nil
}

func formatSalary(salary string) string {
	salary = strings.ReplaceAll(salary, "$", "")
	salary = strings.ReplaceAll(salary, ",", "")

	salaryValue, err := strconv.Atoi(salary)
	if err != nil {
		return "No Data"
	}

	return fmt.Sprintf("$%,d", salaryValue)
}

func colorizeSalary(salary string) string {
	if salary == "No Data" || salary == "" {
		return salary
	}

	formattedSalary := formatSalary(salary)
	salaryValue, err := strconv.Atoi(strings.ReplaceAll(formattedSalary, "$", ""))
	if err != nil {
		return formattedSalary
	}

	switch {
	case salaryValue >= 300000:
		return fmt.Sprintf("\033[32m%s\033[0m", formattedSalary)
	case salaryValue >= 200000:
		return fmt.Sprintf("\033[92m%s\033[0m", formattedSalary)
	case salaryValue >= 100000:
		return fmt.Sprintf("\033[93m%s\033[0m", formattedSalary)
	default:
		return fmt.Sprintf("\033[31m%s\033[0m", formattedSalary)
	}
}

func cleanSalaryString(salary string) string {
	salary = strings.ReplaceAll(salary, "%!,(int=", "")
	salary = strings.ReplaceAll(salary, ")d", "")
	n := len(salary)
	if n > 3 {
		salary = salary[:n-3] + "," + salary[n-3:]
	}
	return salary
}

func main() {
	job := flag.String("j", "", "Job characteristic to search for on job listing websites")
	company := flag.String("c", "", "Name of a specific company to search for salary information")
	silence := flag.Bool("s", false, "Silence the banner")
	pages := flag.Int("p", 50, "Number of search result pages to scrape (default: 50)")
	engine := flag.String("e", "google", "Search engine to use (default: google). Options: Google, Shodan, Bing, Duck, Yahoo, Ask. Note: Only tested with Google")
	table := flag.Bool("t", false, "Re-organize output into a table in ascending order based on median salary")
	remote := flag.Bool("r", false, "Search for remote jobs only")

	flag.Parse()

	if *job == "" && *company == "" {
		fmt.Println("Please provide a job title or company name to search for. Use --help for usage details.")
		return
	}

	printBanner(*silence)

	if *job != "" {
		dorkQuery := fmt.Sprintf("site:lever.co OR site:greenhouse.io %s", *job)
		outputStr, err := runGoDork(dorkQuery, *pages, *engine)
		if err != nil {
			fmt.Println("Error running go-dork command:", err)
			fmt.Println("It looks like 'go-dork' is not installed or the command failed.")
			fmt.Println("You can install 'go-dork' by running the following command:")
			fmt.Println("GO111MODULE=on go install dw1.io/go-dork@latest")
			return
		}

		jobUrls, err := extractJobUrls(outputStr, *remote)
		if err != nil {
			fmt.Println("Error extracting job URLs:", err)
			return
		}

		var salaries []SalaryInfo
		for _, job := range jobUrls {
			companyName := getCompanyName(job.URL)

			salary, err := getSalary(companyName)
			if err != nil {
				salary = "No Data"
			}

			job.Salary = colorizeSalary(salary)
			job.Company = companyName

			salaries = append(salaries, job)
		}

		if *table {
			salaries = filterSalariesWithNoData(salaries)
			sort.Slice(salaries, func(i, j int) bool {
				return extractSalaryValue(salaries[i].Salary) > extractSalaryValue(salaries[j].Salary)
			})

			fmt.Printf("\033[1m%-25s %-25s%-9s %-50s %-50s\033[0m\n", "Company Name", "Median Salary", "", "Job Title", "Job URL")
			for _, salary := range salaries {
				cleanedSalary := cleanSalaryString(salary.Salary)
				fmt.Printf("%-25s %-25s%-9s %-50s %-50s\n", colorizeText(salary.Company, "35"), cleanedSalary, "", salary.Title, salary.URL)
			}
		} else {
			for _, salary := range salaries {
				fmt.Println("Job URL:", salary.URL)
				fmt.Printf("Company: \033[35m%s\033[0m\n", salary.Company)
				fmt.Printf("Job Title: \033[35m%s\033[0m\n", salary.Title)
				if salary.Salary == "No Data" {
					fmt.Println("No salary information found for this company.")
				} else {
					cleanedSalary := cleanSalaryString(salary.Salary)
					fmt.Printf("Median Total Comp for Software Engineer: %s\n", cleanedSalary)
				}
				fmt.Println(strings.Repeat("-", 50))
			}
		}
	}

	if *company != "" {
		salary, err := getSalary(*company)
		if err != nil {
			fmt.Printf("Company: \033[35m%s\033[0m\n", *company)
			fmt.Println("No salary information found for this company.")
		} else {
			fmt.Printf("Company: \033[35m%s\033[0m\n", *company)
			if salary == "" {
				fmt.Println("No salary information found for this company.")
			} else {
				cleanedSalary := cleanSalaryString(colorizeSalary(salary))
				fmt.Printf("Median Total Comp for Software Engineer: %s\n", cleanedSalary)
			}
		}
		fmt.Println(strings.Repeat("-", 50))
	}
}

func getCompanyName(url string) string {
	re := regexp.MustCompile(`\/\/[^/]+\/([^/]+)\/`)
	match := re.FindStringSubmatch(url)
	if len(match) > 1 {
		return match[1]
	}
	return ""
}

func filterSalariesWithNoData(salaries []SalaryInfo) []SalaryInfo {
	var filtered []SalaryInfo
	for _, salary := range salaries {
		if salary.Salary != "No Data" {
			filtered = append(filtered, salary)
		}
	}
	return filtered
}

func extractSalaryValue(salary string) int {
	salary = strings.ReplaceAll(salary, "$", "")
	salary = strings.ReplaceAll(salary, ",", "")
	value := 0
	fmt.Sscanf(salary, "%d", &value)
	return value
}

func colorizeText(text, colorCode string) string {
	return fmt.Sprintf("\033[%sm%s\033[0m", colorCode, text)
}
