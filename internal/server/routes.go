package server

import (
	"awesomeProject1/pkg/middleware"
	"log"
	"net/http"
)

// SetupRoutes configures all API routes
func SetupRoutes(server *Server) http.Handler {
	mux := http.NewServeMux()

	// API version 1
	apiV1 := http.NewServeMux()

	// Media (image) routes
	apiV1.Handle("/media", middleware.PrometheusMiddleware(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			server.HandleDirectLinks(w, r)
		}),
	))

	// POST /api/v1/media/process - start processing new images (with authentication)
	apiV1.Handle("/media/process", middleware.PrometheusMiddleware(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Check method
			if r.Method != http.MethodPost {
				w.Header().Set("Allow", "POST")
				http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
				return
			}

			// Check authentication
			apiKey := r.Header.Get("X-API-Key")
			if apiKey != APIUpdatePassword {
				w.Header().Set("WWW-Authenticate", "Basic realm=\"Restricted\"")
				http.Error(w, "Unauthorized: Invalid API key", http.StatusUnauthorized)
				return
			}

			server.HandleMediaProcessing(w, r)
		}),
	))

	// GET /api/v1/media/status - check processing status
	apiV1.Handle("/media/status", middleware.PrometheusMiddleware(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			server.HandleImageStatusRequest(w, r)
		}),
	))

	// GET /api/v1/media/stats - overall statistics for all images
	apiV1.Handle("/media/stats", middleware.PrometheusMiddleware(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/media/stats" {
				server.HandleTotalImagesStats(w, r)
			} else {
				// This is for /media/stats/{productID} - statistics for a specific product
				server.HandleMediaStats(w, r)
			}
		}),
	))

	// GET /api/v1/similarity - calculate string similarity
	apiV1.Handle("/similarity", middleware.PrometheusMiddleware(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			server.HandleSimilarity(w, r)
		}),
	))

	// Cache management
	apiV1.Handle("/cache/stats", middleware.PrometheusMiddleware(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			server.HandleCacheStats(w, r)
		}),
	))

	apiV1.Handle("/cache/clear", middleware.PrometheusMiddleware(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			server.HandleClearCache(w, r)
		}),
	))

	// Support for batch anonymous images
	apiV1.Handle("/anonymous/package", middleware.PrometheusMiddleware(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			server.HandleAnonymousPackageImage(w, r)
		}),
	))

	// Route for serving images (should be above the /api route)
	mux.Handle("/", middleware.PrometheusMiddleware(
		http.HandlerFunc(server.HandleImageRequest),
	))

	// Add versioned API to main mux
	mux.Handle("/api/v1/", http.StripPrefix("/api/v1", apiV1))

	// Backwards compatibility with old routes (for smooth transition)
	mux.Handle("/api/media/update", middleware.PrometheusMiddleware(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			log.Println("Access to deprecated API: /api/media/update. Use /api/v1/media/process")

			// Check authentication
			apiKey := r.Header.Get("X-API-Key")
			if apiKey != APIUpdatePassword {
				w.Header().Set("WWW-Authenticate", "Basic realm=\"Restricted\"")
				http.Error(w, "Unauthorized: Invalid API key", http.StatusUnauthorized)
				return
			}

			server.HandleMediaProcessing(w, r)
		}),
	))

	mux.Handle("/api/media", middleware.PrometheusMiddleware(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			log.Println("Access to deprecated API: /api/media. Use /api/v1/media")
			server.HandleDirectLinks(w, r)
		}),
	))

	mux.Handle("/api/media/status", middleware.PrometheusMiddleware(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			log.Println("Access to deprecated API: /api/media/status. Use /api/v1/media/status")
			server.HandleImageStatusRequest(w, r)
		}),
	))

	return mux
}
