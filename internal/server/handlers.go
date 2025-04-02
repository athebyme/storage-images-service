// internal/server/handlers.go
package server

import (
	"awesomeProject1/internal/business"
	"awesomeProject1/pkg/database"
	"awesomeProject1/pkg/types"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// MediaRequest represents a request to get media
type MediaRequest struct {
	ProductIDs []int `json:"productIDs"`
}

// MediaResponse map of "Product ID" -> "list of image URLs"
type MediaResponse map[string][]string

// MediaStatsResponse response structure with additional statistics
type MediaStatsResponse struct {
	TotalCount int      `json:"totalCount"`
	URLs       []string `json:"urls"`
}

// HandleDirectLinks returns a list of available images for the specified product IDs
// POST /api/v1/media with request body:
// {"productIDs": [1, 2, 3]}
func (s *Server) HandleDirectLinks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Decode request body
	var req MediaRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Error reading request body: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Check for productIDs
	if len(req.ProductIDs) == 0 {
		// If no IDs specified, get all available IDs
		existingIDs, err := s.DB.GetArticles()
		if err != nil {
			http.Error(w, "Error getting available IDs: "+err.Error(), http.StatusInternalServerError)
			return
		}

		for id := range existingIDs {
			req.ProductIDs = append(req.ProductIDs, id)
		}
	}

	// Create response
	response := make(MediaResponse)

	// Process each ID
	for _, id := range req.ProductIDs {
		productDir := filepath.Join(StorageDir, fmt.Sprintf("%d", id))

		// Check if directory exists
		if _, err := os.Stat(productDir); os.IsNotExist(err) {
			continue
		}

		// Read files from directory
		files, err := os.ReadDir(productDir)
		if err != nil {
			continue
		}

		var urls []string
		for _, file := range files {
			if file.IsDir() || !IsImageFile(file.Name()) {
				continue
			}

			imageURL := fmt.Sprintf("%s/%d/%s", LocalHost, id, file.Name())
			urls = append(urls, imageURL)
		}

		if len(urls) > 0 {
			response[fmt.Sprintf("%d", id)] = urls
		}
	}

	// Send response
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "private, max-age=300") // Cache for 5 minutes

	if len(response) == 0 {
		http.Error(w, "Images not found", http.StatusNotFound)
		return
	}

	if err := json.NewEncoder(w).Encode(response); err != nil {
		http.Error(w, "Error encoding response: "+err.Error(), http.StatusInternalServerError)
	}
}

// HandleMediaStats gets statistics on images for a specific product
// GET /api/v1/media/stats/{productID}
func (s *Server) HandleMediaStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract product ID from URL
	pathParts := strings.Split(r.URL.Path, "/")
	if len(pathParts) < 5 {
		http.Error(w, "Invalid request path", http.StatusBadRequest)
		return
	}

	productIDStr := pathParts[4]
	productID, err := strconv.Atoi(productIDStr)
	if err != nil {
		http.Error(w, "Invalid product ID", http.StatusBadRequest)
		return
	}

	// Check cache
	if countValue, exists := ImageCache.productCounts.Load(productID); exists {
		// Cache hit
		ImageCache.RecordHit()
		count := countValue.(int)

		// If 0 images, return empty response
		if count == 0 {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(MediaStatsResponse{
				TotalCount: 0,
				URLs:       []string{},
			})
			return
		}

		// Load URLs
		productDir := filepath.Join(StorageDir, fmt.Sprintf("%d", productID))
		files, err := os.ReadDir(productDir)
		if err != nil {
			http.Error(w, "Error reading directory", http.StatusInternalServerError)
			return
		}

		var urls []string
		for _, file := range files {
			if file.IsDir() || !IsImageFile(file.Name()) {
				continue
			}

			imageURL := fmt.Sprintf("%s/%d/%s", LocalHost, productID, file.Name())
			urls = append(urls, imageURL)
		}

		response := MediaStatsResponse{
			TotalCount: count,
			URLs:       urls,
		}

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "private, max-age=300") // Cache for 5 minutes
		json.NewEncoder(w).Encode(response)
		return
	}

	// Cache miss
	ImageCache.RecordMiss()

	// Path to product directory
	productDir := filepath.Join(StorageDir, fmt.Sprintf("%d", productID))

	// Check if directory exists
	if _, err := os.Stat(productDir); os.IsNotExist(err) {
		// If directory doesn't exist, remember this in cache and return empty response
		ImageCache.productCounts.Store(productID, 0)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(MediaStatsResponse{
			TotalCount: 0,
			URLs:       []string{},
		})
		return
	}

	// Read files from directory
	files, err := os.ReadDir(productDir)
	if err != nil {
		http.Error(w, "Error reading directory", http.StatusInternalServerError)
		return
	}

	var urls []string
	imageCount := 0

	for _, file := range files {
		if file.IsDir() || !IsImageFile(file.Name()) {
			continue
		}

		imageCount++
		imageURL := fmt.Sprintf("%s/%d/%s", LocalHost, productID, file.Name())
		urls = append(urls, imageURL)
	}

	// Save to cache
	ImageCache.productCounts.Store(productID, imageCount)

	// Create response
	response := MediaStatsResponse{
		TotalCount: imageCount,
		URLs:       urls,
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "private, max-age=300") // Cache for 5 minutes
	json.NewEncoder(w).Encode(response)
}

