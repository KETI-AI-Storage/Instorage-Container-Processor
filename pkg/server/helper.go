package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// notifyInstorageManager sends status updates to instorage-manager via webhook
func (p *ContainerProcessor) notifyInstorageManager(jobID, status, message string) {
	p.logger.V(1).Info("Sending status update to instorage-manager",
		"jobId", jobID,
		"status", status,
		"message", message,
		"managerURL", p.managerURL,
	)

	// Skip if manager URL is not configured
	if p.managerURL == "" {
		p.logger.V(1).Info("Manager URL not configured, skipping webhook notification", "jobId", jobID)
		return
	}

	// Get job state for additional information
	p.jobMux.RLock()
	jobState, exists := p.jobs[jobID]
	p.jobMux.RUnlock()

	if !exists {
		p.logger.Error(fmt.Errorf("job not found"), "Cannot send webhook for unknown job", "jobId", jobID)
		return
	}

	// Create webhook payload
	webhook := ContainerStatusWebhook{
		JobID:       jobID,
		Status:      status,
		Message:     message,
		ContainerID: jobState.ContainerID,
		Timestamp:   time.Now().UTC().Format(time.RFC3339),
	}

	// Add error message and exit code if available
	if jobState.ErrorMessage != "" {
		webhook.ErrorMessage = jobState.ErrorMessage
	}
	if status == "completed" || status == "failed" {
		webhook.ExitCode = &jobState.ExitCode
	}

	// Send webhook in a goroutine to avoid blocking
	go func() {
		err := p.sendWebhook(webhook)
		if err != nil {
			p.logger.Error(err, "Failed to send webhook to instorage-manager",
				"jobId", jobID,
				"status", status,
				"managerURL", p.managerURL,
			)
		} else {
			p.logger.Info("Successfully sent webhook to instorage-manager",
				"jobId", jobID,
				"status", status,
			)
		}
	}()
}

// sendWebhook performs the actual HTTP POST to instorage-manager
func (p *ContainerProcessor) sendWebhook(webhook ContainerStatusWebhook) error {
	// Marshal webhook payload
	jsonData, err := json.Marshal(webhook)
	if err != nil {
		return fmt.Errorf("failed to marshal webhook payload: %w", err)
	}

	// Create webhook URL
	webhookURL := fmt.Sprintf("%s/api/v1/webhook/container/status", p.managerURL)

	// Create HTTP request
	req, err := http.NewRequest("POST", webhookURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to create webhook request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	// Send request
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send webhook request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("webhook returned status %d", resp.StatusCode)
	}

	return nil
}

// parseCPU parses CPU limit strings and converts them to Docker format
// Supports various formats: "100m", "0.1", "1", "1.5", "200m", etc.
// Returns CPU quota in microseconds (for use with Docker's CpuQuota)
func (p *ContainerProcessor) parseCPU(cpuLimit string) (int64, error) {
	if cpuLimit == "" {
		return 0, fmt.Errorf("CPU limit cannot be empty")
	}

	cpuLimit = strings.TrimSpace(cpuLimit)

	// Handle millicores (e.g., "100m", "250m", "1000m")
	if strings.HasSuffix(cpuLimit, "m") {
		milliStr := strings.TrimSuffix(cpuLimit, "m")
		milli, err := strconv.ParseFloat(milliStr, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid millicores format: %s", cpuLimit)
		}
		if milli <= 0 {
			return 0, fmt.Errorf("CPU limit must be positive: %s", cpuLimit)
		}
		// Convert millicores to CPU quota
		// Docker's CpuQuota is in microseconds per 100ms period
		// 1000m = 1 CPU = 100000 microseconds
		return int64(milli * 100), nil
	}

	// Handle decimal values (e.g., "0.1", "1", "2.5")
	cpu, err := strconv.ParseFloat(cpuLimit, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid CPU format: %s", cpuLimit)
	}
	if cpu <= 0 {
		return 0, fmt.Errorf("CPU limit must be positive: %s", cpuLimit)
	}

	// Convert to CPU quota (1 CPU = 100000 microseconds)
	return int64(cpu * 100000), nil
}

// parseMemory parses memory limit strings and converts them to bytes
// Supports various formats: "128Mi", "1Gi", "512M", "1G", "1024", etc.
func (p *ContainerProcessor) parseMemory(memLimit string) (int64, error) {
	if memLimit == "" {
		return 0, fmt.Errorf("memory limit cannot be empty")
	}

	memLimit = strings.TrimSpace(memLimit)

	// Define regex patterns for different memory formats
	patterns := []struct {
		regex      *regexp.Regexp
		multiplier int64
	}{
		{regexp.MustCompile(`^(\d+(?:\.\d+)?)Ti?$`), 1024 * 1024 * 1024 * 1024}, // TiB
		{regexp.MustCompile(`^(\d+(?:\.\d+)?)T$`), 1000 * 1000 * 1000 * 1000},   // TB
		{regexp.MustCompile(`^(\d+(?:\.\d+)?)Gi?$`), 1024 * 1024 * 1024},        // GiB
		{regexp.MustCompile(`^(\d+(?:\.\d+)?)G$`), 1000 * 1000 * 1000},          // GB
		{regexp.MustCompile(`^(\d+(?:\.\d+)?)Mi?$`), 1024 * 1024},               // MiB
		{regexp.MustCompile(`^(\d+(?:\.\d+)?)M$`), 1000 * 1000},                 // MB
		{regexp.MustCompile(`^(\d+(?:\.\d+)?)Ki?$`), 1024},                      // KiB
		{regexp.MustCompile(`^(\d+(?:\.\d+)?)K$`), 1000},                        // KB
		{regexp.MustCompile(`^(\d+(?:\.\d+)?)$`), 1},                            // Bytes
	}

	for _, pattern := range patterns {
		if matches := pattern.regex.FindStringSubmatch(memLimit); matches != nil {
			value, err := strconv.ParseFloat(matches[1], 64)
			if err != nil {
				return 0, fmt.Errorf("invalid number format in memory limit: %s", memLimit)
			}
			if value <= 0 {
				return 0, fmt.Errorf("memory limit must be positive: %s", memLimit)
			}

			bytes := int64(value * float64(pattern.multiplier))

			// Validate reasonable memory limits (1MB to 1TB)
			const minMemory = 1024 * 1024               // 1MB
			const maxMemory = 1024 * 1024 * 1024 * 1024 // 1TB

			if bytes < minMemory {
				return 0, fmt.Errorf("memory limit too small (minimum 1MB): %s", memLimit)
			}
			if bytes > maxMemory {
				return 0, fmt.Errorf("memory limit too large (maximum 1TB): %s", memLimit)
			}

			return bytes, nil
		}
	}

	return 0, fmt.Errorf("invalid memory format: %s (supported formats: 128Mi, 1Gi, 512M, 1G, 1024)", memLimit)
}
