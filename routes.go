package main

import (
	"awesomeProject1/middleware"
	"log"
	"net/http"
)

// setupRoutes настраивает все маршруты API
func setupRoutes(server *Server) http.Handler {
	mux := http.NewServeMux()

	// API версия 1
	apiV1 := http.NewServeMux()

	// Маршруты для работы с медиа (изображениями)

	// POST /api/v1/media - получение ссылок на изображения через тело запроса
	apiV1.Handle("/media", middleware.PrometheusMiddleware(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				w.Header().Set("Allow", "POST")
				http.Error(w, "Метод не разрешен", http.StatusMethodNotAllowed)
				return
			}
			server.handleDirectLinks(w, r)
		}),
	))

	// POST /api/v1/media/process - запуск обработки новых изображений (с аутентификацией)
	apiV1.Handle("/media/process", middleware.PrometheusMiddleware(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Проверяем метод
			if r.Method != http.MethodPost {
				w.Header().Set("Allow", "POST")
				http.Error(w, "Метод не разрешен", http.StatusMethodNotAllowed)
				return
			}

			// Проверяем аутентификацию
			apiKey := r.Header.Get("X-API-Key")
			if apiKey != API_UPDATE_PASSWORD {
				w.Header().Set("WWW-Authenticate", "Basic realm=\"Restricted\"")
				http.Error(w, "Unauthorized: Invalid API key", http.StatusUnauthorized)
				return
			}

			server.handleMediaProcessing(w, r)
		}),
	))

	// GET /api/v1/media/status - проверка статуса обработки
	apiV1.Handle("/media/status", middleware.PrometheusMiddleware(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			server.handleImageStatusRequest(w, r)
		}),
	))

	// GET /api/v1/media/stats - общая статистика по всем изображениям
	apiV1.Handle("/media/stats", middleware.PrometheusMiddleware(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/media/stats" {
				server.handleTotalImagesStats(w, r)
			} else {
				// Это для /media/stats/{productID} - статистика по конкретному продукту
				server.handleMediaStats(w, r)
			}
		}),
	))

	// GET /api/v1/similarity - вычисление сходства строк
	apiV1.Handle("/similarity", middleware.PrometheusMiddleware(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			server.handleSimilarity(w, r)
		}),
	))

	// Управление кэшем
	apiV1.Handle("/cache/stats", middleware.PrometheusMiddleware(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			server.handleCacheStats(w, r)
		}),
	))

	apiV1.Handle("/cache/clear", middleware.PrometheusMiddleware(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			server.handleClearCache(w, r)
		}),
	))

	// Поддержка пакетных анонимных изображений
	apiV1.Handle("/anonymous/package", middleware.PrometheusMiddleware(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			server.handleAnonymousPackageImage(w, r)
		}),
	))

	// Маршрут для отдачи изображений (должен быть выше маршрута /api)
	mux.Handle("/", middleware.PrometheusMiddleware(
		http.HandlerFunc(server.handleImageRequest),
	))

	// Добавляем версионированное API к основному mux
	mux.Handle("/api/v1/", http.StripPrefix("/api/v1", apiV1))

	// Обратная совместимость со старыми маршрутами (для плавного перехода)
	mux.Handle("/api/media/update", middleware.PrometheusMiddleware(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			log.Println("Обращение к устаревшему API: /api/media/update. Используйте /api/v1/media/process")

			// Проверяем аутентификацию
			apiKey := r.Header.Get("X-API-Key")
			if apiKey != API_UPDATE_PASSWORD {
				w.Header().Set("WWW-Authenticate", "Basic realm=\"Restricted\"")
				http.Error(w, "Unauthorized: Invalid API key", http.StatusUnauthorized)
				return
			}

			server.handleMediaProcessing(w, r)
		}),
	))

	mux.Handle("/api/media", middleware.PrometheusMiddleware(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			log.Println("Обращение к устаревшему API: /api/media. Используйте /api/v1/media")
			server.handleDirectLinks(w, r)
		}),
	))

	mux.Handle("/api/media/status", middleware.PrometheusMiddleware(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			log.Println("Обращение к устаревшему API: /api/media/status. Используйте /api/v1/media/status")
			server.handleImageStatusRequest(w, r)
		}),
	))

	return mux
}
