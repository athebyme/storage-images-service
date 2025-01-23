package main

import (
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
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Server struct {
	db mydb.Driver
}

func NewServer(db mydb.Driver) *Server {
	return &Server{db: db}
}

type MediaRequest struct {
	ProductIDs []int `json:"productIDs"`
}

type MediaResponse map[string][]string

const (
	remoteAPI  = "https://api.athebyme-market.ru/api/media"
	localHost  = "http://media.athebyme-market.ru"
	storageDir = "./storage"
)

func makeSquareImage(input io.Reader, outputPath string) error {
	img, _, err := image.Decode(input)
	if err != nil {
		return err
	}

	originalBounds := img.Bounds()
	width := originalBounds.Dx()
	height := originalBounds.Dy()

	size := width
	if height > width {
		size = height
	}

	squareImage := image.NewRGBA(image.Rect(0, 0, size, size))
	white := color.RGBA{255, 255, 255, 255}
	draw.Draw(squareImage, squareImage.Bounds(), &image.Uniform{white}, image.Point{}, draw.Src)

	offsetX := (size - width) / 2
	offsetY := (size - height) / 2
	draw.Draw(squareImage, image.Rect(offsetX, offsetY, offsetX+width, offsetY+height), img, originalBounds.Min, draw.Over)

	outputFile, err := os.Create(outputPath)
	if err != nil {
		return err
	}
	defer outputFile.Close()

	return jpeg.Encode(outputFile, squareImage, nil)
}

func downloadImage(ctx context.Context, url, outputPath string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to download image: %s", resp.Status)
	}

	file, err := os.Create(outputPath)
	if err != nil {
		return err
	}
	defer file.Close()

	_, err = io.Copy(file, resp.Body)
	return err
}

func fetchRemoteMedia(productIDs []int) (MediaResponse, error) {
	requestBody, err := json.Marshal(MediaRequest{ProductIDs: productIDs})
	if err != nil {
		return nil, err
	}

	resp, err := http.Post(remoteAPI, "application/json", bytes.NewReader(requestBody))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("remote API error: %s", resp.Status)
	}

	var mediaResponse MediaResponse
	if err := json.NewDecoder(resp.Body).Decode(&mediaResponse); err != nil {
		return nil, err
	}

	return mediaResponse, nil
}

