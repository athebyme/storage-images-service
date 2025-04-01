package main

import (
	"log"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Cache глобальный кэш для изображений и метаданных
type Cache struct {
	imageData      sync.Map // Для кэширования содержимого файлов (путь к файлу -> []byte)
	metadata       sync.Map // Для кэширования метаданных (путь к файлу -> FileMetadata)
	productCounts  sync.Map // Для кэширования количества изображений по продуктам
	totalCount     int      // Общее количество изображений
	countMutex     sync.RWMutex
	cacheHits      int64
	cacheMisses    int64
	statsMutex     sync.RWMutex
	lastCacheReset time.Time
}

// FileMetadata метаданные файла для кэширования
type FileMetadata struct {
	ETag         string
	LastModified time.Time
	ContentType  string
	Size         int64
}

// Создаем глобальный кэш
var imageCache = &Cache{
	lastCacheReset: time.Now(),
}

// Инициализируем кэш
func initCache() {
	// Запускаем горутину для периодического очищения кэша
	go func() {
		for {
			time.Sleep(6 * time.Hour) // Очищаем кэш каждые 6 часов
			imageCache.Clear()
		}
	}()
}

// Clear очищает кэш
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

	log.Println("Кэш очищен")
}

// GetCacheStats возвращает статистику кэша
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

// RecordHit записывает попадание в кэш
func (c *Cache) RecordHit() {
	c.statsMutex.Lock()
	c.cacheHits++
	c.statsMutex.Unlock()
}

// RecordMiss записывает промах в кэш
func (c *Cache) RecordMiss() {
	c.statsMutex.Lock()
	c.cacheMisses++
	c.statsMutex.Unlock()
}

// isImageFile проверяет, является ли файл изображением по расширению
func isImageFile(filename string) bool {
	ext := strings.ToLower(filepath.Ext(filename))
	return ext == ".jpg" || ext == ".jpeg" || ext == ".png" || ext == ".gif" || ext == ".webp"
}

// getContentType определяет MIME-тип файла по расширению
func getContentType(filename string) string {
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
