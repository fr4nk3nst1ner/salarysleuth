package scraper

import (
	"github.com/fr4nk3nst1ner/salarysleuth/internal/models"
)

// ScrapeGreenhouse scrapes job listings from Greenhouse
func ScrapeGreenhouse(description string, pages int, debug bool, proxyURL string, progress *models.ScrapeProgress) ([]models.SalaryInfo, error) {
	// Placeholder implementation
	return []models.SalaryInfo{}, nil
} 