func (s *Server) handleMediaRequest(w http.ResponseWriter, r *http.Request) {
	log.Printf("Signal received")

	// Ответим сразу с кодом 200
	w.WriteHeader(http.StatusOK)
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"status":"processing"}`))

	// Создаем новый контекст для асинхронной обработки
	ctx := context.Background()
	ctx = context.WithValue(ctx, "db", s.db)
	ids, err := s.db.GetArticles()
	if err != nil {
		log.Fatalf("Cant get exist articles from db!")
	}

	ctx = context.WithValue(ctx, "exist_ids", ids)

	var req MediaRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		log.Printf("Invalid request: %v", err)
		return
	}

	// Выполнение долгой задачи в новой горутине
	go func() {
		defer ctx.Done()

		statusChan := make(chan types.ProcessStatus, len(req.ProductIDs)) // Новый канал для статусов

		log.Printf("Fetch data from remote API...")
		remoteMedia, err := fetchRemoteMedia(req.ProductIDs)
		if err != nil {
			log.Printf("Error fetching remote media: %v", err)
			return
		}

		// Обработка статусов после завершения обработки медиа
		go func() {
			for status := range statusChan {
				if status.Status == "error" {
					log.Printf("API error: %s | ID: %d", status.Status, status.ProductID)
				}
				if err := s.db.UpdateImageStatus(status); err != nil {
					log.Printf("Failed to update image status: %v", err)
				}
			}
		}()

		log.Printf("Processing media...")
		if err = processMedia(ctx, remoteMedia, statusChan); err != nil {
			log.Printf("Error processing media: %v", err)
			return
		}

		log.Printf("Media processing completed successfully")
	}()
}

func processMedia(ctx context.Context, media MediaResponse, statusChan chan<- types.ProcessStatus) error {
	defer close(statusChan)

	idsSync := sync.Map{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	var batchWG sync.WaitGroup

	ids, ok := ctx.Value("exist_ids").(map[int]struct{})
	if ok {
		for id := range ids {
			idsSync.Store(id, struct{}{})
		}
	}

	limiter := time.NewTicker(time.Minute / 200)
	defer limiter.Stop()

	errCh := make(chan error, len(media))
	photoLoadCh := make(chan mydb.Article, 100)
	semaphore := make(chan struct{}, 100)

	driver, ok := ctx.Value("db").(*mydb.PostgresDriver)
	if !ok {
		return fmt.Errorf("db is not set to the context")
	}

	batch := make(chan []mydb.Article, 1)

	batchWG.Add(1)
	go func() {
		defer batchWG.Done()
		var loadBatch []mydb.Article
		defer close(batch)

		for article := range photoLoadCh {
			loadBatch = append(loadBatch, article)
			log.Printf("Article %d is done", article.ID)

			if len(loadBatch) == 100 {
				batch <- loadBatch
				loadBatch = []mydb.Article{}
			}
		}

		if len(loadBatch) > 0 {
			batch <- loadBatch
		}
	}()

	batchWG.Add(1)
	go func() {
		defer batchWG.Done()
		for locBatch := range batch {
			err := driver.CreateRecords(locBatch)
			if err != nil {
				log.Printf("Failed to upload batch: %v", err)
				errCh <- err
				return
			}
			log.Printf("Uploaded batch")
		}
	}()

	for productID, urls := range media {
		wg.Add(1)
		go func(productID string, urls []string) {
			defer wg.Done()

			localURLs := make([]string, len(urls))
			id, err := strconv.Atoi(productID)
			if err != nil {
				log.Printf("Failed to convert productID to int: %v", err)
				statusChan <- types.ProcessStatus{ProductID: id, Status: "error", Timestamp: time.Now()}
				errCh <- err
				return
			}

			if _, ok := idsSync.Load(id); ok {
				statusChan <- types.ProcessStatus{ProductID: id, Status: "exists", Timestamp: time.Now()}
				return
			}

			for i, url := range urls {
				select {
				case <-ctx.Done():
					log.Printf("Context done!")
					statusChan <- types.ProcessStatus{ProductID: id, Status: "cancelled", Timestamp: time.Now()}
					errCh <- ctx.Err()
					return
				default:
				}

				<-limiter.C

				semaphore <- struct{}{}
				func() {
					defer func() { <-semaphore }()

					outputDir := filepath.Join(storageDir, productID)
					if err := os.MkdirAll(outputDir, os.ModePerm); err != nil {
						statusChan <- types.ProcessStatus{ProductID: id, Status: "error", Timestamp: time.Now()}
						errCh <- err
						return
					}
					outputPath := filepath.Join(outputDir, fmt.Sprintf("%d.jpg", i))

					tempPath := outputPath + ".tmp"
					if err := downloadImage(ctx, url, tempPath); err != nil {
						statusChan <- types.ProcessStatus{ProductID: id, Status: "error", Timestamp: time.Now()}
						errCh <- err
						return
					}

					file, err := os.Open(tempPath)
					if err != nil {
						statusChan <- types.ProcessStatus{ProductID: id, Status: "error", Timestamp: time.Now()}
						errCh <- err
						return
					}

					if err := makeSquareImage(file, outputPath); err != nil {
						statusChan <- types.ProcessStatus{ProductID: id, Status: "error", Timestamp: time.Now()}
						errCh <- err
						return
					}

					defer func() {
						_ = file.Close()
						_ = os.Remove(tempPath)
					}()

					localURLs[i] = fmt.Sprintf("%s/%s/%d.jpg", localHost, productID, i)
				}()
			}

			mu.Lock()
			statusChan <- types.ProcessStatus{ProductID: id, Status: "ok", Timestamp: time.Now()}
			photoLoadCh <- mydb.Article{ID: id, Photos: localURLs}
			mu.Unlock()

		}(productID, urls)
	}

	wg.Wait()
	close(photoLoadCh)

	batchWG.Wait()

	close(semaphore)
	ctx.Done()

	if len(errCh) > 0 {
		return <-errCh
	}

	return nil
}

func (s *Server) handleDirectLinks(w http.ResponseWriter, r *http.Request) {
	body, err := ioutil.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Не удалось прочитать тело запроса", http.StatusBadRequest)
		return
	}

	var request MediaRequest
	err = json.Unmarshal(body, &request)
	if err != nil {
		http.Error(w, "Ошибка при обработке JSON", http.StatusBadRequest)
		return
	}

	storageDir := "./storage"

	var imageLinks []string

	for _, productID := range request.ProductIDs {
		productDir := filepath.Join(storageDir, fmt.Sprintf("%d", productID))

		if _, err := ioutil.ReadDir(productDir); err != nil {
			continue
		}

		files, err := ioutil.ReadDir(productDir)
		if err != nil {
			http.Error(w, "Ошибка при чтении папки с изображениями", http.StatusInternalServerError)
			return
		}

		for _, file := range files {
			imageLink := fmt.Sprintf("%s/%d/%s", localHost, productID, file.Name())
			imageLinks = append(imageLinks, imageLink)
		}
	}

	if len(imageLinks) > 0 {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(imageLinks)
	} else {
		http.Error(w, "Изображения не найдены", http.StatusNotFound)
	}
}

func (s *Server) handleImageRequest(w http.ResponseWriter, r *http.Request) {
	pathParts := strings.Split(r.URL.Path, "/")
	if len(pathParts) < 3 {
		http.Error(w, "Неверный путь запроса", http.StatusBadRequest)
		return
	}

	productID := pathParts[1]
	fileName := pathParts[2]

	imageDir := "./storage/" + productID

	imagePath := filepath.Join(imageDir, fileName)

	if _, err := os.Stat(imagePath); os.IsNotExist(err) {
		http.Error(w, "Изображение не найдено", http.StatusNotFound)
		return
	}

	ext := filepath.Ext(imagePath)
	switch ext {
	case ".jpg", ".jpeg":
		w.Header().Set("Content-Type", "image/jpeg")
	case ".png":
		w.Header().Set("Content-Type", "image/png")
	case ".gif":
		w.Header().Set("Content-Type", "image/gif")
	default:
		http.Error(w, "Неподдерживаемый формат изображения", http.StatusUnsupportedMediaType)
		return
	}

	http.ServeFile(w, r, imagePath)
}
func (s *Server) handleImageStatusRequest(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ProductIDs []int `json:"productIDs"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	var statuses []types.ProcessStatus
	var err error

	if len(req.ProductIDs) == 0 {
		statuses, err = s.db.GetAllImageStatuses()
	} else {
		statuses, err = s.db.GetImageStatusesByProductIDs(req.ProductIDs)
	}

	if err != nil {
		http.Error(w, fmt.Sprintf("Error fetching image statuses: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(statuses); err != nil {
		http.Error(w, "Error encoding response", http.StatusInternalServerError)
	}
}

func main() {
	runtime.GOMAXPROCS(5)
	if err := os.MkdirAll(storageDir, os.ModePerm); err != nil {
		log.Fatalf("Failed to create storage directory: %v", err)
	}
	dbConfig := mydb.PostgresConfiguration{}
	cfg, err := dbConfig.LoadConfig("./config/config.yaml")

	if err != nil {
		log.Fatalf("Error loading config: %v", err)
	}

	db, err := mydb.NewPostgresDriver(cfg.GetConnectionString())

	if err != nil {
		log.Fatalf("Error connecting to database: %v", err)
	}

	if err = db.MigrateUp(); err != nil {
		log.Fatalf("Error migrating database: %v", err)
	}

	server := NewServer(db)

	http.HandleFunc("/api/media/update", server.handleMediaRequest)
	http.HandleFunc("/api/media", server.handleDirectLinks)
	http.HandleFunc("/", server.handleImageRequest)

	port := 8081
	log.Printf("Server is running on port %d", port)
	if err := http.ListenAndServe(":"+strconv.Itoa(port), nil); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}
