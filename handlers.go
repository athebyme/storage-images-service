package main

import (
	"awesomeProject1/business"
	mydb "awesomeProject1/database"
	"awesomeProject1/types"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// MediaRequest представляет запрос на получение медиа
type MediaRequest struct {
	ProductIDs []int `json:"productIDs"`
}

// MediaResponse карта "ID продукта" -> "список URL изображений"
type MediaResponse map[string][]string

// MediaStatsResponse структура ответа с дополнительной статистикой
type MediaStatsResponse struct {
	TotalCount int      `json:"totalCount"`
	URLs       []string `json:"urls"`
}

// handleDirectLinks возвращает список доступных изображений для указанных ID продуктов
// POST /api/v1/media с телом запроса:
// {"productIDs": [1, 2, 3]}
func (s *Server) handleDirectLinks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "Метод не разрешен", http.StatusMethodNotAllowed)
		return
	}

	// Декодируем тело запроса
	var req MediaRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Ошибка при чтении тела запроса: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Проверяем наличие productIDs
	if len(req.ProductIDs) == 0 {
		// Если ID не указаны, получаем все доступные ID
		existingIDs, err := s.db.GetArticles()
		if err != nil {
			http.Error(w, "Ошибка при получении доступных ID: "+err.Error(), http.StatusInternalServerError)
			return
		}

		for id := range existingIDs {
			req.ProductIDs = append(req.ProductIDs, id)
		}
	}

	// Создаем ответ
	response := make(MediaResponse)

	// Обрабатываем каждый идентификатор
	for _, id := range req.ProductIDs {
		productDir := filepath.Join(storageDir, fmt.Sprintf("%d", id))

		// Проверяем существование директории
		if _, err := os.Stat(productDir); os.IsNotExist(err) {
			continue
		}

		// Читаем файлы из директории
		files, err := ioutil.ReadDir(productDir)
		if err != nil {
			continue
		}

		var urls []string
		for _, file := range files {
			if file.IsDir() || !isImageFile(file.Name()) {
				continue
			}

			imageURL := fmt.Sprintf("%s/%d/%s", localHost, id, file.Name())
			urls = append(urls, imageURL)
		}

		if len(urls) > 0 {
			response[fmt.Sprintf("%d", id)] = urls
		}
	}

	// Отправляем ответ
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "private, max-age=300") // Кэширование на 5 минут

	if len(response) == 0 {
		http.Error(w, "Изображения не найдены", http.StatusNotFound)
		return
	}

	if err := json.NewEncoder(w).Encode(response); err != nil {
		http.Error(w, "Ошибка при кодировании ответа: "+err.Error(), http.StatusInternalServerError)
	}
}

// handleMediaStats получает статистику по изображениям для конкретного продукта
// GET /api/v1/media/stats/{productID}
func (s *Server) handleMediaStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, "Метод не разрешен", http.StatusMethodNotAllowed)
		return
	}

	// Извлекаем ID продукта из URL
	pathParts := strings.Split(r.URL.Path, "/")
	if len(pathParts) < 5 {
		http.Error(w, "Неверный путь запроса", http.StatusBadRequest)
		return
	}

	productIDStr := pathParts[4]
	productID, err := strconv.Atoi(productIDStr)
	if err != nil {
		http.Error(w, "Неверный ID продукта", http.StatusBadRequest)
		return
	}

	// Проверяем наличие в кэше
	if countValue, exists := imageCache.productCounts.Load(productID); exists {
		// Кэш-хит
		imageCache.RecordHit()
		count := countValue.(int)

		// Если 0 изображений, возвращаем пустой ответ
		if count == 0 {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(MediaStatsResponse{
				TotalCount: 0,
				URLs:       []string{},
			})
			return
		}

		// Загружаем URLs
		productDir := filepath.Join(storageDir, fmt.Sprintf("%d", productID))
		files, err := ioutil.ReadDir(productDir)
		if err != nil {
			http.Error(w, "Ошибка при чтении директории", http.StatusInternalServerError)
			return
		}

		var urls []string
		for _, file := range files {
			if file.IsDir() || !isImageFile(file.Name()) {
				continue
			}

			imageURL := fmt.Sprintf("%s/%d/%s", localHost, productID, file.Name())
			urls = append(urls, imageURL)
		}

		response := MediaStatsResponse{
			TotalCount: count,
			URLs:       urls,
		}

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "private, max-age=300") // Кэширование на 5 минут
		json.NewEncoder(w).Encode(response)
		return
	}

	// Кэш-мисс
	imageCache.RecordMiss()

	// Путь к директории продукта
	productDir := filepath.Join(storageDir, fmt.Sprintf("%d", productID))

	// Проверяем существование директории
	if _, err := os.Stat(productDir); os.IsNotExist(err) {
		// Если директория не существует, запоминаем это в кэше и возвращаем пустой ответ
		imageCache.productCounts.Store(productID, 0)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(MediaStatsResponse{
			TotalCount: 0,
			URLs:       []string{},
		})
		return
	}

	// Читаем файлы из директории
	files, err := ioutil.ReadDir(productDir)
	if err != nil {
		http.Error(w, "Ошибка при чтении директории", http.StatusInternalServerError)
		return
	}

	var urls []string
	imageCount := 0

	for _, file := range files {
		if file.IsDir() || !isImageFile(file.Name()) {
			continue
		}

		imageCount++
		imageURL := fmt.Sprintf("%s/%d/%s", localHost, productID, file.Name())
		urls = append(urls, imageURL)
	}

	// Сохраняем в кэш
	imageCache.productCounts.Store(productID, imageCount)

	// Формируем ответ
	response := MediaStatsResponse{
		TotalCount: imageCount,
		URLs:       urls,
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "private, max-age=300") // Кэширование на 5 минут
	json.NewEncoder(w).Encode(response)
}

