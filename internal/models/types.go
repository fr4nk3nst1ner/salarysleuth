package models

// SalaryInfo represents salary information for a job posting
type SalaryInfo struct {
	Company     string `json:"company"`
	Title       string `json:"title"`
	Location    string `json:"location"`
	URL         string `json:"url"`
	SalaryRange string `json:"salary_range"`
	LevelSalary string `json:"level_salary,omitempty"`
	Source      string `json:"source"`
}

// Salary represents the salary structure from job postings
type Salary struct {
	BaseSalary struct {
		Value struct {
			MinValue float64 `json:"minValue"`
			MaxValue float64 `json:"maxValue"`
		} `json:"value"`
	} `json:"baseSalary"`
}

// ScrapeProgress represents the progress of a scraping operation
type ScrapeProgress struct {
	FoundJobs int `json:"found_jobs"`
} 