// HandleTotalImagesStats gets overall image statistics
// GET /api/v1/media/stats
func (s *Server) HandleTotalImagesStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Check cache
	ImageCache.countMutex.RLock()
	cachedCount := ImageCache.totalCount
	ImageCache.countMutex.RUnlock()

	if cachedCount > 0 {
		// Use cached value
		ImageCache.RecordHit()
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "private, max-age=300") // Cache for 5 minutes
		json.NewEncoder(w).Encode(map[string]int{
			"totalCount": cachedCount,
		})
		return
	}

	// Cache miss, recalculate
	ImageCache.RecordMiss()

	totalCount := 0

	// Walk storage directory
	err := filepath.Walk(StorageDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip directories and special files
		if info.IsDir() || strings.HasPrefix(info.Name(), ".") {
			return nil
		}

		// Check if it's an image
		if IsImageFile(info.Name()) {
			totalCount++
		}

		return nil
	})

	if err != nil {
		http.Error(w, "Error counting images", http.StatusInternalServerError)
		return
	}

	// Save to cache
	ImageCache.countMutex.Lock()
	ImageCache.totalCount = totalCount
	ImageCache.countMutex.Unlock()

	// Send response
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "private, max-age=300") // Cache for 5 minutes
	json.NewEncoder(w).Encode(map[string]int{
		"totalCount": totalCount,
	})
}

// HandleCacheStats returns cache statistics
// GET /api/v1/cache/stats
func (s *Server) HandleCacheStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	stats := ImageCache.GetCacheStats()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stats)
}

// HandleClearCache clears the cache
// POST /api/v1/cache/clear
func (s *Server) HandleClearCache(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ImageCache.Clear()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status":  "ok",
		"message": "Cache cleared",
	})
}

