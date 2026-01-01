package main

import (
	"flag"
	"fmt"
	"log"
	"strings"
	"sort"
	"os"

	"github.com/fr4nk3nst1ner/salarysleuth/internal/models"
	"github.com/fr4nk3nst1ner/salarysleuth/internal/scraper"
	"github.com/fr4nk3nst1ner/salarysleuth/internal/ui"
	"github.com/fr4nk3nst1ner/salarysleuth/internal/utils"
)

// printExamples displays usage examples for the program
func printExamples() {
	fmt.Println("\n📋 SalarySleuth Usage Examples 📋")
	fmt.Println("\n1. Search for remote jobs across the United States that mention \"OSCP\" in the job description:")
	fmt.Println("   salarysleuth -description \"OSCP\" -remote")
	
	fmt.Println("\n2. Search only for internships that include \"Software Engineer\" in the title:")
	fmt.Println("   salarysleuth -description \"Software Engineer\" -internships")
	
	fmt.Println("\n3. Search for jobs in \"San Francisco, CA\" that mention \"Metasploit\" in the job title, and silence the banner:")
	fmt.Println("   salarysleuth -description \"Metasploit\" -city \"San Francisco, CA\" -silence")
	
	fmt.Println("\n4. Search for jobs at top paying tech companies that mention \"Python\":")
	fmt.Println("   salarysleuth -description \"Python\" -top-pay")
	
	fmt.Println("\n5. Display the list of top paying companies according to levels.fyi:")
	fmt.Println("   salarysleuth -top-paying-companies")
	
	fmt.Println("\n6. Search for \"Software Engineer\" jobs across multiple pages with a proxy:")
	fmt.Println("   salarysleuth -description \"Software Engineer\" -pages 3 -proxy http://localhost:8080")
	
	fmt.Println("\n7. Search for jobs on Indeed and display results in table format:")
	fmt.Println("   salarysleuth -description \"DevOps\" -source indeed -table")
	
	fmt.Println("\nFor more information, visit: https://github.com/fr4nk3nst1ner/salarysleuth")
	os.Exit(0)
}