// handleTotalImagesStats получает общую статистику по изображениям
// GET /api/v1/media/stats
func (s *Server) handleTotalImagesStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, "Метод не разрешен", http.StatusMethodNotAllowed)
		return
	}

	// Проверяем кэш
	imageCache.countMutex.RLock()
	cachedCount := imageCache.totalCount
	imageCache.countMutex.RUnlock()

	if cachedCount > 0 {
		// Используем кэшированное значение
		imageCache.RecordHit()
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "private, max-age=300") // Кэширование на 5 минут
		json.NewEncoder(w).Encode(map[string]int{
			"totalCount": cachedCount,
		})
		return
	}

	// Кэш-мисс, считаем заново
	imageCache.RecordMiss()

	totalCount := 0

	// Обходим директорию хранения
	err := filepath.Walk(storageDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Пропускаем директории и специальные файлы
		if info.IsDir() || strings.HasPrefix(info.Name(), ".") {
			return nil
		}

		// Проверяем, что это изображение
		if isImageFile(info.Name()) {
			totalCount++
		}

		return nil
	})

	if err != nil {
		http.Error(w, "Ошибка при подсчете изображений", http.StatusInternalServerError)
		return
	}

	// Сохраняем в кэш
	imageCache.countMutex.Lock()
	imageCache.totalCount = totalCount
	imageCache.countMutex.Unlock()

	// Отправляем ответ
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "private, max-age=300") // Кэширование на 5 минут
	json.NewEncoder(w).Encode(map[string]int{
		"totalCount": totalCount,
	})
}

// handleCacheStats возвращает статистику кэша
// GET /api/v1/cache/stats
func (s *Server) handleCacheStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, "Метод не разрешен", http.StatusMethodNotAllowed)
		return
	}

	stats := imageCache.GetCacheStats()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stats)
}

// handleClearCache очищает кэш
// POST /api/v1/cache/clear
func (s *Server) handleClearCache(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "Метод не разрешен", http.StatusMethodNotAllowed)
		return
	}

	imageCache.Clear()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status":  "ok",
		"message": "Кэш очищен",
	})
}

