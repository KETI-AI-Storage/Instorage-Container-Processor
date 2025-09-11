package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/gorilla/mux"
	"go.uber.org/zap"

	"instorage-container-processor/pkg/docker"
)

type JobRequest struct {
	Name      string            `json:"name"`
	Namespace string            `json:"namespace"`
	Image     string            `json:"image"`
	Command   []string          `json:"command,omitempty"`
	Args      []string          `json:"args,omitempty"`
	Resources map[string]string `json:"resources,omitempty"`
	InputData struct {
		Path      string `json:"path"`
		MountPath string `json:"mountPath"`
	} `json:"inputData"`
	OutputData struct {
		Path      string `json:"path"`
		MountPath string `json:"mountPath"`
	} `json:"outputData"`
}

type JobResponse struct {
	JobID   string `json:"jobId"`
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

type Server struct {
	addr         string
	dockerClient *docker.Client
	logger       *zap.Logger
	router       *mux.Router
	server       *http.Server
	jobManager   *JobManager
}

func NewServer(addr string, dockerClient *docker.Client, logger *zap.Logger) *Server {
	router := mux.NewRouter()
	jobManager := NewJobManager(dockerClient, logger)
	
	s := &Server{
		addr:         addr,
		dockerClient: dockerClient,
		logger:       logger,
		router:       router,
		jobManager:   jobManager,
	}
	
	s.setupRoutes()
	
	return s
}

func (s *Server) setupRoutes() {
	api := s.router.PathPrefix("/api/v1").Subrouter()
	
	api.HandleFunc("/jobs", s.handleCreateJob).Methods("POST")
	api.HandleFunc("/jobs/{jobId}", s.handleGetJob).Methods("GET")
	api.HandleFunc("/jobs/{jobId}", s.handleDeleteJob).Methods("DELETE")
	api.HandleFunc("/health", s.handleHealth).Methods("GET")
	
	s.router.Use(s.loggingMiddleware)
}

func (s *Server) Start(ctx context.Context) error {
	s.server = &http.Server{
		Addr:    s.addr,
		Handler: s.router,
	}
	
	go s.jobManager.Start(ctx)
	
	errCh := make(chan error, 1)
	go func() {
		s.logger.Info("Starting HTTP server", zap.String("addr", s.addr))
		if err := s.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()
	
	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		s.logger.Info("Shutting down HTTP server")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		return s.server.Shutdown(shutdownCtx)
	}
}

func (s *Server) handleCreateJob(w http.ResponseWriter, r *http.Request) {
	var req JobRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.respondError(w, http.StatusBadRequest, "Invalid JSON payload")
		return
	}
	
	if req.Image == "" {
		s.respondError(w, http.StatusBadRequest, "Image is required")
		return
	}
	
	job := &Job{
		ID:        generateJobID(),
		Name:      req.Name,
		Namespace: req.Namespace,
		Image:     req.Image,
		Command:   req.Command,
		Args:      req.Args,
		Resources: req.Resources,
		InputData: JobData{
			Path:      req.InputData.Path,
			MountPath: req.InputData.MountPath,
		},
		OutputData: JobData{
			Path:      req.OutputData.Path,
			MountPath: req.OutputData.MountPath,
		},
		Status:    JobStatusPending,
		CreatedAt: time.Now(),
	}
	
	if err := s.jobManager.SubmitJob(job); err != nil {
		s.logger.Error("Failed to submit job", zap.Error(err))
		s.respondError(w, http.StatusInternalServerError, "Failed to submit job")
		return
	}
	
	resp := JobResponse{
		JobID:  job.ID,
		Status: string(job.Status),
	}
	
	s.respondJSON(w, http.StatusAccepted, resp)
}

func (s *Server) handleGetJob(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	jobID := vars["jobId"]
	
	job := s.jobManager.GetJob(jobID)
	if job == nil {
		s.respondError(w, http.StatusNotFound, "Job not found")
		return
	}
	
	resp := JobResponse{
		JobID:   job.ID,
		Status:  string(job.Status),
		Message: job.Message,
	}
	
	s.respondJSON(w, http.StatusOK, resp)
}

func (s *Server) handleDeleteJob(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	jobID := vars["jobId"]
	
	if err := s.jobManager.CancelJob(jobID); err != nil {
		s.respondError(w, http.StatusNotFound, "Job not found")
		return
	}
	
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	s.respondJSON(w, http.StatusOK, map[string]string{"status": "healthy"})
}

func (s *Server) respondJSON(w http.ResponseWriter, statusCode int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(data)
}

func (s *Server) respondError(w http.ResponseWriter, statusCode int, message string) {
	s.respondJSON(w, statusCode, map[string]string{"error": message})
}

func (s *Server) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		s.logger.Info("HTTP request",
			zap.String("method", r.Method),
			zap.String("path", r.URL.Path),
			zap.Duration("duration", time.Since(start)),
		)
	})
}

func generateJobID() string {
	return fmt.Sprintf("job-%d", time.Now().UnixNano())
}