// HandleImageRequest handles requests for images with optimized caching
func (s *Server) HandleImageRequest(w http.ResponseWriter, r *http.Request) {
	pathParts := strings.Split(r.URL.Path, "/")
	if len(pathParts) < 3 {
		http.Error(w, "Invalid request path", http.StatusBadRequest)
		return
	}

	productID := pathParts[1]
	fileName := pathParts[2]
	imagePath := filepath.Join(StorageDir, productID, fileName)

	// Check metadata cache
	if metadataValue, exists := ImageCache.metadata.Load(imagePath); exists {
		metadata := metadataValue.(FileMetadata)

		// Check conditional request
		if match := r.Header.Get("If-None-Match"); match != "" && match == metadata.ETag {
			w.WriteHeader(http.StatusNotModified)
			ImageCache.RecordHit()
			return
		}

		// Set caching headers
		w.Header().Set("ETag", metadata.ETag)
		w.Header().Set("Last-Modified", metadata.LastModified.Format(http.TimeFormat))
		w.Header().Set("Cache-Control", "public, max-age=86400") // 1 day
		w.Header().Set("Content-Type", metadata.ContentType)

		// Check image data cache
		if imageData, exists := ImageCache.imageData.Load(imagePath); exists {
			// Serve from cache if size isn't too large
			data := imageData.([]byte)
			if len(data) < 10*1024*1024 { // Don't cache files > 10MB in memory
				ImageCache.RecordHit()
				http.ServeContent(w, r, fileName, metadata.LastModified,
					bytes.NewReader(data))
				return
			}
		}
	}

	// Cache miss
	ImageCache.RecordMiss()

	// Check if file exists
	fileInfo, err := os.Stat(imagePath)
	if os.IsNotExist(err) {
		http.Error(w, "Image not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "Error getting file info", http.StatusInternalServerError)
		return
	}

	// Determine MIME type
	contentType := GetContentType(fileName)

	// Generate ETag based on last modified time and size
	etag := fmt.Sprintf(`"%x-%x"`, fileInfo.ModTime().Unix(), fileInfo.Size())

	// Check conditional request
	if match := r.Header.Get("If-None-Match"); match != "" && match == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	// Set caching headers
	w.Header().Set("ETag", etag)
	w.Header().Set("Last-Modified", fileInfo.ModTime().Format(http.TimeFormat))
	w.Header().Set("Cache-Control", "public, max-age=86400") // 1 day
	w.Header().Set("Content-Type", contentType)

	// Save metadata in cache
	metadata := FileMetadata{
		ETag:         etag,
		LastModified: fileInfo.ModTime(),
		ContentType:  contentType,
		Size:         fileInfo.Size(),
	}
	ImageCache.metadata.Store(imagePath, metadata)

	// If file is not too large, cache its content
	if fileInfo.Size() < 10*1024*1024 { // Don't cache files > 10MB
		data, err := os.ReadFile(imagePath)
		if err == nil {
			ImageCache.imageData.Store(imagePath, data)
			http.ServeContent(w, r, fileName, fileInfo.ModTime(), bytes.NewReader(data))
			return
		}
	}

	// If file is large or there was an error reading, open and serve normally
	file, err := os.Open(imagePath)
	if err != nil {
		http.Error(w, "Error opening file", http.StatusInternalServerError)
		return
	}
	defer file.Close()

	// Use ServeContent for support of Range requests and proper headers
	http.ServeContent(w, r, fileName, fileInfo.ModTime(), file)
}

// HandleAnonymousPackageImage handles requests for anonymous batch images
func (s *Server) HandleAnonymousPackageImage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ext := r.URL.Query().Get("ext")
	if ext == "" {
		ext = "png"
	}

	var filename string
	var contentType string

	switch ext {
	case "png":
		filename = "package-png.png"
		contentType = "image/png"
	case "jpg", "jpeg":
		filename = "package-jpg.jpg"
		contentType = "image/jpeg"
	default:
		http.Error(w, "Invalid image format", http.StatusBadRequest)
		return
	}

	filePath := filepath.Join("./storage/!additional/", filename)

	// Check cache
	if imageData, exists := ImageCache.imageData.Load(filePath); exists {
		// Serve from cache
		ImageCache.RecordHit()
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Cache-Control", "public, max-age=86400") // 1 day
		w.Write(imageData.([]byte))
		return
	}

	// Cache miss
	ImageCache.RecordMiss()

	// Get file
	data, err := os.ReadFile(filePath)
	if err != nil {
		http.Error(w, "Image not found", http.StatusNotFound)
		return
	}

	// Cache for future requests
	ImageCache.imageData.Store(filePath, data)

	// Serve file
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "public, max-age=86400") // 1 day
	w.Write(data)
}

