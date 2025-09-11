package server

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/gin-gonic/gin"
	"github.com/go-logr/logr"
)

// ContainerJobRequest represents incoming job request from instorage-manager
type ContainerJobRequest struct {
	JobID           string                 `json:"job_id" binding:"required"`
	JobName         string                 `json:"job_name" binding:"required"`
	Namespace       string                 `json:"namespace"`
	Image           string                 `json:"image" binding:"required"`
	ImagePullPolicy string                 `json:"image_pull_policy,omitempty"`
	DataPath        string                 `json:"data_path" binding:"required"`
	OutputPath      string                 `json:"output_path" binding:"required"`
	Environment     map[string]string      `json:"environment,omitempty"`
	Resources       *ContainerResources    `json:"resources,omitempty"`
	Labels          map[string]string      `json:"labels,omitempty"`
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

// ContainerProcessor manages Docker containers for job execution
type ContainerProcessor struct {
	logger       logr.Logger
	dockerClient *client.Client
	operatorURL  string
	
	// Job state management
	jobs   map[string]*JobState
	jobMux sync.RWMutex
	
	// HTTP client for operator communication
	httpClient *http.Client
}

// NewContainerProcessor creates a new container processor instance
func NewContainerProcessor(logger logr.Logger, operatorURL string) (*ContainerProcessor, error) {
	dockerClient, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("failed to create Docker client: %w", err)
	}

	return &ContainerProcessor{
		logger:       logger,
		dockerClient: dockerClient,
		operatorURL:  operatorURL,
		jobs:         make(map[string]*JobState),
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}, nil
}

// SetupRoutes configures HTTP routes for the server
func (p *ContainerProcessor) SetupRoutes() *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(gin.Recovery())
	
	// Logging middleware
	router.Use(gin.LoggerWithFormatter(func(param gin.LogFormatterParams) string {
		return fmt.Sprintf("%s - [%s] \"%s %s %s %d %s \"%s\" %s\"\n",
			param.ClientIP,
			param.TimeStamp.Format(time.RFC3339),
			param.Method,
			param.Path,
			param.Request.Proto,
			param.StatusCode,
			param.Latency,
			param.Request.UserAgent(),
			param.ErrorMessage,
		)
	}))

	// API routes
	api := router.Group("/api/v1")
	{
		api.POST("/jobs", p.SubmitJob)
		api.GET("/jobs/:jobId/status", p.GetJobStatus)
		api.POST("/jobs/:jobId/cancel", p.CancelJob)
		api.GET("/jobs", p.ListJobs)
		api.GET("/health", p.HealthCheck)
	}

	return router
}

// SubmitJob handles job submission requests
func (p *ContainerProcessor) SubmitJob(c *gin.Context) {
	var req ContainerJobRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, ContainerJobResponse{
			Success: false,
			Message: fmt.Sprintf("Invalid request: %v", err),
			JobID:   req.JobID,
		})
		return
	}

	p.logger.Info("Received job submission request",
		"jobId", req.JobID,
		"jobName", req.JobName,
		"image", req.Image,
		"dataPath", req.DataPath,
		"outputPath", req.OutputPath,
	)

	// Check if job already exists
	p.jobMux.RLock()
	_, exists := p.jobs[req.JobID]
	p.jobMux.RUnlock()

	if exists {
		c.JSON(http.StatusConflict, ContainerJobResponse{
			Success: false,
			Message: fmt.Sprintf("Job %s already exists", req.JobID),
			JobID:   req.JobID,
		})
		return
	}

	// Create job state
	ctx, cancel := context.WithCancel(context.Background())
	jobState := &JobState{
		JobRequest:    &req,
		Status:        "pending",
		Message:       "Job received and queued for processing",
		StartTime:     time.Now(),
		CancelContext: cancel,
	}

	// Store job state
	p.jobMux.Lock()
	p.jobs[req.JobID] = jobState
	p.jobMux.Unlock()

	// Start job processing asynchronously
	go p.processJob(ctx, req.JobID)

	c.JSON(http.StatusAccepted, ContainerJobResponse{
		Success: true,
		Message: "Job submitted successfully",
		JobID:   req.JobID,
	})
}

