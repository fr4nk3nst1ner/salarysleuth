package main

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"
)

var (
	schedulerMu      sync.Mutex
	schedulerRunning bool
)

func startScheduler() {
	schedulerMu.Lock()
	if schedulerRunning {
		schedulerMu.Unlock()
		return
	}
	schedulerRunning = true
	schedulerMu.Unlock()

	log.Println("Scan scheduler started (checks every 60 seconds)")
	go schedulerLoop()
}

func schedulerLoop() {
	for {
		time.Sleep(60 * time.Second)
		checkAndRunSchedules()
	}
}

func checkAndRunSchedules() {
	now := time.Now()
	allAlerts := loadAllUserAlerts()

	for username, cfg := range allAlerts {
		if !cfg.Telegram.Enabled || !cfg.Telegram.Verified {
			continue
		}

		for i, sched := range cfg.Schedules {
			if !sched.Enabled {
				continue
			}
			if !shouldRunNow(sched, now) {
				continue
			}

			log.Printf("Scheduler: running scan '%s' for user %s", sched.Name, username)
			go executeScheduledScan(username, cfg, i)
		}
	}
}

func shouldRunNow(sched ScheduledScan, now time.Time) bool {
	if now.Hour() != sched.Hour || now.Minute() != sched.Minute {
		return false
	}

	if sched.LastRun != "" {
		lastRun, err := time.Parse(time.RFC3339, sched.LastRun)
		if err == nil && now.Sub(lastRun) < 23*time.Hour {
			return false
		}
	}

	weekday := int(now.Weekday())

	switch sched.Schedule {
	case "daily":
		return true
	case "weekdays":
		return weekday >= 1 && weekday <= 5
	case "weekly":
		return weekday == 1 // Monday
	case "custom":
		for _, d := range sched.Days {
			if d == weekday {
				return true
			}
		}
		return false
	}
	return false
}

func executeScheduledScan(username string, cfg UserAlertConfig, schedIdx int) {
	sched := cfg.Schedules[schedIdx]
	var jobs []Job
	var err error
	var resultMsg string

	if sched.Type == "default" {
		jobs, err = runScheduledDefaultScan()
		if err != nil {
			resultMsg = fmt.Sprintf("Error: %v", err)
			log.Printf("Scheduler: default scan failed for %s: %v", username, err)
		} else {
			resultMsg = fmt.Sprintf("Found %d jobs", len(jobs))
		}
	} else if sched.Type == "custom" && sched.Query != "" {
		jobs, err = runScheduledCustomScan(sched.Query)
		if err != nil {
			resultMsg = fmt.Sprintf("Error: %v", err)
			log.Printf("Scheduler: custom scan '%s' failed for %s: %v", sched.Query, username, err)
		} else {
			resultMsg = fmt.Sprintf("Found %d jobs for '%s'", len(jobs), sched.Query)
		}
	}

	cfg.Schedules[schedIdx].LastRun = time.Now().Format(time.RFC3339)
	cfg.Schedules[schedIdx].LastResult = resultMsg
	if err := saveUserAlerts(username, cfg); err != nil {
		log.Printf("Scheduler: failed to save state for %s: %v", username, err)
	}

	if len(jobs) > 0 {
		if err := sendUserTelegramJobAlert(cfg.Telegram, sched.Name, jobs); err != nil {
			log.Printf("Scheduler: failed to send alert to %s: %v", username, err)
		} else {
			log.Printf("Scheduler: sent %d jobs to %s for '%s'", len(jobs), username, sched.Name)
		}
	} else if sched.NotifyEmpty && err == nil {
		noResultMsg := fmt.Sprintf("📋 *SalarySleuth Schedule*\n\n*%s*\n\nNo new jobs found this scan\\.\n\n📅 %s",
			escapeMarkdown(sched.Name),
			escapeMarkdown(time.Now().Format("Jan 2, 2006 3:04 PM")))
		_ = sendUserTelegramNotification(cfg.Telegram, noResultMsg)
	}
}

func runScheduledDefaultScan() ([]Job, error) {
	jobs, err := runSalarySleuth(config.Description, config.Pages)
	if err != nil {
		return nil, err
	}
	filteredJobs := filterOffsecJobs(jobs)
	cfg, _ := LoadConfig()
	tagged := TagJobs(filteredJobs, cfg)
	result := make([]Job, len(tagged))
	for i, t := range tagged {
		result[i] = t.Job
	}
	return result, nil
}

func runScheduledCustomScan(query string) ([]Job, error) {
	sources := []string{"linkedin", "greenhouse", "lever"}
	perSourceTimeout := 3 * time.Minute
	var allJobs []Job
	var mu sync.Mutex
	var wg sync.WaitGroup

	for _, src := range sources {
		wg.Add(1)
		go func(source string) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), perSourceTimeout)
			defer cancel()

			srcJobs, err := runSingleSourceSearch(ctx, query, config.Pages, source)
			if err != nil {
				log.Printf("Scheduler: source %s error for '%s': %v", source, query, err)
				return
			}
			if len(srcJobs) > 0 {
				mu.Lock()
				allJobs = append(allJobs, srcJobs...)
				mu.Unlock()
			}
		}(src)
	}

	wg.Wait()
	return allJobs, nil
}
