package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"
)

// TelegramMessage represents a message to send via Telegram
type TelegramMessage struct {
	ChatID    string `json:"chat_id"`
	Text      string `json:"text"`
	ParseMode string `json:"parse_mode"`
}

// sendTelegramNotifications sends notifications for new jobs
func sendTelegramNotifications(newJobs []Job) {
	if config.TelegramBotToken == "" || config.TelegramChatID == "" {
		log.Println("Telegram credentials not configured, skipping notifications")
		return
	}

	// Build a consolidated message to avoid rate limits
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("🎯 *SalarySleuth Alert*\n\n"))
	sb.WriteString(fmt.Sprintf("Found *%d new* offensive security job\\(s\\)\\!\n\n", len(newJobs)))
	sb.WriteString("━━━━━━━━━━━━━━━━━━━━\n\n")

	// Group jobs (max 10 per message to avoid Telegram message length limits)
	batchSize := 10
	for i := 0; i < len(newJobs); i += batchSize {
		end := i + batchSize
		if end > len(newJobs) {
			end = len(newJobs)
		}
		batch := newJobs[i:end]

		var batchMsg strings.Builder
		if i == 0 {
			batchMsg.WriteString(fmt.Sprintf("🎯 *SalarySleuth Alert*\n\n"))
			batchMsg.WriteString(fmt.Sprintf("Found *%d new* offensive security job\\(s\\)\\!\n\n", len(newJobs)))
			batchMsg.WriteString("━━━━━━━━━━━━━━━━━━━━\n\n")
		}

		for j, job := range batch {
			batchMsg.WriteString(fmt.Sprintf("*%d\\.* %s\n", i+j+1, escapeMarkdown(job.Title)))
			batchMsg.WriteString(fmt.Sprintf("   🏢 %s\n", escapeMarkdown(job.Company)))
			if job.Location != "" {
				batchMsg.WriteString(fmt.Sprintf("   📍 %s\n", escapeMarkdown(job.Location)))
			}
			batchMsg.WriteString(fmt.Sprintf("   🔗 [Apply](%s)\n\n", job.URL))
		}

		if end == len(newJobs) {
			batchMsg.WriteString("━━━━━━━━━━━━━━━━━━━━\n")
			batchMsg.WriteString(fmt.Sprintf("📅 %s", escapeMarkdown(time.Now().Format("Jan 2, 2006 3:04 PM"))))
		}

		if err := sendTelegramMessage(batchMsg.String()); err != nil {
			log.Printf("Failed to send batch notification: %v", err)
		}

		// Rate limit between batches
		if end < len(newJobs) {
			time.Sleep(2 * time.Second)
		}
	}
}

func formatJobMessage(job Job) string {
	var sb strings.Builder

	// Company and title
	sb.WriteString(fmt.Sprintf("🏢 *%s*\n", escapeMarkdown(job.Company)))
	sb.WriteString(fmt.Sprintf("💼 %s\n", escapeMarkdown(job.Title)))

	// Location
	if job.Location != "" {
		sb.WriteString(fmt.Sprintf("📍 %s\n", escapeMarkdown(job.Location)))
	}

	// Salary info
	if job.LevelSalary != "" && job.LevelSalary != "No Data" {
		sb.WriteString(fmt.Sprintf("💰 %s\n", escapeMarkdown(job.LevelSalary)))
	} else if job.SalaryRange != "" && job.SalaryRange != "Not Available" {
		sb.WriteString(fmt.Sprintf("💰 %s\n", escapeMarkdown(job.SalaryRange)))
	}

	// Source
	sb.WriteString(fmt.Sprintf("📊 Source: %s\n", escapeMarkdown(job.Source)))

	// URL - Telegram supports clickable links
	sb.WriteString(fmt.Sprintf("\n🔗 [Apply Here](%s)", job.URL))

	return sb.String()
}

func escapeMarkdown(text string) string {
	// Escape special characters for Telegram Markdown
	replacer := strings.NewReplacer(
		"_", "\\_",
		"*", "\\*",
		"[", "\\[",
		"]", "\\]",
		"(", "\\(",
		")", "\\)",
		"~", "\\~",
		"`", "\\`",
		">", "\\>",
		"#", "\\#",
		"+", "\\+",
		"-", "\\-",
		"=", "\\=",
		"|", "\\|",
		"{", "\\{",
		"}", "\\}",
		".", "\\.",
		"!", "\\!",
	)
	return replacer.Replace(text)
}

func sendTelegramMessage(text string) error {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", config.TelegramBotToken)

	msg := TelegramMessage{
		ChatID:    config.TelegramChatID,
		Text:      text,
		ParseMode: "MarkdownV2",
	}

	jsonData, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal message: %v", err)
	}

	resp, err := http.Post(url, "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to send request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// Try again with plain text if Markdown fails
		msg.ParseMode = ""
		msg.Text = stripMarkdown(text)
		jsonData, _ = json.Marshal(msg)
		resp2, err := http.Post(url, "application/json", bytes.NewBuffer(jsonData))
		if err != nil {
			return fmt.Errorf("failed to send request (retry): %v", err)
		}
		defer resp2.Body.Close()
		if resp2.StatusCode != http.StatusOK {
			return fmt.Errorf("telegram API returned status: %d", resp2.StatusCode)
		}
	}

	return nil
}

func stripMarkdown(text string) string {
	// Remove markdown formatting for plain text fallback
	replacer := strings.NewReplacer(
		"*", "",
		"_", "",
		"`", "",
		"[", "",
		"]", "",
	)
	// Also handle links
	text = replacer.Replace(text)
	return text
}

// SendTestMessage sends a test message to verify Telegram configuration
func SendTestMessage() error {
	msg := "🧪 *SalarySleuth Test*\n\nTelegram integration is working correctly\\!"
	return sendTelegramMessage(msg)
}
