package main

import (
	"awesomeProject1/internal/server"
	"awesomeProject1/pkg/database"
	"awesomeProject1/pkg/metrics"
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"
)

func main() {
	// Optimize CPU usage
	numCPU := runtime.NumCPU()
	runtime.GOMAXPROCS(numCPU)

	// Set up logging
	logFile, err := os.OpenFile("app.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Printf("Warning: could not open log file: %v. Using standard output.", err)
	} else {
		log.SetOutput(logFile)
		defer logFile.Close()
	}

	log.Printf("Starting server using %d CPUs", numCPU)

	// Create storage directory if it doesn't exist
	if err := os.MkdirAll(server.StorageDir, os.ModePerm); err != nil {
		log.Fatalf("Error creating storage directory: %v", err)
	}

	// Create directory for anonymous packages
	if err := os.MkdirAll(server.StorageDir+"/!additional", os.ModePerm); err != nil {
		log.Printf("Warning: failed to create directory for anonymous packages: %v", err)
	}

	// Initialize cache
	server.InitCache()
	log.Printf("Image cache initialized")

	// Load database configuration
	dbConfig := database.PostgresConfiguration{}
	cfg, err := dbConfig.LoadConfig("./config/config.yaml")
	if err != nil {
		log.Fatalf("Error loading configuration: %v", err)
	}

	// Connect to database
	db, err := database.NewPostgresDriver(cfg.GetConnectionString())
	if err != nil {
		log.Fatalf("Error connecting to database: %v", err)
	}
	defer db.Close()

	// Apply migrations
	if err = db.MigrateUp(); err != nil {
		log.Printf("Warning when applying migrations: %v. Continuing operation.", err)
	}

	// Create server
	srv := server.NewServer(db)

	// Start metrics server
	startMetricsServer()

	// Preload cache with frequently used images
	go srv.PreloadCommonImages()

	// Set up HTTP server with timeouts
	httpServer := &http.Server{
		Addr:         ":8081",
		Handler:      server.SetupRoutes(srv),
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	// Create channel for stop signals
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	// Start HTTP server in a separate goroutine
	go func() {
		log.Printf("Server started on port 8081")
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Error starting server: %v", err)
		}
	}()

	// Wait for stop signal
	<-stop

	log.Println("Stop signal received. Shutting down...")

	// Create context with timeout for graceful shutdown
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Stop server
	if err := httpServer.Shutdown(ctx); err != nil {
		log.Fatalf("Error stopping server: %v", err)
	}

	log.Println("Server stopped")
}

// Start Prometheus metrics server
func startMetricsServer() {
	port := 2112
	metricsHandler := metrics.MetricsHandler()

	// Start metrics server in a separate goroutine
	go func() {
		log.Printf("Starting metrics server on port: %d", port)
		metricsServer := &http.Server{
			Addr:    fmt.Sprintf(":%d", port),
			Handler: metricsHandler,
		}

		if err := metricsServer.ListenAndServe(); err != nil {
			log.Printf("Error starting metrics server: %v", err)
		}
	}()
}