// handleImageRequest обрабатывает запросы на получение изображений с оптимизированным кэшированием
func (s *Server) handleImageRequest(w http.ResponseWriter, r *http.Request) {
	pathParts := strings.Split(r.URL.Path, "/")
	if len(pathParts) < 3 {
		http.Error(w, "Неверный путь запроса", http.StatusBadRequest)
		return
	}

	productID := pathParts[1]
	fileName := pathParts[2]
	imagePath := filepath.Join(storageDir, productID, fileName)

	// Проверяем кэш метаданных
	if metadataValue, exists := imageCache.metadata.Load(imagePath); exists {
		metadata := metadataValue.(FileMetadata)

		// Проверяем условный запрос
		if match := r.Header.Get("If-None-Match"); match != "" && match == metadata.ETag {
			w.WriteHeader(http.StatusNotModified)
			imageCache.RecordHit()
			return
		}

		// Устанавливаем заголовки кэширования
		w.Header().Set("ETag", metadata.ETag)
		w.Header().Set("Last-Modified", metadata.LastModified.Format(http.TimeFormat))
		w.Header().Set("Cache-Control", "public, max-age=86400") // 1 день
		w.Header().Set("Content-Type", metadata.ContentType)

		// Проверяем кэш данных изображения
		if imageData, exists := imageCache.imageData.Load(imagePath); exists {
			// Отдаем из кэша, если размер не слишком большой
			data := imageData.([]byte)
			if len(data) < 10*1024*1024 { // Не кэшируем файлы > 10MB в памяти
				imageCache.RecordHit()
				http.ServeContent(w, r, fileName, metadata.LastModified,
					bytes.NewReader(data))
				return
			}
		}
	}

	// Кэш-мисс
	imageCache.RecordMiss()

	// Проверяем существование файла
	fileInfo, err := os.Stat(imagePath)
	if os.IsNotExist(err) {
		http.Error(w, "Изображение не найдено", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "Ошибка при получении информации о файле", http.StatusInternalServerError)
		return
	}

	// Определяем MIME-тип
	contentType := getContentType(fileName)

	// Генерируем ETag на основе последнего времени модификации и размера
	etag := fmt.Sprintf(`"%x-%x"`, fileInfo.ModTime().Unix(), fileInfo.Size())

	// Проверяем условный запрос
	if match := r.Header.Get("If-None-Match"); match != "" && match == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	// Устанавливаем заголовки кэширования
	w.Header().Set("ETag", etag)
	w.Header().Set("Last-Modified", fileInfo.ModTime().Format(http.TimeFormat))
	w.Header().Set("Cache-Control", "public, max-age=86400") // 1 день
	w.Header().Set("Content-Type", contentType)

	// Сохраняем метаданные в кэше
	metadata := FileMetadata{
		ETag:         etag,
		LastModified: fileInfo.ModTime(),
		ContentType:  contentType,
		Size:         fileInfo.Size(),
	}
	imageCache.metadata.Store(imagePath, metadata)

	// Если файл не слишком большой, кэшируем его содержимое
	if fileInfo.Size() < 10*1024*1024 { // Не кэшируем файлы > 10MB
		data, err := os.ReadFile(imagePath)
		if err == nil {
			imageCache.imageData.Store(imagePath, data)
			http.ServeContent(w, r, fileName, fileInfo.ModTime(), bytes.NewReader(data))
			return
		}
	}

	// Если файл большой или возникла ошибка чтения, открываем и отдаем обычным способом
	file, err := os.Open(imagePath)
	if err != nil {
		http.Error(w, "Ошибка при открытии файла", http.StatusInternalServerError)
		return
	}
	defer file.Close()

	// Используем ServeContent для поддержки Range-запросов и правильных заголовков
	http.ServeContent(w, r, fileName, fileInfo.ModTime(), file)
}

// handleAnonymousPackageImage обрабатывает запросы на анонимные пакетные изображения
func (s *Server) handleAnonymousPackageImage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, "Метод не разрешен", http.StatusMethodNotAllowed)
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
		http.Error(w, "Неверный формат изображения", http.StatusBadRequest)
		return
	}

	filePath := filepath.Join("./storage/!additional/", filename)

	// Проверяем кэш
	if imageData, exists := imageCache.imageData.Load(filePath); exists {
		// Отдаем из кэша
		imageCache.RecordHit()
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Cache-Control", "public, max-age=86400") // 1 день
		w.Write(imageData.([]byte))
		return
	}

	// Кэш-мисс
	imageCache.RecordMiss()

	// Получаем файл
	data, err := os.ReadFile(filePath)
	if err != nil {
		http.Error(w, "Изображение не найдено", http.StatusNotFound)
		return
	}

	// Кэшируем для будущих запросов
	imageCache.imageData.Store(filePath, data)

	// Отдаем файл
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "public, max-age=86400") // 1 день
	w.Write(data)
}

// handleSimilarity обрабатывает запросы для вычисления сходства строк
func (s *Server) handleSimilarity(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, "Метод не разрешен", http.StatusMethodNotAllowed)
		return
	}

	// Получаем параметры из URL
	s1 := r.URL.Query().Get("s1")
	s2 := r.URL.Query().Get("s2")

	if s1 == "" || s2 == "" {
		http.Error(w, "Отсутствуют обязательные параметры s1 и s2", http.StatusBadRequest)
		return
	}

	// Вычисляем сходство
	sim := business.Similarity(s1, s2)

	// Возвращаем результат
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]float64{"similarity": sim})
}