func main() {
	// Command line flags
	description := flag.String("description", "", "Job description to search for")
	city := flag.String("city", "", "City to search in")
	titleKeyword := flag.String("title", "", "Title keyword to filter by")
	pages := flag.Int("pages", 10, "Number of pages to scrape")
	source := flag.String("source", "", "Source to scrape (linkedin, greenhouse, lever, monster, indeed). If not specified, searches all sources.")
	remoteOnly := flag.Bool("remote", false, "Only show remote positions")
	internshipsOnly := flag.Bool("internships", false, "Only show internship positions")
	topPayOnly := flag.Bool("top-pay", false, "Only show jobs from top paying companies according to levels.fyi")
	debug := flag.Bool("debug", false, "Enable debug mode")
	proxyURL := flag.String("proxy", "", "Proxy URL to use")
	table := flag.Bool("table", false, "Show results in table format (only jobs with Levels.fyi data)")
	noLevels := flag.Bool("no-levels", false, "Skip fetching salary data from Levels.fyi")
	topPayingCompanies := flag.Bool("top-paying-companies", false, "Show the list of top paying companies from Levels.fyi")
	examples := flag.Bool("examples", false, "Show usage examples")
	
	// Banner control flags (two aliases for the same functionality)
	silence := flag.Bool("silence", false, "Silence the banner")
	noBanner := flag.Bool("nobanner", false, "Silence the banner (alias for -silence)")

	flag.Parse()

	// Display banner (skip if either -silence or -nobanner is set)
	ui.PrintBanner(*silence || *noBanner)

	// Handle -examples flag
	if *examples {
		printExamples()
		return
	}

	// Handle -top-paying-companies flag
	if *topPayingCompanies {
		if err := utils.PrintTopPayingCompanies(*debug); err != nil {
			log.Fatalf("Error fetching top paying companies: %v", err)
		}
		return
	}

	// Validate inputs
	if *description == "" {
		log.Fatal("Description is required")
	}

	// Validate table mode with no-levels
	if *table && *noLevels {
		log.Fatal("Cannot use -table with -no-levels as table mode requires Levels.fyi data")
	}

	// Initialize progress tracking
	progress := &models.ScrapeProgress{
		FoundJobs: 0,
	}

	var allResults []models.SalaryInfo

	// Set sources to search - default to all working sources if not specified
	// Note: Indeed and Monster have aggressive bot protection, so we exclude them from "all"
	var sourcesToSearch []string
	if *source != "" {
		if !utils.IsValidSource(*source) {
			log.Fatal("Invalid source. Must be one of: linkedin, greenhouse, lever, monster, indeed")
		}
		sourcesToSearch = []string{*source}
	} else {
		// Search all reliable sources when none specified
		sourcesToSearch = []string{"linkedin", "greenhouse", "lever"}
		fmt.Println("Searching all sources: LinkedIn, Greenhouse, Lever")
	}

	// Search each source
	for _, src := range sourcesToSearch {
		var results []models.SalaryInfo
		var err error

		if *debug {
			fmt.Printf("\nSearching source: %s\n", src)
		}

		switch strings.ToLower(src) {
		case "linkedin":
			results, err = scraper.ScrapeLinkedIn(*description, *city, *titleKeyword, *remoteOnly, *internshipsOnly, *topPayOnly, *pages, *debug, *proxyURL, progress)
		case "greenhouse":
			results, err = scraper.ScrapeGreenhouse(*description, *pages, *debug, *proxyURL, progress, *topPayOnly)
			// Filter for remote jobs in post-processing
			if *remoteOnly && err == nil {
				var filteredResults []models.SalaryInfo
				for _, job := range results {
					if utils.IsValidJob(job.Title, job.Location, *titleKeyword, true, *internshipsOnly, *topPayOnly, job.Company) {
						filteredResults = append(filteredResults, job)
					}
				}
				results = filteredResults
			}
		case "lever":
			results, err = scraper.ScrapeLever(*description, *pages, *debug, *proxyURL, progress, *topPayOnly)
			// Filter for remote jobs in post-processing
			if *remoteOnly && err == nil {
				var filteredResults []models.SalaryInfo
				for _, job := range results {
					if utils.IsValidJob(job.Title, job.Location, *titleKeyword, true, *internshipsOnly, *topPayOnly, job.Company) {
						filteredResults = append(filteredResults, job)
					}
				}
				results = filteredResults
			}
		case "monster":
			results, err = scraper.ScrapeMonster(*description, *pages, *debug, *proxyURL, progress, *topPayOnly)
			// Filter for remote jobs in post-processing
			if *remoteOnly && err == nil {
				var filteredResults []models.SalaryInfo
				for _, job := range results {
					if utils.IsValidJob(job.Title, job.Location, *titleKeyword, true, *internshipsOnly, *topPayOnly, job.Company) {
						filteredResults = append(filteredResults, job)
					}
				}
				results = filteredResults
			}
		case "indeed":
			results, err = scraper.ScrapeIndeed(*description, *pages, *debug, *proxyURL, progress, *topPayOnly)
			// Filter for remote jobs in post-processing
			if *remoteOnly && err == nil {
				var filteredResults []models.SalaryInfo
				for _, job := range results {
					if utils.IsValidJob(job.Title, job.Location, *titleKeyword, true, *internshipsOnly, *topPayOnly, job.Company) {
						filteredResults = append(filteredResults, job)
					}
				}
				results = filteredResults
			}
		}

		if err != nil {
			fmt.Printf("Error searching %s: %v\n", src, err)
			continue
		}

		allResults = append(allResults, results...)
	}

	// Deduplicate results if searching multiple sources
	if len(sourcesToSearch) > 1 {
		allResults = deduplicateJobs(allResults)
		if *debug {
			fmt.Printf("After deduplication: %d unique jobs\n", len(allResults))
		}
	}

	// Process results with Levels.fyi data if not disabled
	if len(allResults) > 0 && !*noLevels {
		fmt.Printf("\nFetching salary data from Levels.fyi...\n")
		utils.ProcessWithLevelsFyi(allResults, *debug)
	}

	// Print results
	fmt.Printf("\nFound %d jobs with salary information\n\n", len(allResults))

	if *table {
		// Filter jobs with Levels.fyi salary data only
		filteredJobs := []models.SalaryInfo{}
		for _, job := range allResults {
			if job.LevelSalary != "" && job.LevelSalary != "No Data" {
				filteredJobs = append(filteredJobs, job)
			}
		}

		// Sort jobs by salary
		sort.SliceStable(filteredJobs, func(i, j int) bool {
			salaryI := utils.ExtractNumericValue(filteredJobs[i].LevelSalary)
			salaryJ := utils.ExtractNumericValue(filteredJobs[j].LevelSalary)
			return salaryI > salaryJ
		})

		// Print table header
		fmt.Printf("\n\033[1m%-15s %-25s %-37s %-20s %s\033[0m\n",
			"Company Name", "Levels.fyi Median", "Job Title", "Source", "Job URL")
		fmt.Println(strings.Repeat("-", 175))

		// Print table rows
		for _, job := range filteredJobs {
			fmt.Printf("\033[35m%-15s\033[0m %-25s %-37s %-20s %s\n",
				truncateString(job.Company, 14),
				ui.ColorizeSalary(job.LevelSalary),
				truncateString(job.Title, 36),
				job.Source,
				job.URL)
		}
		fmt.Println(strings.Repeat("-", 175))
		fmt.Printf("\nShowing %d jobs with Levels.fyi salary data\n", len(filteredJobs))
	} else {
		// Standard output format
		for _, job := range allResults {
			fmt.Printf("Company: %s\n", job.Company)
			fmt.Printf("Title: %s\n", job.Title)
			fmt.Printf("Location: %s\n", job.Location)
			// fmt.Printf("Salary Range: %s\n", ui.ColorizeSalary(job.SalaryRange))
			if !*noLevels && job.LevelSalary != "" && job.LevelSalary != "No Data" {
				fmt.Printf("Levels.fyi Average: %s\n", ui.ColorizeSalary(job.LevelSalary))
			}
			fmt.Printf("URL: %s\n", job.URL)
			fmt.Printf("Source: %s\n", job.Source)
			fmt.Println(strings.Repeat("-", 80))
		}
	}
}

