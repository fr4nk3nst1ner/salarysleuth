package main

import (
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// AppConfig represents the application configuration
type AppConfig struct {
	Scraper  ScraperConfig  `yaml:"scraper"`
	Telegram TelegramConfig `yaml:"telegram"`
	Filters  FiltersConfig  `yaml:"filters"`
	Display  DisplayConfig  `yaml:"display"`
}

type ScraperConfig struct {
	Pages       int    `yaml:"pages"`
	Description string `yaml:"description"`
}

type TelegramConfig struct {
	BotToken string `yaml:"bot_token"` // Prefer TELEGRAM_BOT_TOKEN env var
	ChatID   string `yaml:"chat_id"`   // Prefer TELEGRAM_CHAT_ID env var
	Enabled  bool   `yaml:"enabled"`
}

type FiltersConfig struct {
	Categories     map[string]CategoryConfig `yaml:"categories"`
	Levels         map[string]CategoryConfig `yaml:"levels"`
	Certifications map[string]CategoryConfig `yaml:"certifications"`
	Remote         CategoryConfig            `yaml:"remote"`
	Exclude        ExcludeConfig             `yaml:"exclude"`
}

type CategoryConfig struct {
	Keywords    []string `yaml:"keywords"`
	DisplayName string   `yaml:"display_name"`
}

type ExcludeConfig struct {
	DefensiveRoles []string `yaml:"defensive_roles"`
	NonSecurity    []string `yaml:"non_security"`
	Compliance     []string `yaml:"compliance"`
}

type DisplayConfig struct {
	ShowAllJobs    bool                `yaml:"show_all_jobs"`
	DefaultFilters DefaultFiltersConfig `yaml:"default_filters"`
	PageSize       int                  `yaml:"page_size"`
	DefaultSort    string               `yaml:"default_sort"`
}

type DefaultFiltersConfig struct {
	Categories     []string `yaml:"categories"`
	Levels         []string `yaml:"levels"`
	Certifications []string `yaml:"certifications"`
	RemoteOnly     bool     `yaml:"remote_only"`
}

// JobTags represents the tags applied to a job
type JobTags struct {
	Categories     []string `json:"categories"`
	Level          string   `json:"level"`
	Certifications []string `json:"certifications"`
	IsRemote       bool     `json:"is_remote"`
	IsExcluded     bool     `json:"is_excluded"`
	ExcludeReason  string   `json:"exclude_reason,omitempty"`
}

// TaggedJob represents a job with its tags
type TaggedJob struct {
	Job  Job     `json:"job"`
	Tags JobTags `json:"tags"`
}

var appConfig *AppConfig

// LoadConfig loads the configuration from config.yaml
func LoadConfig() (*AppConfig, error) {
	if appConfig != nil {
		return appConfig, nil
	}

	configPath := findConfigPath()
	
	data, err := os.ReadFile(configPath)
	if err != nil {
		// Return default config if file not found
		return getDefaultConfig(), nil
	}

	var cfg AppConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	appConfig = &cfg
	return appConfig, nil
}

func findConfigPath() string {
	// Check various locations for config.yaml
	paths := []string{
		"config.yaml",
		filepath.Join(config.DataDir, "..", "config.yaml"),
		"/app/salarysleuth/jobtracker/config.yaml",
		"/app/config.yaml",
	}

	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}

	return "config.yaml"
}