// handleImageStatusRequest возвращает статус обработки изображений
func (s *Server) handleImageStatusRequest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, "Метод не разрешен", http.StatusMethodNotAllowed)
		return
	}

	// Получаем параметры из URL
	idsParam := r.URL.Query().Get("ids")
	var productIDs []int

	if idsParam != "" {
		idStrs := strings.Split(idsParam, ",")
		for _, idStr := range idStrs {
			id, err := strconv.Atoi(strings.TrimSpace(idStr))
			if err != nil {
				http.Error(w, fmt.Sprintf("Неверный формат ID: %s", idStr), http.StatusBadRequest)
				return
			}
			productIDs = append(productIDs, id)
		}
	}

	// Получаем статусы из БД
	var statuses []types.ProcessStatus
	var err error

	if len(productIDs) == 0 {
		statuses, err = s.db.GetAllImageStatuses()
	} else {
		statuses, err = s.db.GetImageStatusesByProductIDs(productIDs)
	}

	if err != nil {
		http.Error(w, fmt.Sprintf("Ошибка при получении статусов: %v", err), http.StatusInternalServerError)
		return
	}

	// Отправляем ответ
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache")
	if err := json.NewEncoder(w).Encode(statuses); err != nil {
		http.Error(w, "Ошибка при кодировании ответа", http.StatusInternalServerError)
	}
}

// handleMediaProcessing обрабатывает запросы на обработку медиафайлов (асинхронная операция)
func (s *Server) handleMediaProcessing(w http.ResponseWriter, r *http.Request) {
	// Проверяем аутентификацию (эта проверка уже выполняется в маршрутизаторе, но можно продублировать)
	apiKey := r.Header.Get("X-API-Key")
	if apiKey != API_UPDATE_PASSWORD {
		w.Header().Set("WWW-Authenticate", "Basic realm=\"Restricted\"")
		http.Error(w, "Unauthorized: Invalid API key", http.StatusUnauthorized)
		return
	}

	var req MediaRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Неверный формат запроса: "+err.Error(), http.StatusBadRequest)
		return
	}

	if len(req.ProductIDs) == 0 {
		http.Error(w, "Список productIDs не может быть пустым", http.StatusBadRequest)
		return
	}

	// Начинаем асинхронную обработку
	go func() {
		ctx := context.Background()
		ctx = context.WithValue(ctx, "db", s.db)

		// Получаем существующие ID из базы данных
		ids, err := s.db.GetArticles()
		if err != nil {
			log.Printf("Ошибка при получении существующих статей: %v", err)
			return
		}
		ctx = context.WithValue(ctx, "exist_ids", ids)

		// Получаем данные из удаленного API
		remoteMedia, err := fetchRemoteMedia(req.ProductIDs)
		if err != nil {
			log.Printf("Ошибка при получении медиа из удаленного API: %v", err)
			return
		}

		// Создаем канал для обработки статусов
		statusChan := make(chan types.ProcessStatus, len(req.ProductIDs))

		// Запускаем обработку статусов
		go func() {
			for status := range statusChan {
				if err := s.db.UpdateImageStatus(status); err != nil {
					log.Printf("Ошибка при обновлении статуса: %v", err)
				}
			}
		}()

		// Обрабатываем медиафайлы
		if err := processMedia(ctx, remoteMedia, statusChan); err != nil {
			log.Printf("Ошибка при обработке медиа: %v", err)
		}

		// После завершения обработки очищаем кэш для обновления статистики
		imageCache.Clear()
	}()

	// Возвращаем статус 202 Accepted для асинхронных операций
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "processing",
		"statusUrl": fmt.Sprintf("/api/v1/media/status?ids=%s",
			strings.Trim(strings.Join(strings.Fields(fmt.Sprint(req.ProductIDs)), ","), "[]")),
		"message": "Обработка изображений запущена",
	})
}

