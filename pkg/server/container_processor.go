package server

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"instorage-container-processor/pkg/docker"
)

// SetupRoutes configures HTTP routes for receiving requests from instorage-manager
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

	// API routes for instorage-manager communication
	api := router.Group("/api/v1")
	{
		api.POST("/containers", p.SubmitJob)                 // instorage-manager submits container execution jobs
		api.GET("/containers/:jobId/status", p.GetJobStatus) // get job execution status
		api.DELETE("/containers/:jobId", p.CancelJob)        // cancel running job
		api.GET("/containers", p.ListJobs)                   // list all jobs
		api.GET("/health", p.HealthCheck)                    // health check endpoint
	}

	return router
}

// SubmitJob handles container job submission requests from instorage-manager
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

	p.logger.Info("Received container job submission from instorage-manager",
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
			Message: fmt.Sprintf("Container job %s already exists", req.JobID),
			JobID:   req.JobID,
		})
		return
	}

	// Create job state
	ctx, cancel := context.WithCancel(context.Background())
	jobState := &JobState{
		JobRequest:    &req,
		Status:        "pending",
		Message:       "Container job received and queued for processing",
		StartTime:     time.Now(),
		CancelContext: cancel,
	}

	// Store job state
	p.jobMux.Lock()
	p.jobs[req.JobID] = jobState
	p.jobMux.Unlock()

	// Start container processing asynchronously
	go p.processContainerJob(ctx, req.JobID)

	c.JSON(http.StatusAccepted, ContainerJobResponse{
		Success: true,
		Message: "Container job submitted successfully",
		JobID:   req.JobID,
	})
}

// GetJobStatus returns current container job status
func (p *ContainerProcessor) GetJobStatus(c *gin.Context) {
	jobId := c.Param("jobId")

	p.jobMux.RLock()
	jobState, exists := p.jobs[jobId]
	p.jobMux.RUnlock()

	if !exists {
		c.JSON(http.StatusNotFound, gin.H{
			"error": fmt.Sprintf("Container job %s not found", jobId),
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

// CancelJob cancels a running or pending container job
func (p *ContainerProcessor) CancelJob(c *gin.Context) {
	jobId := c.Param("jobId")

	p.jobMux.Lock()
	jobState, exists := p.jobs[jobId]
	if !exists {
		p.jobMux.Unlock()
		c.JSON(http.StatusNotFound, gin.H{
			"error": fmt.Sprintf("Container job %s not found", jobId),
		})
		return
	}

	// Cancel the context to stop processing
	if jobState.CancelContext != nil {
		jobState.CancelContext()
	}

	// Try to stop the container if it's running using our wrapper client
	if jobState.ContainerID != "" && jobState.Status == "running" {
		if err := p.dockerClient.StopContainer(context.Background(), jobState.ContainerID); err != nil {
			p.logger.Error(err, "Failed to stop container", "jobId", jobId, "containerId", jobState.ContainerID)
		}
	}

	jobState.Status = "cancelled"
	jobState.Message = "Container job cancelled by instorage-manager request"
	jobState.EndTime = time.Now()
	p.jobMux.Unlock()

	p.logger.Info("Container job cancelled", "jobId", jobId)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Container job cancelled successfully",
		"job_id":  jobId,
	})
}

// ListJobs returns all container jobs
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
		"jobs":  jobs,
		"count": len(jobs),
	})
}

// HealthCheck returns server health status
func (p *ContainerProcessor) HealthCheck(c *gin.Context) {
	// Use a simple ping context instead of calling dockerClient.Ping directly
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Try to get container status with invalid ID to test Docker connectivity
	_, err := p.dockerClient.GetContainerStatus(ctx, "health-check-test")
	// We expect an error here, but if Docker is unreachable, we'll get a different error
	dockerConnected := err != nil && fmt.Sprintf("%v", err) != "failed to connect to Docker daemon"

	if dockerConnected {
		c.JSON(http.StatusOK, gin.H{
			"status":    "healthy",
			"timestamp": time.Now().Unix(),
			"service":   "instorage-container-processor",
		})
		return
	}

	// If we get any other error, Docker might be unreachable
	c.JSON(http.StatusServiceUnavailable, gin.H{
		"status": "unhealthy",
		"error":  fmt.Sprintf("Docker daemon unreachable: %v", err),
	})
}