// GetJobStatus returns current job status
func (p *ContainerProcessor) GetJobStatus(c *gin.Context) {
	jobId := c.Param("jobId")

	p.jobMux.RLock()
	jobState, exists := p.jobs[jobId]
	p.jobMux.RUnlock()

	if !exists {
		c.JSON(http.StatusNotFound, gin.H{
			"error": fmt.Sprintf("Job %s not found", jobId),
		})
		return
	}

	status := ContainerJobStatus{
		JobID:        jobId,
		ContainerID:  jobState.ContainerID,
		Status:       jobState.Status,
		Message:      jobState.Message,
		StartTime:    jobState.StartTime,
		EndTime:      jobState.EndTime,
		ExitCode:     jobState.ExitCode,
		OutputPath:   jobState.OutputPath,
		ErrorMessage: jobState.ErrorMessage,
	}

	c.JSON(http.StatusOK, status)
}

// CancelJob cancels a running or pending job
func (p *ContainerProcessor) CancelJob(c *gin.Context) {
	jobId := c.Param("jobId")

	p.jobMux.Lock()
	jobState, exists := p.jobs[jobId]
	if !exists {
		p.jobMux.Unlock()
		c.JSON(http.StatusNotFound, gin.H{
			"error": fmt.Sprintf("Job %s not found", jobId),
		})
		return
	}

	// Cancel the context to stop processing
	if jobState.CancelContext != nil {
		jobState.CancelContext()
	}

	// Try to stop the container if it's running
	if jobState.ContainerID != "" && jobState.Status == "running" {
		timeout := 30
		if err := p.dockerClient.ContainerStop(context.Background(), jobState.ContainerID, container.StopOptions{
			Timeout: &timeout,
		}); err != nil {
			p.logger.Error(err, "Failed to stop container", "jobId", jobId, "containerId", jobState.ContainerID)
		}
	}

	jobState.Status = "cancelled"
	jobState.Message = "Job cancelled by user request"
	jobState.EndTime = time.Now()
	p.jobMux.Unlock()

	p.logger.Info("Job cancelled", "jobId", jobId)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Job cancelled successfully",
		"job_id":  jobId,
	})
}

// ListJobs returns all jobs
func (p *ContainerProcessor) ListJobs(c *gin.Context) {
	p.jobMux.RLock()
	defer p.jobMux.RUnlock()

	jobs := make([]ContainerJobStatus, 0, len(p.jobs))
	for jobId, jobState := range p.jobs {
		status := ContainerJobStatus{
			JobID:        jobId,
			ContainerID:  jobState.ContainerID,
			Status:       jobState.Status,
			Message:      jobState.Message,
			StartTime:    jobState.StartTime,
			EndTime:      jobState.EndTime,
			ExitCode:     jobState.ExitCode,
			OutputPath:   jobState.OutputPath,
			ErrorMessage: jobState.ErrorMessage,
		}
		jobs = append(jobs, status)
	}

	c.JSON(http.StatusOK, gin.H{
		"jobs": jobs,
		"count": len(jobs),
	})
}

// HealthCheck returns server health status
func (p *ContainerProcessor) HealthCheck(c *gin.Context) {
	// Check Docker daemon connectivity
	_, err := p.dockerClient.Ping(context.Background())
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"status": "unhealthy",
			"error":  fmt.Sprintf("Docker daemon unreachable: %v", err),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status": "healthy",
		"timestamp": time.Now().Unix(),
	})
}

// processJob executes the job in a Docker container
func (p *ContainerProcessor) processJob(ctx context.Context, jobId string) {
	p.logger.Info("Starting job processing", "jobId", jobId)

	// Get job state
	p.jobMux.RLock()
	jobState, exists := p.jobs[jobId]
	p.jobMux.RUnlock()

	if !exists {
		p.logger.Error(fmt.Errorf("job not found"), "Failed to process job", "jobId", jobId)
		return
	}

	// Update status to running
	p.jobMux.Lock()
	jobState.Status = "running"
	jobState.Message = "Creating and starting container"
	p.jobMux.Unlock()

	// Notify operator of status change
	p.notifyOperator(jobId, "running", "Container is starting")

	// Create and run container
	err := p.runContainer(ctx, jobState)
	
	// Update final status
	p.jobMux.Lock()
	if err != nil {
		jobState.Status = "failed"
		jobState.Message = "Container execution failed"
		jobState.ErrorMessage = err.Error()
		jobState.EndTime = time.Now()
	} else if jobState.Status != "cancelled" {
		jobState.Status = "completed"
		jobState.Message = "Container execution completed successfully"
		jobState.OutputPath = jobState.JobRequest.OutputPath
		jobState.EndTime = time.Now()
	}
	finalStatus := jobState.Status
	finalMessage := jobState.Message
	p.jobMux.Unlock()

	// Notify operator of final status
	p.notifyOperator(jobId, finalStatus, finalMessage)

	p.logger.Info("Job processing completed", "jobId", jobId, "status", finalStatus)
}