// fetchRemoteMedia получает данные о медиафайлах из удаленного API
func fetchRemoteMedia(productIDs []int) (MediaResponse, error) {
	requestBody, err := json.Marshal(MediaRequest{ProductIDs: productIDs})
	if err != nil {
		return nil, fmt.Errorf("ошибка при маршалинге запроса: %w", err)
	}

	// Создаем HTTP клиент с таймаутом
	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	resp, err := client.Post(remoteAPI, "application/json", bytes.NewReader(requestBody))
	if err != nil {
		return nil, fmt.Errorf("ошибка при запросе к удаленному API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("удаленный API вернул ошибку: %s, код: %d, тело: %s",
			resp.Status, resp.StatusCode, string(body))
	}

	var mediaResponse MediaResponse
	if err := json.NewDecoder(resp.Body).Decode(&mediaResponse); err != nil {
		return nil, fmt.Errorf("ошибка при декодировании ответа API: %w", err)
	}

	return mediaResponse, nil
}

// processMedia обрабатывает полученные медиафайлы
func processMedia(ctx context.Context, media MediaResponse, statusChan chan<- types.ProcessStatus) error {
	defer close(statusChan)

	idsSync := sync.Map{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	var batchWG sync.WaitGroup

	// Получаем существующие ID из контекста
	ids, ok := ctx.Value("exist_ids").(map[int]struct{})
	if ok {
		for id := range ids {
			idsSync.Store(id, struct{}{})
		}
	}

	// Ограничиваем частоту запросов
	limiter := time.NewTicker(time.Minute / 200)
	defer limiter.Stop()

	// Каналы для обработки ошибок и результатов
	errCh := make(chan error, len(media))
	photoLoadCh := make(chan mydb.Article, 100)
	semaphore := make(chan struct{}, 100) // Ограничиваем количество параллельных операций

	// Получаем драйвер БД из контекста
	driver, ok := ctx.Value("db").(*mydb.PostgresDriver)
	if !ok {
		return fmt.Errorf("драйвер БД не установлен в контексте")
	}

	// Канал для пакетной обработки
	batch := make(chan []mydb.Article, 1)

	// Горутина для накопления записей в пакеты
	batchWG.Add(1)
	go func() {
		defer batchWG.Done()
		var loadBatch []mydb.Article
		defer close(batch)

		for article := range photoLoadCh {
			loadBatch = append(loadBatch, article)
			log.Printf("Товар %d обработан", article.ID)

			// Если набрали 100 записей, отправляем пакет
			if len(loadBatch) == 100 {
				batch <- loadBatch
				loadBatch = []mydb.Article{}
			}
		}

		// Отправляем оставшиеся записи
		if len(loadBatch) > 0 {
			batch <- loadBatch
		}
	}()

	// Горутина для пакетной записи в БД
	batchWG.Add(1)
	go func() {
		defer batchWG.Done()
		for locBatch := range batch {
			err := driver.CreateRecords(locBatch)
			if err != nil {
				log.Printf("Ошибка при записи пакета в БД: %v", err)
				errCh <- err
				return
			}
			log.Printf("Пакет из %d записей успешно записан в БД", len(locBatch))
		}
	}()

	// Обрабатываем медиафайлы для каждого продукта
	for productID, urls := range media {
		wg.Add(1)
		go func(productID string, urls []string) {
			defer wg.Done()

			// Массив для хранения локальных URL
			localURLs := make([]string, len(urls))

			// Конвертируем ID в число
			id, err := strconv.Atoi(productID)
			if err != nil {
				log.Printf("Ошибка конвертации ID продукта: %v", err)
				statusChan <- types.ProcessStatus{ProductID: 0, Status: "error", Timestamp: time.Now()}
				errCh <- err
				return
			}

			// Проверяем, существует ли уже продукт
			if _, ok := idsSync.Load(id); ok {
				statusChan <- types.ProcessStatus{ProductID: id, Status: "exists", Timestamp: time.Now()}
				return
			}

			// Обрабатываем каждый URL изображения
			for i, url := range urls {
				select {
				case <-ctx.Done():
					log.Printf("Контекст отменен")
					statusChan <- types.ProcessStatus{ProductID: id, Status: "cancelled", Timestamp: time.Now()}
					errCh <- ctx.Err()
					return
				default:
					// Продолжаем обработку
				}

				// Соблюдаем ограничение частоты запросов
				<-limiter.C

				// Используем семафор для ограничения параллельных загрузок
				semaphore <- struct{}{}
				func() {
					defer func() { <-semaphore }()

					// Создаем директорию для хранения изображений
					outputDir := filepath.Join(storageDir, productID)
					if err := os.MkdirAll(outputDir, os.ModePerm); err != nil {
						log.Printf("Ошибка при создании директории %s: %v", outputDir, err)
						statusChan <- types.ProcessStatus{ProductID: id, Status: "error", Timestamp: time.Now()}
						errCh <- err
						return
					}

					// Пути для временного и итогового файлов
					outputPath := filepath.Join(outputDir, fmt.Sprintf("%d.jpg", i))
					tempPath := outputPath + ".tmp"

					// Загружаем изображение
					if err := downloadImage(ctx, url, tempPath); err != nil {
						log.Printf("Ошибка при загрузке изображения для товара %d: %v", id, err)
						statusChan <- types.ProcessStatus{ProductID: id, Status: "error", Timestamp: time.Now()}
						errCh <- err
						return
					}

					// Открываем временный файл для обработки
					file, err := os.Open(tempPath)
					if err != nil {
						log.Printf("Ошибка при открытии временного файла для товара %d: %v", id, err)
						statusChan <- types.ProcessStatus{ProductID: id, Status: "error", Timestamp: time.Now()}
						errCh <- err
						return
					}

					// Преобразуем изображение в квадратный формат
					if err := makeSquareImage(file, outputPath); err != nil {
						log.Printf("Ошибка при создании квадратного изображения для товара %d: %v", id, err)
						statusChan <- types.ProcessStatus{ProductID: id, Status: "error", Timestamp: time.Now()}
						errCh <- err
						return
					}

					// Закрываем файл и удаляем временный
					defer func() {
						_ = file.Close()
						_ = os.Remove(tempPath)
					}()

					// Формируем URL для локального изображения
					localURLs[i] = fmt.Sprintf("%s/%s/%d.jpg", localHost, productID, i)
				}()
			}

			// Все изображения обработаны успешно
			mu.Lock()
			statusChan <- types.ProcessStatus{ProductID: id, Status: "ok", Timestamp: time.Now()}
			photoLoadCh <- mydb.Article{ID: id, Photos: localURLs}
			mu.Unlock()

		}(productID, urls)
	}

	// Ждем завершения обработки всех изображений
	wg.Wait()
	close(photoLoadCh)

	// Ждем завершения записи всех пакетов
	batchWG.Wait()

	// Закрываем семафор
	close(semaphore)

	// Проверяем наличие ошибок
	if len(errCh) > 0 {
		return <-errCh
	}

	return nil
}

// downloadImage загружает изображение по URL
func downloadImage(ctx context.Context, url, outputPath string) error {
	// Создаем HTTP-запрос с контекстом
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("ошибка при создании запроса: %w", err)
	}

	// Используем клиент с таймаутом
	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	// Выполняем запрос
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("ошибка при загрузке изображения: %w", err)
	}
	defer resp.Body.Close()

	// Проверяем код ответа
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ошибка при загрузке изображения: %s", resp.Status)
	}

	// Создаем файл для сохранения изображения
	file, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("ошибка при создании файла: %w", err)
	}
	defer file.Close()

	// Копируем данные
	_, err = io.Copy(file, resp.Body)
	if err != nil {
		return fmt.Errorf("ошибка при записи файла: %w", err)
	}

	return nil
}

