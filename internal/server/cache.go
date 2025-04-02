package server

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Cache global cache for images and metadata
type Cache struct {
	imageData      sync.Map // For caching file contents (file path -> []byte)
	metadata       sync.Map // For caching file metadata (file path -> FileMetadata)
	productCounts  sync.Map // For caching the number of images by product
	totalCount     int      // Total number of images
	countMutex     sync.RWMutex
	cacheHits      int64
	cacheMisses    int64
	statsMutex     sync.RWMutex
	lastCacheReset time.Time
}

// FileMetadata file metadata for caching
type FileMetadata struct {
	ETag         string
	LastModified time.Time
	ContentType  string
	Size         int64
}

// Create global cache
var ImageCache = &Cache{
	lastCacheReset: time.Now(),
}

// InitCache initializes the cache and starts a routine for periodic cleaning
func InitCache() {
	// Start a goroutine for periodic cache clearing
	go func() {
		for {
			time.Sleep(6 * time.Hour) // Clear cache every 6 hours
			ImageCache.Clear()
		}
	}()
}

// Clear clears the cache
func (c *Cache) Clear() {
	c.imageData = sync.Map{}
	c.metadata = sync.Map{}
	c.countMutex.Lock()
	c.totalCount = 0
	c.countMutex.Unlock()
	c.productCounts = sync.Map{}

	c.statsMutex.Lock()
	c.cacheHits = 0
	c.cacheMisses = 0
	c.lastCacheReset = time.Now()
	c.statsMutex.Unlock()

	log.Println("Cache cleared")
}

// GetCacheStats returns cache statistics
func (c *Cache) GetCacheStats() map[string]interface{} {
	c.statsMutex.RLock()
	defer c.statsMutex.RUnlock()

	totalRequests := c.cacheHits + c.cacheMisses
	hitRate := 0.0
	if totalRequests > 0 {
		hitRate = float64(c.cacheHits) / float64(totalRequests) * 100
	}

	return map[string]interface{}{
		"hits":          c.cacheHits,
		"misses":        c.cacheMisses,
		"hitRate":       hitRate,
		"lastReset":     c.lastCacheReset,
		"runningTimeMs": time.Since(c.lastCacheReset).Milliseconds(),
	}
}

// RecordHit records a cache hit
func (c *Cache) RecordHit() {
	c.statsMutex.Lock()
	c.cacheHits++
	c.statsMutex.Unlock()
}

// RecordMiss records a cache miss
func (c *Cache) RecordMiss() {
	c.statsMutex.Lock()
	c.cacheMisses++
	c.statsMutex.Unlock()
}

// isImageFile checks if the file is an image by extension
func IsImageFile(filename string) bool {
	ext := strings.ToLower(filepath.Ext(filename))
	return ext == ".jpg" || ext == ".jpeg" || ext == ".png" || ext == ".gif" || ext == ".webp"
}

// GetContentType determines the MIME type of the file by extension
func GetContentType(filename string) string {
	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	default:
		return "application/octet-stream"
	}
}

// preloadCommonImages preloads frequently used images into cache
func preloadCommonImages() {
	// Preload anonymous packages
	anonymousDir := StorageDir + "/!additional"
	files, err := os.ReadDir(anonymousDir)
	if err != nil {
		log.Printf("Warning: could not read anonymous packages directory: %v", err)
		return
	}

	var wg sync.WaitGroup
	for _, file := range files {
		wg.Add(1)
		go func(filename string) {
			defer wg.Done()

			fullPath := anonymousDir + "/" + filename
			data, err := os.ReadFile(fullPath)
			if err != nil {
				log.Printf("Failed to read file %s: %v", fullPath, err)
				return
			}

			// Save to cache
			ImageCache.imageData.Store(fullPath, data)

			// Create metadata
			fileInfo, err := os.Stat(fullPath)
			if err == nil {
				contentType := GetContentType(filename)
				etag := fmt.Sprintf(`"%x-%x"`, fileInfo.ModTime().Unix(), fileInfo.Size())

				metadata := FileMetadata{
					ETag:         etag,
					LastModified: fileInfo.ModTime(),
					ContentType:  contentType,
					Size:         fileInfo.Size(),
				}
				ImageCache.metadata.Store(fullPath, metadata)
			}

			log.Printf("File %s preloaded to cache", filename)
		}(file.Name())
	}

	wg.Wait()
	log.Printf("Preloading of frequently used images completed")
}