// runContainer creates and executes a Docker container for the job
func (p *ContainerProcessor) runContainer(ctx context.Context, jobState *JobState) error {
	req := jobState.JobRequest

	// Build environment variables
	env := make([]string, 0)
	for key, value := range req.Environment {
		env = append(env, fmt.Sprintf("%s=%s", key, value))
	}

	// Create container configuration
	config := &container.Config{
		Image: req.Image,
		Env:   env,
		Labels: map[string]string{
			"instorage.job.id":        req.JobID,
			"instorage.job.name":      req.JobName,
			"instorage.job.namespace": req.Namespace,
		},
	}

	// Add user labels
	if req.Labels != nil {
		for key, value := range req.Labels {
			config.Labels[key] = value
		}
	}

	hostConfig := &container.HostConfig{
		AutoRemove: true, // Remove container after completion
	}

	// Add resource limits if specified
	if req.Resources != nil {
		if req.Resources.MemoryLimit != "" {
			if memBytes, err := p.parseMemory(req.Resources.MemoryLimit); err == nil {
				hostConfig.Memory = memBytes
			}
		}
		if req.Resources.CPULimit != "" {
			if cpuPeriod, cpuQuota, err := p.parseCPU(req.Resources.CPULimit); err == nil {
				hostConfig.CPUPeriod = cpuPeriod
				hostConfig.CPUQuota = cpuQuota
			}
		}
	}

	// Add bind mounts for data and output paths
	hostConfig.Binds = []string{
		fmt.Sprintf("%s:%s:ro", req.DataPath, "/data"), // Read-only data mount
		fmt.Sprintf("%s:%s", req.OutputPath, "/output"), // Read-write output mount
	}

	// Create container
	resp, err := p.dockerClient.ContainerCreate(ctx, config, hostConfig, nil, nil, "")
	if err != nil {
		return fmt.Errorf("failed to create container: %w", err)
	}

	containerID := resp.ID
	p.jobMux.Lock()
	jobState.ContainerID = containerID
	p.jobMux.Unlock()

	p.logger.Info("Container created", "jobId", req.JobID, "containerId", containerID)

	// Start container
	if err := p.dockerClient.ContainerStart(ctx, containerID, container.StartOptions{}); err != nil {
		return fmt.Errorf("failed to start container: %w", err)
	}

	p.logger.Info("Container started", "jobId", req.JobID, "containerId", containerID)

	// Wait for container to complete
	statusCh, errCh := p.dockerClient.ContainerWait(ctx, containerID, container.WaitConditionNotRunning)
	
	select {
	case err := <-errCh:
		if err != nil {
			return fmt.Errorf("error waiting for container: %w", err)
		}
	case status := <-statusCh:
		p.jobMux.Lock()
		jobState.ExitCode = int(status.StatusCode)
		p.jobMux.Unlock()
		
		if status.StatusCode != 0 {
			return fmt.Errorf("container exited with code %d", status.StatusCode)
		}
	case <-ctx.Done():
		// Context was cancelled, job was cancelled
		p.logger.Info("Job processing cancelled", "jobId", req.JobID)
		return ctx.Err()
	}

	p.logger.Info("Container completed successfully", "jobId", req.JobID, "containerId", containerID)
	return nil
}

// notifyOperator sends job status updates back to the operator
func (p *ContainerProcessor) notifyOperator(jobId, status, message string) {
	if p.operatorURL == "" {
		p.logger.V(1).Info("No operator URL configured, skipping notification", "jobId", jobId, "status", status)
		return
	}

	// This would be implemented to call back to the operator
	// For now, just log the status update
	p.logger.Info("Notifying operator of job status change",
		"jobId", jobId,
		"status", status,
		"message", message,
		"operatorURL", p.operatorURL,
	)

	// TODO: Implement actual HTTP callback to operator
	// This would involve making HTTP requests to update the job status in Kubernetes
}

// Helper functions for resource parsing
func (p *ContainerProcessor) parseMemory(memStr string) (int64, error) {
	// Simple implementation - in production, use proper parsing
	// For now, assume format like "512Mi", "1Gi", etc.
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

func (p *ContainerProcessor) parseCPU(cpuStr string) (int64, int64, error) {
	// Simple implementation - assume format like "0.5", "1", "2" (CPU cores)
	value, err := strconv.ParseFloat(cpuStr, 64)
	if err != nil {
		return 0, 0, err
	}
	
	// Docker uses period/quota system
	// Period is typically 100000 microseconds (100ms)
	// Quota is period * CPU cores
	period := int64(100000)
	quota := int64(value * float64(period))
	
	return period, quota, nil
}