// HandleSimilarity handles requests for calculating string similarity
func (s *Server) HandleSimilarity(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Get parameters from URL
	s1 := r.URL.Query().Get("s1")
	s2 := r.URL.Query().Get("s2")

	if s1 == "" || s2 == "" {
		http.Error(w, "Missing required parameters s1 and s2", http.StatusBadRequest)
		return
	}

	// Calculate similarity
	sim := business.Similarity(s1, s2)

	// Return result
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]float64{"similarity": sim})
}

// HandleImageStatusRequest returns image processing status
func (s *Server) HandleImageStatusRequest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Get parameters from URL
	idsParam := r.URL.Query().Get("ids")
	var productIDs []int

	if idsParam != "" {
		idStrs := strings.Split(idsParam, ",")
		for _, idStr := range idStrs {
			id, err := strconv.Atoi(strings.TrimSpace(idStr))
			if err != nil {
				http.Error(w, fmt.Sprintf("Invalid ID format: %s", idStr), http.StatusBadRequest)
				return
			}
			productIDs = append(productIDs, id)
		}
	}

	// Get statuses from DB
	var statuses []types.ProcessStatus
	var err error

	if len(productIDs) == 0 {
		statuses, err = s.DB.GetAllImageStatuses()
	} else {
		statuses, err = s.DB.GetImageStatusesByProductIDs(productIDs)
	}

	if err != nil {
		http.Error(w, fmt.Sprintf("Error getting statuses: %v", err), http.StatusInternalServerError)
		return
	}

	// Send response
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache")
	if err := json.NewEncoder(w).Encode(statuses); err != nil {
		http.Error(w, "Error encoding response", http.StatusInternalServerError)
	}
}

// HandleMediaProcessing handles requests for media file processing (asynchronous operation)
func (s *Server) HandleMediaProcessing(w http.ResponseWriter, r *http.Request) {
	// Check authentication (this check is already performed in the router, but can be duplicated)
	apiKey := r.Header.Get("X-API-Key")
	if apiKey != APIUpdatePassword {
		w.Header().Set("WWW-Authenticate", "Basic realm=\"Restricted\"")
		http.Error(w, "Unauthorized: Invalid API key", http.StatusUnauthorized)
		return
	}

	var req MediaRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request format: "+err.Error(), http.StatusBadRequest)
		return
	}

	if len(req.ProductIDs) == 0 {
		http.Error(w, "ProductIDs list cannot be empty", http.StatusBadRequest)
		return
	}

	// Start asynchronous processing
	go func() {
		ctx := context.Background()
		ctx = context.WithValue(ctx, "db", s.DB)

		// Get existing IDs from database
		ids, err := s.DB.GetArticles()
		if err != nil {
			log.Printf("Error getting existing articles: %v", err)
			return
		}
		ctx = context.WithValue(ctx, "exist_ids", ids)

		// Get data from remote API
		remoteMedia, err := fetchRemoteMedia(req.ProductIDs)
		if err != nil {
			log.Printf("Error getting media from remote API: %v", err)
			return
		}

		// Create channel for processing statuses
		statusChan := make(chan types.ProcessStatus, len(req.ProductIDs))

		// Start status processing
		go func() {
			for status := range statusChan {
				if err := s.DB.UpdateImageStatus(status); err != nil {
					log.Printf("Error updating status: %v", err)
				}
			}
		}()

		// Process media files
		if err := processMedia(ctx, remoteMedia, statusChan); err != nil {
			log.Printf("Error processing media: %v", err)
		}

		// After processing is complete, clear cache to update statistics
		ImageCache.Clear()
	}()

	// Return status 202 Accepted for asynchronous operations
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "processing",
		"statusUrl": fmt.Sprintf("/api/v1/media/status?ids=%s",
			strings.Trim(strings.Join(strings.Fields(fmt.Sprint(req.ProductIDs)), ","), "[]")),
		"message": "Image processing started",
	})
}

