package server

import (
	"context"
	"fmt"
	"instorage-container-processor/pkg/docker"
	"net/http"
	"sync"
	"time"

	"github.com/go-logr/logr"
	"go.uber.org/zap"
)

// ContainerJobRequest represents incoming job request from instorage-manager
type ContainerJobRequest struct {
	JobID           string              `json:"job_id" binding:"required"`
	JobName         string              `json:"job_name" binding:"required"`
	Namespace       string              `json:"namespace"`
	Image           string              `json:"image" binding:"required"`
	ImagePullPolicy string              `json:"image_pull_policy,omitempty"`
	DataPath        string              `json:"data_path" binding:"required"`
	OutputPath      string              `json:"output_path" binding:"required"`
	Environment     map[string]string   `json:"environment,omitempty"`
	Resources       *ContainerResources `json:"resources,omitempty"`
	Labels          map[string]string   `json:"labels,omitempty"`
}

// ContainerResources represents resource limits for containers
type ContainerResources struct {
	CPULimit      string `json:"cpu_limit,omitempty"`
	MemoryLimit   string `json:"memory_limit,omitempty"`
	CPURequest    string `json:"cpu_request,omitempty"`
	MemoryRequest string `json:"memory_request,omitempty"`
}

// ContainerJobResponse represents response to job submission
type ContainerJobResponse struct {
	Success     bool   `json:"success"`
	Message     string `json:"message"`
	JobID       string `json:"job_id"`
	ContainerID string `json:"container_id,omitempty"`
}

// ContainerJobStatus represents current job status
type ContainerJobStatus struct {
	JobID        string    `json:"job_id"`
	ContainerID  string    `json:"container_id,omitempty"`
	Status       string    `json:"status"` // pending, running, completed, failed, cancelled
	Message      string    `json:"message"`
	StartTime    time.Time `json:"start_time,omitempty"`
	EndTime      time.Time `json:"end_time,omitempty"`
	ExitCode     int       `json:"exit_code,omitempty"`
	OutputPath   string    `json:"output_path,omitempty"`
	ErrorMessage string    `json:"error_message,omitempty"`
}

// JobState represents internal job state
type JobState struct {
	JobRequest    *ContainerJobRequest
	ContainerID   string
	Status        string
	Message       string
	StartTime     time.Time
	EndTime       time.Time
	ExitCode      int
	OutputPath    string
	ErrorMessage  string
	CancelContext context.CancelFunc
}

// ContainerStatusWebhook represents webhook payload sent to instorage-manager
type ContainerStatusWebhook struct {
	JobID        string `json:"job_id"`
	Status       string `json:"status"` // running, completed, failed, cancelled
	Message      string `json:"message"`
	ContainerID  string `json:"container_id,omitempty"`
	ErrorMessage string `json:"error_message,omitempty"`
	ExitCode     *int   `json:"exit_code,omitempty"`
	Timestamp    string `json:"timestamp"`
}

// ContainerProcessor manages Docker containers for job execution from instorage-manager
type ContainerProcessor struct {
	logger       logr.Logger
	dockerClient *docker.Client
	operatorURL  string
	managerURL   string

	// Job state management
	jobs   map[string]*JobState
	jobMux sync.RWMutex

	// HTTP client for communication
	httpClient *http.Client
}

// NewContainerProcessor creates a new container processor instance
func NewContainerProcessor(logger logr.Logger, operatorURL string, managerURL string) (*ContainerProcessor, error) {
	// Convert logr.Logger to zap.Logger for docker client compatibility
	zapLogger, _ := zap.NewProduction()

	// Use our wrapper Docker client instead of direct Docker client
	dockerClient, err := docker.NewClient(zapLogger)
	if err != nil {
		return nil, fmt.Errorf("failed to create Docker client: %w", err)
	}

	return &ContainerProcessor{
		logger:       logger,
		dockerClient: dockerClient,
		operatorURL:  operatorURL,
		managerURL:   managerURL,
		jobs:         make(map[string]*JobState),
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}, nil
}