// makeSquareImage преобразует изображение в квадратный формат
func makeSquareImage(input io.Reader, outputPath string) error {
	// Декодируем изображение
	img, _, err := image.Decode(input)
	if err != nil {
		return fmt.Errorf("ошибка при декодировании изображения: %w", err)
	}

	originalBounds := img.Bounds()
	width := originalBounds.Dx()
	height := originalBounds.Dy()

	// Определяем размер квадратного изображения
	size := width
	if height > width {
		size = height
	}

	// Создаем новое изображение
	squareImage := image.NewRGBA(image.Rect(0, 0, size, size))
	white := color.RGBA{255, 255, 255, 255}
	draw.Draw(squareImage, squareImage.Bounds(), &image.Uniform{white}, image.Point{}, draw.Src)

	// Размещаем исходное изображение по центру
	offsetX := (size - width) / 2
	offsetY := (size - height) / 2
	draw.Draw(squareImage, image.Rect(offsetX, offsetY, offsetX+width, offsetY+height),
		img, originalBounds.Min, draw.Over)

	// Сохраняем результат
	outputFile, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("ошибка при создании выходного файла: %w", err)
	}
	defer outputFile.Close()

	// Кодируем изображение в JPEG
	return jpeg.Encode(outputFile, squareImage, nil)
}