// fetchRemoteMedia gets media file data from remote API
func fetchRemoteMedia(productIDs []int) (MediaResponse, error) {
	requestBody, err := json.Marshal(MediaRequest{ProductIDs: productIDs})
	if err != nil {
		return nil, fmt.Errorf("error marshaling request: %w", err)
	}

	// Create HTTP client with timeout
	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	resp, err := client.Post(RemoteAPI, "application/json", bytes.NewReader(requestBody))
	if err != nil {
		return nil, fmt.Errorf("error requesting remote API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("remote API returned error: %s, code: %d, body: %s",
			resp.Status, resp.StatusCode, string(body))
	}

	var mediaResponse MediaResponse
	if err := json.NewDecoder(resp.Body).Decode(&mediaResponse); err != nil {
		return nil, fmt.Errorf("error decoding API response: %w", err)
	}

	return mediaResponse, nil
}

// processMedia processes received media files
func processMedia(ctx context.Context, media MediaResponse, statusChan chan<- types.ProcessStatus) error {
	defer close(statusChan)

	idsSync := sync.Map{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	var batchWG sync.WaitGroup

	// Get existing IDs from context
	ids, ok := ctx.Value("exist_ids").(map[int]struct{})
	if ok {
		for id := range ids {
			idsSync.Store(id, struct{}{})
		}
	}

	// Limit request frequency
	limiter := time.NewTicker(time.Minute / 200)
	defer limiter.Stop()

	// Channels for processing errors and results
	errCh := make(chan error, len(media))
	photoLoadCh := make(chan database.Article, 100)
	semaphore := make(chan struct{}, 100) // Limit number of parallel operations

	// Get DB driver from context
	driver, ok := ctx.Value("db").(database.Driver)
	if !ok {
		return fmt.Errorf("DB driver not set in context")
	}

	// Channel for batch processing
	batch := make(chan []database.Article, 1)

	// Goroutine for accumulating records in batches
	batchWG.Add(1)
	go func() {
		defer batchWG.Done()
		var loadBatch []database.Article
		defer close(batch)

		for article := range photoLoadCh {
			loadBatch = append(loadBatch, article)
			log.Printf("Product %d processed", article.ID)

			// If we've collected 100 records, send batch
			if len(loadBatch) == 100 {
				batch <- loadBatch
				loadBatch = []database.Article{}
			}
		}

		// Send remaining records
		if len(loadBatch) > 0 {
			batch <- loadBatch
		}
	}()

	// Goroutine for batch writing to DB
	batchWG.Add(1)
	go func() {
		defer batchWG.Done()
		for locBatch := range batch {
			err := driver.CreateRecords(locBatch)
			if err != nil {
				log.Printf("Error writing batch to DB: %v", err)
				errCh <- err
				return
			}
			log.Printf("Batch of %d records successfully written to DB", len(locBatch))
		}
	}()

	// Process media files for each product
	for productID, urls := range media {
		wg.Add(1)
		go func(productID string, urls []string) {
			defer wg.Done()

			// Array for storing local URLs
			localURLs := make([]string, len(urls))

			// Convert ID to number
			id, err := strconv.Atoi(productID)
			if err != nil {
				log.Printf("Error converting product ID: %v", err)
				statusChan <- types.ProcessStatus{ProductID: 0, Status: "error", Timestamp: time.Now()}
				errCh <- err
				return
			}

			// Check if product already exists
			if _, ok := idsSync.Load(id); ok {
				statusChan <- types.ProcessStatus{ProductID: id, Status: "exists", Timestamp: time.Now()}
				return
			}

			// Process each image URL
			for i, url := range urls {
				select {
				case <-ctx.Done():
					log.Printf("Context canceled")
					statusChan <- types.ProcessStatus{ProductID: id, Status: "cancelled", Timestamp: time.Now()}
					errCh <- ctx.Err()
					return
				default:
					// Continue processing
				}

				// Observe rate limit
				<-limiter.C

				// Use semaphore to limit parallel downloads
				semaphore <- struct{}{}
				func() {
					defer func() { <-semaphore }()

					// Create directory for storing images
					outputDir := filepath.Join(StorageDir, productID)
					if err := os.MkdirAll(outputDir, os.ModePerm); err != nil {
						log.Printf("Error creating directory %s: %v", outputDir, err)
						statusChan <- types.ProcessStatus{ProductID: id, Status: "error", Timestamp: time.Now()}
						errCh <- err
						return
					}

					// Paths for temporary and final files
					outputPath := filepath.Join(outputDir, fmt.Sprintf("%d.jpg", i))
					tempPath := outputPath + ".tmp"

					// Download image
					if err := downloadImage(ctx, url, tempPath); err != nil {
						log.Printf("Error downloading image for product %d: %v", id, err)
						statusChan <- types.ProcessStatus{ProductID: id, Status: "error", Timestamp: time.Now()}
						errCh <- err
						return
					}

					// Open temporary file for processing
					file, err := os.Open(tempPath)
					if err != nil {
						log.Printf("Error opening temporary file for product %d: %v", id, err)
						statusChan <- types.ProcessStatus{ProductID: id, Status: "error", Timestamp: time.Now()}
						errCh <- err
						return
					}

					// Convert image to square format
					if err := makeSquareImage(file, outputPath); err != nil {
						log.Printf("Error creating square image for product %d: %v", id, err)
						statusChan <- types.ProcessStatus{ProductID: id, Status: "error", Timestamp: time.Now()}
						errCh <- err
						return
					}

					// Close file and remove temporary
					defer func() {
						_ = file.Close()
						_ = os.Remove(tempPath)
					}()

					// Form URL for local image
					localURLs[i] = fmt.Sprintf("%s/%s/%d.jpg", LocalHost, productID, i)
				}()
			}

			// All images processed successfully
			mu.Lock()
			statusChan <- types.ProcessStatus{ProductID: id, Status: "ok", Timestamp: time.Now()}
			photoLoadCh <- database.Article{ID: id, Photos: localURLs}
			mu.Unlock()

		}(productID, urls)
	}

	// Wait for all image processing to complete
	wg.Wait()
	close(photoLoadCh)

	// Wait for all batches to be written
	batchWG.Wait()

	// Close semaphore
	close(semaphore)

	// Check for errors
	if len(errCh) > 0 {
		return <-errCh
	}

	return nil
}

// downloadImage downloads an image by URL
func downloadImage(ctx context.Context, url, outputPath string) error {
	// Create HTTP request with context
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("error creating request: %w", err)
	}

	// Use client with timeout
	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	// Execute request
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("error downloading image: %w", err)
	}
	defer resp.Body.Close()

	// Check response code
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("error downloading image: %s", resp.Status)
	}

	// Create file for saving image
	file, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("error creating file: %w", err)
	}
	defer file.Close()

	// Copy data
	_, err = io.Copy(file, resp.Body)
	if err != nil {
		return fmt.Errorf("error writing file: %w", err)
	}

	return nil
}

// makeSquareImage converts image to square format
func makeSquareImage(input io.Reader, outputPath string) error {
	// Decode image
	img, _, err := image.Decode(input)
	if err != nil {
		return fmt.Errorf("error decoding image: %w", err)
	}

	originalBounds := img.Bounds()
	width := originalBounds.Dx()
	height := originalBounds.Dy()

	// Determine square image size
	size := width
	if height > width {
		size = height
	}

	// Create new image
	squareImage := image.NewRGBA(image.Rect(0, 0, size, size))
	white := color.RGBA{255, 255, 255, 255}
	draw.Draw(squareImage, squareImage.Bounds(), &image.Uniform{white}, image.Point{}, draw.Src)

	// Place original image in center
	offsetX := (size - width) / 2
	offsetY := (size - height) / 2
	draw.Draw(squareImage, image.Rect(offsetX, offsetY, offsetX+width, offsetY+height),
		img, originalBounds.Min, draw.Over)

	// Save result
	outputFile, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("error creating output file: %w", err)
	}
	defer outputFile.Close()

	// Encode image as JPEG
	return jpeg.Encode(outputFile, squareImage, nil)
}
