package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type TelegramAlertConfig struct {
	Enabled  bool   `json:"enabled"`
	BotToken string `json:"bot_token"`
	ChatID   string `json:"chat_id"`
	Verified bool   `json:"verified"`
}

type ScheduledScan struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Type        string `json:"type"` // "default" or "custom"
	Query       string `json:"query,omitempty"`
	Schedule    string `json:"schedule"` // "daily", "weekdays", "weekly", "custom"
	Days        []int  `json:"days,omitempty"`
	Hour        int    `json:"hour"`
	Minute      int    `json:"minute"`
	Enabled     bool   `json:"enabled"`
	NotifyEmpty bool   `json:"notify_empty"`
	LastRun     string `json:"last_run,omitempty"`
	LastResult  string `json:"last_result,omitempty"`
	CreatedAt   string `json:"created_at"`
}

type UserAlertConfig struct {
	Telegram  TelegramAlertConfig `json:"telegram"`
	Schedules []ScheduledScan     `json:"schedules"`
}

var alertsMu sync.RWMutex

func alertsDir() string {
	return filepath.Join(config.DataDir, "alerts")
}

func ensureAlertsDir() {
	if err := os.MkdirAll(alertsDir(), 0755); err != nil {
		log.Printf("Failed to create alerts directory: %v", err)
	}
}

func userAlertFile(username string) string {
	return filepath.Join(alertsDir(), username+".json")
}

func loadUserAlerts(username string) UserAlertConfig {
	alertsMu.RLock()
	defer alertsMu.RUnlock()

	data, err := os.ReadFile(userAlertFile(username))
	if err != nil {
		return UserAlertConfig{}
	}

	var cfg UserAlertConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		log.Printf("Failed to parse alerts for %s: %v", username, err)
		return UserAlertConfig{}
	}
	return cfg
}

func saveUserAlerts(username string, cfg UserAlertConfig) error {
	alertsMu.Lock()
	defer alertsMu.Unlock()

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal alerts: %v", err)
	}
	return os.WriteFile(userAlertFile(username), data, 0644)
}

func loadAllUserAlerts() map[string]UserAlertConfig {
	alertsMu.RLock()
	defer alertsMu.RUnlock()

	result := make(map[string]UserAlertConfig)
	entries, err := os.ReadDir(alertsDir())
	if err != nil {
		return result
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if len(name) < 6 || name[len(name)-5:] != ".json" {
			continue
		}
		username := name[:len(name)-5]
		data, err := os.ReadFile(filepath.Join(alertsDir(), name))
		if err != nil {
			continue
		}
		var cfg UserAlertConfig
		if err := json.Unmarshal(data, &cfg); err != nil {
			continue
		}
		result[username] = cfg
	}
	return result
}

func sendUserTelegramNotification(tg TelegramAlertConfig, text string) error {
	if !tg.Enabled || tg.BotToken == "" || tg.ChatID == "" {
		return fmt.Errorf("telegram not configured")
	}
	return sendTelegramMessageWithCreds(tg.BotToken, tg.ChatID, text)
}

func notifySubscribedUsers(scanName string, jobs []Job) {
	allAlerts := loadAllUserAlerts()
	for username, cfg := range allAlerts {
		if cfg.Telegram.Enabled && cfg.Telegram.Verified && cfg.Telegram.BotToken != "" && cfg.Telegram.ChatID != "" {
			if err := sendUserTelegramJobAlert(cfg.Telegram, scanName, jobs); err != nil {
				log.Printf("Failed to notify %s via Telegram: %v", username, err)
			} else {
				log.Printf("Sent Telegram alert to %s (%d jobs)", username, len(jobs))
			}
		}
	}
}

func sendUserTelegramJobAlert(tg TelegramAlertConfig, scanName string, jobs []Job) error {
	if len(jobs) == 0 {
		return nil
	}

	batchSize := 10
	for i := 0; i < len(jobs); i += batchSize {
		end := i + batchSize
		if end > len(jobs) {
			end = len(jobs)
		}
		batch := jobs[i:end]

		var msg string
		if i == 0 {
			msg = fmt.Sprintf("🎯 *SalarySleuth Alert*\n\n")
			msg += fmt.Sprintf("📋 Schedule: *%s*\n", escapeMarkdown(scanName))
			msg += fmt.Sprintf("Found *%d* new job\\(s\\)\\!\n\n", len(jobs))
			msg += "━━━━━━━━━━━━━━━━━━━━\n\n"
		}

		for j, job := range batch {
			msg += fmt.Sprintf("*%d\\.* %s\n", i+j+1, escapeMarkdown(job.Title))
			msg += fmt.Sprintf("   🏢 %s\n", escapeMarkdown(job.Company))
			if job.Location != "" {
				msg += fmt.Sprintf("   📍 %s\n", escapeMarkdown(job.Location))
			}
			if job.LevelSalary != "" {
				msg += fmt.Sprintf("   💰 %s\n", escapeMarkdown(job.LevelSalary))
			} else if job.SalaryRange != "" && job.SalaryRange != "Not Available" {
				msg += fmt.Sprintf("   💰 %s\n", escapeMarkdown(job.SalaryRange))
			}
			msg += fmt.Sprintf("   🔗 [Apply](%s)\n\n", job.URL)
		}

		if end == len(jobs) {
			msg += "━━━━━━━━━━━━━━━━━━━━\n"
			msg += fmt.Sprintf("📅 %s", escapeMarkdown(time.Now().Format("Jan 2, 2006 3:04 PM")))
		}

		if err := sendUserTelegramNotification(tg, msg); err != nil {
			return err
		}

		if end < len(jobs) {
			time.Sleep(2 * time.Second)
		}
	}
	return nil
}