func getDefaultConfig() *AppConfig {
	return &AppConfig{
		Scraper: ScraperConfig{
			Pages:       20,
			Description: "Offensive Security",
		},
		Telegram: TelegramConfig{
			Enabled: true,
		},
		Filters: FiltersConfig{
			Categories: map[string]CategoryConfig{
				"penetration_testing": {
					Keywords:    []string{"penetration test", "pentest", "pen test", "ethical hack"},
					DisplayName: "Penetration Testing",
				},
				"red_team": {
					Keywords:    []string{"red team", "adversary", "purple team"},
					DisplayName: "Red Team",
				},
				"appsec": {
					Keywords:    []string{"appsec", "application security", "product security"},
					DisplayName: "AppSec",
				},
				"offensive_security": {
					Keywords:    []string{"offensive security", "offensive"},
					DisplayName: "Offensive Security",
				},
				"security_research": {
					Keywords:    []string{"security research", "vulnerability research", "exploit"},
					DisplayName: "Security Research",
				},
			},
			Levels: map[string]CategoryConfig{
				"management": {
					Keywords:    []string{"manager", "head of", "lead", "director"},
					DisplayName: "Management/Lead",
				},
				"senior": {
					Keywords:    []string{"senior", "sr.", "sr ", "staff", "principal"},
					DisplayName: "Senior IC",
				},
				"mid": {
					Keywords:    []string{"ii", "iii"},
					DisplayName: "Mid-Level",
				},
				"junior": {
					Keywords:    []string{"junior", "jr.", "entry", "associate", "intern"},
					DisplayName: "Junior/Entry",
				},
			},
			Certifications: map[string]CategoryConfig{
				"oscp": {
					Keywords:    []string{"oscp"},
					DisplayName: "OSCP",
				},
				"osce": {
					Keywords:    []string{"osce", "oswe", "osep"},
					DisplayName: "OSCE/OSWE/OSEP",
				},
			},
			Remote: CategoryConfig{
				Keywords:    []string{"remote", "work from home"},
				DisplayName: "Remote",
			},
			Exclude: ExcludeConfig{
				DefensiveRoles: []string{"soc analyst", "incident response", "blue team"},
				NonSecurity:    []string{"software engineer", "devops", "product manager"},
				Compliance:     []string{"compliance", "grc", "governance"},
			},
		},
		Display: DisplayConfig{
			ShowAllJobs: true,
			PageSize:    50,
			DefaultSort: "newest",
		},
	}
}

// TagJob applies tags to a job based on configuration
func TagJob(job Job, cfg *AppConfig) TaggedJob {
	titleLower := strings.ToLower(job.Title)
	locationLower := strings.ToLower(job.Location)
	
	tags := JobTags{
		Categories:     []string{},
		Certifications: []string{},
	}

	// Check categories
	for catID, cat := range cfg.Filters.Categories {
		for _, kw := range cat.Keywords {
			if strings.Contains(titleLower, strings.ToLower(kw)) {
				tags.Categories = append(tags.Categories, catID)
				break
			}
		}
	}

	// Check levels (take the first match)
	for levelID, level := range cfg.Filters.Levels {
		for _, kw := range level.Keywords {
			if strings.Contains(titleLower, strings.ToLower(kw)) {
				tags.Level = levelID
				break
			}
		}
		if tags.Level != "" {
			break
		}
	}
	
	// Default to mid-level if no level detected
	if tags.Level == "" {
		tags.Level = "mid"
	}

	// Check certifications
	for certID, cert := range cfg.Filters.Certifications {
		for _, kw := range cert.Keywords {
			if strings.Contains(titleLower, strings.ToLower(kw)) {
				tags.Certifications = append(tags.Certifications, certID)
				break
			}
		}
	}

	// Check remote
	for _, kw := range cfg.Filters.Remote.Keywords {
		if strings.Contains(titleLower, strings.ToLower(kw)) ||
			strings.Contains(locationLower, strings.ToLower(kw)) {
			tags.IsRemote = true
			break
		}
	}

	// Check exclusions
	for _, kw := range cfg.Filters.Exclude.DefensiveRoles {
		if strings.Contains(titleLower, strings.ToLower(kw)) {
			tags.IsExcluded = true
			tags.ExcludeReason = "Defensive role"
			break
		}
	}
	
	if !tags.IsExcluded {
		for _, kw := range cfg.Filters.Exclude.NonSecurity {
			if strings.Contains(titleLower, strings.ToLower(kw)) {
				tags.IsExcluded = true
				tags.ExcludeReason = "Non-security role"
				break
			}
		}
	}
	
	if !tags.IsExcluded {
		for _, kw := range cfg.Filters.Exclude.Compliance {
			if strings.Contains(titleLower, strings.ToLower(kw)) {
				tags.IsExcluded = true
				tags.ExcludeReason = "Compliance/GRC role"
				break
			}
		}
	}

	return TaggedJob{
		Job:  job,
		Tags: tags,
	}
}

// TagJobs applies tags to all jobs
func TagJobs(jobs []Job, cfg *AppConfig) []TaggedJob {
	tagged := make([]TaggedJob, 0, len(jobs))
	for _, job := range jobs {
		tagged = append(tagged, TagJob(job, cfg))
	}
	return tagged
}
