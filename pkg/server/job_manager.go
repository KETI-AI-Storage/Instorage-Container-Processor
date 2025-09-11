package server

import (
	"context"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"

	"instorage-container-processor/pkg/docker"
)

type JobStatus string

const (
	JobStatusPending   JobStatus = "pending"
	JobStatusRunning   JobStatus = "running"
	JobStatusCompleted JobStatus = "completed"
	JobStatusFailed    JobStatus = "failed"
	JobStatusCancelled JobStatus = "cancelled"
)

type JobData struct {
	Path      string `json:"path"`
	MountPath string `json:"mountPath"`
}

type Job struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Namespace   string            `json:"namespace"`
	Image       string            `json:"image"`
	Command     []string          `json:"command,omitempty"`
	Args        []string          `json:"args,omitempty"`
	Resources   map[string]string `json:"resources,omitempty"`
	InputData   JobData           `json:"inputData"`
	OutputData  JobData           `json:"outputData"`
	Status      JobStatus         `json:"status"`
	Message     string            `json:"message,omitempty"`
	ContainerID string            `json:"containerId,omitempty"`
	CreatedAt   time.Time         `json:"createdAt"`
	StartedAt   *time.Time        `json:"startedAt,omitempty"`
	CompletedAt *time.Time        `json:"completedAt,omitempty"`
}

type JobManager struct {
	dockerClient *docker.Client
	logger       *zap.Logger
	jobs         map[string]*Job
	jobQueue     chan *Job
	mutex        sync.RWMutex
	ctx          context.Context
	cancel       context.CancelFunc
}

func NewJobManager(dockerClient *docker.Client, logger *zap.Logger) *JobManager {
	ctx, cancel := context.WithCancel(context.Background())
	
	return &JobManager{
		dockerClient: dockerClient,
		logger:       logger,
		jobs:         make(map[string]*Job),
		jobQueue:     make(chan *Job, 100),
		ctx:          ctx,
		cancel:       cancel,
	}
}

func (jm *JobManager) Start(ctx context.Context) {
	jm.logger.Info("Starting job manager")
	
	go jm.processJobs()
	go jm.monitorJobs()
	
	<-ctx.Done()
	jm.cancel()
	jm.logger.Info("Job manager stopped")
}

func (jm *JobManager) SubmitJob(job *Job) error {
	jm.mutex.Lock()
	defer jm.mutex.Unlock()
	
	jm.jobs[job.ID] = job
	
	select {
	case jm.jobQueue <- job:
		jm.logger.Info("Job submitted", zap.String("jobId", job.ID))
		return nil
	default:
		return fmt.Errorf("job queue is full")
	}
}

func (jm *JobManager) GetJob(jobID string) *Job {
	jm.mutex.RLock()
	defer jm.mutex.RUnlock()
	
	return jm.jobs[jobID]
}

func (jm *JobManager) CancelJob(jobID string) error {
	jm.mutex.Lock()
	defer jm.mutex.Unlock()
	
	job, exists := jm.jobs[jobID]
	if !exists {
		return fmt.Errorf("job not found")
	}
	
	if job.Status == JobStatusRunning && job.ContainerID != "" {
		if err := jm.dockerClient.StopContainer(jm.ctx, job.ContainerID); err != nil {
			jm.logger.Error("Failed to stop container", zap.String("containerId", job.ContainerID), zap.Error(err))
		}
	}
	
	job.Status = JobStatusCancelled
	job.Message = "Job cancelled by user"
	now := time.Now()
	job.CompletedAt = &now
	
	return nil
}

func (jm *JobManager) processJobs() {
	jm.logger.Info("Starting job processor")
	
	for {
		select {
		case <-jm.ctx.Done():
			return
		case job := <-jm.jobQueue:
			jm.processJob(job)
		}
	}
}

func (jm *JobManager) processJob(job *Job) {
	jm.logger.Info("Processing job", zap.String("jobId", job.ID))
	
	jm.mutex.Lock()
	job.Status = JobStatusRunning
	now := time.Now()
	job.StartedAt = &now
	jm.mutex.Unlock()
	
	containerConfig := docker.ContainerConfig{
		Image:   job.Image,
		Command: job.Command,
		Args:    job.Args,
		Volumes: []docker.VolumeMount{
			{
				HostPath:      job.InputData.Path,
				ContainerPath: job.InputData.MountPath,
				ReadOnly:      true,
			},
			{
				HostPath:      job.OutputData.Path,
				ContainerPath: job.OutputData.MountPath,
				ReadOnly:      false,
			},
		},
	}
	
	containerID, err := jm.dockerClient.CreateContainer(jm.ctx, containerConfig)
	if err != nil {
		jm.logger.Error("Failed to create container", zap.String("jobId", job.ID), zap.Error(err))
		jm.failJob(job, fmt.Sprintf("Failed to create container: %v", err))
		return
	}
	
	jm.mutex.Lock()
	job.ContainerID = containerID
	jm.mutex.Unlock()
	
	if err := jm.dockerClient.StartContainer(jm.ctx, containerID); err != nil {
		jm.logger.Error("Failed to start container", zap.String("jobId", job.ID), zap.Error(err))
		jm.failJob(job, fmt.Sprintf("Failed to start container: %v", err))
		return
	}
	
	jm.logger.Info("Container started", zap.String("jobId", job.ID), zap.String("containerId", containerID))
}

func (jm *JobManager) monitorJobs() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	
	for {
		select {
		case <-jm.ctx.Done():
			return
		case <-ticker.C:
			jm.checkRunningJobs()
		}
	}
}

func (jm *JobManager) checkRunningJobs() {
	jm.mutex.RLock()
	var runningJobs []*Job
	for _, job := range jm.jobs {
		if job.Status == JobStatusRunning && job.ContainerID != "" {
			runningJobs = append(runningJobs, job)
		}
	}
	jm.mutex.RUnlock()
	
	for _, job := range runningJobs {
		status, err := jm.dockerClient.GetContainerStatus(jm.ctx, job.ContainerID)
		if err != nil {
			jm.logger.Error("Failed to get container status", zap.String("jobId", job.ID), zap.Error(err))
			continue
		}
		
		switch status {
		case docker.ContainerStatusExited:
			exitCode, err := jm.dockerClient.GetContainerExitCode(jm.ctx, job.ContainerID)
			if err != nil {
				jm.logger.Error("Failed to get container exit code", zap.String("jobId", job.ID), zap.Error(err))
				jm.failJob(job, "Failed to get container exit code")
				continue
			}
			
			if exitCode == 0 {
				jm.completeJob(job)
			} else {
				jm.failJob(job, fmt.Sprintf("Container exited with code %d", exitCode))
			}
		case docker.ContainerStatusError:
			jm.failJob(job, "Container encountered an error")
		}
	}
}

func (jm *JobManager) completeJob(job *Job) {
	jm.mutex.Lock()
	defer jm.mutex.Unlock()
	
	job.Status = JobStatusCompleted
	job.Message = "Job completed successfully"
	now := time.Now()
	job.CompletedAt = &now
	
	jm.logger.Info("Job completed", zap.String("jobId", job.ID))
}

func (jm *JobManager) failJob(job *Job, message string) {
	jm.mutex.Lock()
	defer jm.mutex.Unlock()
	
	job.Status = JobStatusFailed
	job.Message = message
	now := time.Now()
	job.CompletedAt = &now
	
	jm.logger.Info("Job failed", zap.String("jobId", job.ID), zap.String("message", message))
}