// processContainerJob executes the container job requested by instorage-manager
func (p *ContainerProcessor) processContainerJob(ctx context.Context, jobId string) {
	p.logger.Info("Starting container job processing", "jobId", jobId)

	// Get job state
	p.jobMux.RLock()
	jobState, exists := p.jobs[jobId]
	p.jobMux.RUnlock()

	if !exists {
		p.logger.Error(fmt.Errorf("job not found"), "Failed to process container job", "jobId", jobId)
		return
	}

	// Update status to running
	p.jobMux.Lock()
	jobState.Status = "running"
	jobState.Message = "Creating and starting container"
	p.jobMux.Unlock()

	// Notify instorage-manager of status change
	p.notifyInstorageManager(jobId, "running", "Container is starting")

	// Create and run container using our wrapper client
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

	// Notify instorage-manager of final status
	p.notifyInstorageManager(jobId, finalStatus, finalMessage)

	p.logger.Info("Container job processing completed", "jobId", jobId, "status", finalStatus)
}

// runContainer creates and executes a Docker container for the job using our wrapper client
func (p *ContainerProcessor) runContainer(ctx context.Context, jobState *JobState) error {
	req := jobState.JobRequest

	// Build environment variables
	env := make([]string, 0)
	for key, value := range req.Environment {
		env = append(env, fmt.Sprintf("%s=%s", key, value))
	}

	// Create volumes for data and output paths
	volumes := []docker.VolumeMount{
		{
			HostPath:      req.DataPath,
			ContainerPath: "/data",
			ReadOnly:      true,
		},
		{
			HostPath:      req.OutputPath,
			ContainerPath: "/output",
			ReadOnly:      false,
		},
	}

	// Create container configuration using our wrapper types
	config := docker.ContainerConfig{
		Image:   req.Image,
		Env:     env,
		Volumes: volumes,
	}

	// Add resource limits if specified
	if req.Resources != nil {
		if req.Resources.MemoryLimit != "" {
			if memBytes, err := p.parseMemory(req.Resources.MemoryLimit); err == nil {
				config.Resources.MemoryLimit = memBytes
			}
		}
		if req.Resources.CPULimit != "" {
			if cpuQuota, err := p.parseCPU(req.Resources.CPULimit); err == nil {
				config.Resources.CPULimit = cpuQuota
			}
		}
	}

	// Create container using our wrapper client
	containerID, err := p.dockerClient.CreateContainer(ctx, config)
	if err != nil {
		return fmt.Errorf("failed to create container: %w", err)
	}

	p.jobMux.Lock()
	jobState.ContainerID = containerID
	p.jobMux.Unlock()

	p.logger.Info("Container created", "jobId", req.JobID, "containerId", containerID)

	// Start container using our wrapper client
	if err := p.dockerClient.StartContainer(ctx, containerID); err != nil {
		return fmt.Errorf("failed to start container: %w", err)
	}

	p.logger.Info("Container started", "jobId", req.JobID, "containerId", containerID)

	// Monitor container status until completion
	for {
		select {
		case <-ctx.Done():
			// Context was cancelled, job was cancelled
			p.logger.Info("Container job processing cancelled", "jobId", req.JobID)
			return ctx.Err()
		case <-time.After(5 * time.Second):
			// Check container status every 5 seconds
			status, err := p.dockerClient.GetContainerStatus(ctx, containerID)
			if err != nil {
				return fmt.Errorf("failed to get container status: %w", err)
			}

			switch status {
			case docker.ContainerStatusExited:
				// Container finished, get exit code
				exitCode, err := p.dockerClient.GetContainerExitCode(ctx, containerID)
				if err != nil {
					return fmt.Errorf("failed to get container exit code: %w", err)
				}

				p.jobMux.Lock()
				jobState.ExitCode = exitCode
				p.jobMux.Unlock()

				if exitCode != 0 {
					return fmt.Errorf("container exited with code %d", exitCode)
				}

				p.logger.Info("Container completed successfully", "jobId", req.JobID, "containerId", containerID)
				return nil

			case docker.ContainerStatusError:
				return fmt.Errorf("container encountered an error")

			case docker.ContainerStatusRunning:
				// Container still running, continue monitoring
				continue

			default:
				// Unknown status, continue monitoring
				continue
			}
		}
	}
}
