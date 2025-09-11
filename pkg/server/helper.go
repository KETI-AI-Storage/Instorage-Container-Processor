package server

import (
	"fmt"
	"strconv"
)

// notifyInstorageManager sends job status updates back to instorage-manager
func (p *ContainerProcessor) notifyInstorageManager(jobId, status, message string) {
	if p.operatorURL == "" {
		p.logger.V(1).Info("No instorage-manager URL configured, skipping notification", "jobId", jobId, "status", status)
		return
	}

	// This would be implemented to call back to instorage-manager
	// For now, just log the status update
	p.logger.Info("Notifying instorage-manager of job status change",
		"jobId", jobId,
		"status", status,
		"message", message,
		"managerURL", p.operatorURL,
	)

	// TODO: Implement actual HTTP callback to instorage-manager
	// This would involve making HTTP requests to update the job status
}

// Helper functions for resource parsing
func (p *ContainerProcessor) parseMemory(memStr string) (int64, error) {
	// Parse Kubernetes-style memory limits (e.g., "512Mi", "1Gi", "2048Ki")
	if len(memStr) < 3 {
		return 0, fmt.Errorf("invalid memory format")
	}

	unit := memStr[len(memStr)-2:]
	valueStr := memStr[:len(memStr)-2]
	value, err := strconv.ParseInt(valueStr, 10, 64)
	if err != nil {
		return 0, err
	}

	switch unit {
	case "Ki":
		return value * 1024, nil
	case "Mi":
		return value * 1024 * 1024, nil
	case "Gi":
		return value * 1024 * 1024 * 1024, nil
	default:
		return 0, fmt.Errorf("unknown memory unit: %s", unit)
	}
}

func (p *ContainerProcessor) parseCPU(cpuStr string) (int64, error) {
	// Parse CPU limits (e.g., "0.5", "1", "2" representing CPU cores)
	// Return the quota value for Docker (period is handled in docker client)
	value, err := strconv.ParseFloat(cpuStr, 64)
	if err != nil {
		return 0, err
	}

	// Docker uses period/quota system
	// Period is typically 100000 microseconds (100ms)
	// Quota is period * CPU cores
	period := int64(100000)
	quota := int64(value * float64(period))

	return quota, nil
}
