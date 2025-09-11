package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.uber.org/zap/zapcore"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"instorage-container-processor/pkg/server"
)

const (
	DefaultHTTPPort   = "8080"
	DefaultOperatorURL = ""
)

func main() {
	var (
		httpPort    = flag.String("http-port", DefaultHTTPPort, "HTTP server port")
		operatorURL = flag.String("operator-url", DefaultOperatorURL, "Operator callback URL")
	)

	// Setup logging
	opts := zap.Options{
		Development: true,
		TimeEncoder: func(t time.Time, enc zapcore.PrimitiveArrayEncoder) {
			enc.AppendString(t.Format("2006-01-02 15:04:05"))
		},
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	logger := zap.New(zap.UseFlagOptions(&opts))
	ctrl.SetLogger(logger)

	// Get operator URL from environment if not provided
	if *operatorURL == "" {
		*operatorURL = os.Getenv("OPERATOR_URL")
	}

	logger.Info("Starting Instorage Container Processor",
		"httpPort", *httpPort,
		"operatorURL", *operatorURL,
	)

	// Create container processor
	processor, err := server.NewContainerProcessor(logger, *operatorURL)
	if err != nil {
		logger.Error(err, "Failed to create container processor")
		os.Exit(1)
	}

	// Setup HTTP routes
	router := processor.SetupRoutes()

	// Create HTTP server
	httpServer := &http.Server{
		Addr:           fmt.Sprintf(":%s", *httpPort),
		Handler:        router,
		ReadTimeout:    30 * time.Second,
		WriteTimeout:   30 * time.Second,
		IdleTimeout:    60 * time.Second,
		MaxHeaderBytes: 1 << 20, // 1MB
	}

	// Start HTTP server in a goroutine
	go func() {
		logger.Info("Starting HTTP server", "address", httpServer.Addr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error(err, "HTTP server failed")
			os.Exit(1)
		}
	}()

	// Setup graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	// Wait for shutdown signal
	sig := <-quit
	logger.Info("Received shutdown signal", "signal", sig.String())

	// Graceful shutdown with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	logger.Info("Shutting down HTTP server...")
	if err := httpServer.Shutdown(ctx); err != nil {
		logger.Error(err, "HTTP server forced shutdown")
	}

	logger.Info("Instorage Container Processor stopped")
}