// truncateString truncates a string to the specified length and adds "..." if necessary
func truncateString(s string, length int) string {
	if len(s) <= length {
		return s
	}
	return s[:length-3] + "..."
}

// deduplicateJobs removes duplicate job listings based on company + title
// When duplicates are found, it prefers entries with salary information
func deduplicateJobs(jobs []models.SalaryInfo) []models.SalaryInfo {
	seen := make(map[string]int) // maps key to index in result slice
	var result []models.SalaryInfo

	for _, job := range jobs {
		// Create a normalized key from company + title
		key := normalizeForDedup(job.Company) + "|" + normalizeForDedup(job.Title)

		if existingIdx, exists := seen[key]; exists {
			// Duplicate found - prefer the one with salary info
			existing := result[existingIdx]
			if existing.SalaryRange == "Not Available" && job.SalaryRange != "Not Available" {
				// Replace with the one that has salary info
				result[existingIdx] = job
			}
			// Otherwise keep the existing one (first source wins)
		} else {
			// New job, add it
			seen[key] = len(result)
			result = append(result, job)
		}
	}

	return result
}

// normalizeForDedup normalizes a string for deduplication comparison
func normalizeForDedup(s string) string {
	// Convert to lowercase and remove extra whitespace
	s = strings.ToLower(strings.TrimSpace(s))
	// Remove common variations
	s = strings.ReplaceAll(s, ",", "")
	s = strings.ReplaceAll(s, ".", "")
	s = strings.ReplaceAll(s, "-", " ")
	s = strings.ReplaceAll(s, "  ", " ")
	return s
} 