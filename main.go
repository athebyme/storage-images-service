package main

import (
	mydb "awesomeProject1/database"
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

type Server struct {
	db mydb.DatabaseDriver
}

func NewServer(db mydb.DatabaseDriver) *Server {
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

// makeSquareImage transforms an image to a square by adding white padding.
func makeSquareImage(input io.Reader, outputPath string) error {
	img, _, err := image.Decode(input)
	if err != nil {
		return err
	}

	// Get original dimensions
	originalBounds := img.Bounds()
	width := originalBounds.Dx()
	height := originalBounds.Dy()

	// Determine canvas size
	size := width
	if height > width {
		size = height
	}

	// Create a new square image with white background
	squareImage := image.NewRGBA(image.Rect(0, 0, size, size))
	white := color.RGBA{255, 255, 255, 255}
	draw.Draw(squareImage, squareImage.Bounds(), &image.Uniform{white}, image.Point{}, draw.Src)

	// Center the original image
	offsetX := (size - width) / 2
	offsetY := (size - height) / 2
	draw.Draw(squareImage, image.Rect(offsetX, offsetY, offsetX+width, offsetY+height), img, originalBounds.Min, draw.Over)

	// Save the output image
	outputFile, err := os.Create(outputPath)
	if err != nil {
		return err
	}
	defer outputFile.Close()

	return jpeg.Encode(outputFile, squareImage, nil)
}

// downloadImage downloads an image from a URL and saves it to a file.
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
	log.Printf("Dowloaded image %s", url)

	file, err := os.Create(outputPath)
	if err != nil {
		return err
	}
	defer file.Close()

	_, err = io.Copy(file, resp.Body)
	return err
}

// fetchRemoteMedia fetches media data from the remote API.
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

// processMedia downloads, converts, and stores images with rate limiting.
func processMedia(ctx context.Context, media MediaResponse) (MediaResponse, error) {
	localMedia := make(MediaResponse)
	idsSync := sync.Map{}

	var mu sync.Mutex
	var wg sync.WaitGroup

	ids, ok := ctx.Value("exist_ids").(map[int]struct{})
	if ok {
		for id := range ids {
			idsSync.Store(id, struct{}{})
		}
	}

	semaphore := make(chan struct{}, 30)         // Limit to 30 concurrent downloads
	limiter := time.NewTicker(time.Minute / 200) // Limit to 300 downloads per minute
	defer limiter.Stop()

	errCh := make(chan error, len(media))

	for productID, urls := range media {
		wg.Add(1)
		go func(productID string, urls []string) {
			id, err := strconv.Atoi(productID)
			if err != nil {
				errCh <- err
			}

			if _, ok := idsSync.Load(id); ok {
				return
			}

			localURLs := make([]string, len(urls))

			defer wg.Done()
			defer func() {
				id, err := strconv.Atoi(productID)
				if err != nil {
					errCh <- err
				}

				driver, ok := ctx.Value("db").(*mydb.PostgresDriver)
				if !ok {
					errCh <- fmt.Errorf("db is not set to the context")
				}

				err = driver.CreateRecord(mydb.Article{ID: id, Photos: localURLs})
				if err != nil {
					errCh <- err
				}
				log.Printf("created db record")

			}()

			for i, url := range urls {

				select {
				case <-ctx.Done():
					log.Printf("ctx.Done()")
					errCh <- ctx.Err()
					return
				default:
				}

				// Rate limiting
				<-limiter.C

				// Acquire semaphore
				semaphore <- struct{}{}
				func() {
					defer func() {
						<-semaphore
					}() // Release semaphore

					// Prepare paths
					outputDir := filepath.Join(storageDir, productID)
					if err := os.MkdirAll(outputDir, os.ModePerm); err != nil {
						errCh <- err
						return
					}
					outputPath := filepath.Join(outputDir, fmt.Sprintf("%d.jpg", i))

					// Download image
					tempPath := outputPath + ".tmp"
					if err := downloadImage(ctx, url, tempPath); err != nil {
						errCh <- err
						return
					}

					// Convert image
					file, err := os.Open(tempPath)
					if err != nil {
						errCh <- err
						return
					}

					if err := makeSquareImage(file, outputPath); err != nil {
						errCh <- err
						return
					}

					// remove temp file
					defer func() {
						err = file.Close()
						if err != nil {
							errCh <- err
						}
						err = os.Remove(tempPath)
						if err != nil {
							errCh <- err
						}
					}()

					// Add to local URLs
					localURLs[i] = fmt.Sprintf("%s/%s/%d.jpg", localHost, productID, i)
				}()
			}

			mu.Lock()
			localMedia[productID] = localURLs
			mu.Unlock()
		}(productID, urls)
	}

	wg.Wait()
	close(errCh)

	if len(errCh) > 0 {
		return nil, <-errCh
	}

	return localMedia, nil
}

func (s *Server) handleMediaRequest(w http.ResponseWriter, r *http.Request) {

	log.Printf("Signal received")

	ctx, cancel := context.WithTimeout(r.Context(), 500*time.Minute)
	ctx = context.WithValue(ctx, "db", s.db)
	ids, err := s.db.GetArticles()
	if err != nil {
		log.Fatalf("Cant get exist articles from db !")
	}

	ctx = context.WithValue(ctx, "exist_ids", ids)
	defer cancel()

	var req MediaRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	// Fetch data from remote API
	log.Printf("Fetch data from remote API...")
	remoteMedia, err := fetchRemoteMedia(req.ProductIDs)
	if err != nil {
		http.Error(w, fmt.Sprintf("Error fetching remote media: %v", err), http.StatusInternalServerError)
		return
	}

	// Process media
	log.Printf("Processing media...")
	localMedia, err := processMedia(ctx, remoteMedia)
	if err != nil {
		http.Error(w, fmt.Sprintf("Error processing media: %v", err), http.StatusInternalServerError)
		return
	}

	// Return local media response
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(localMedia); err != nil {
		http.Error(w, "Error encoding response", http.StatusInternalServerError)
	}
}

func (s *Server) handleDirectLinks(w http.ResponseWriter, r *http.Request) {
	// Чтение тела запроса
	body, err := ioutil.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Не удалось прочитать тело запроса", http.StatusBadRequest)
		return
	}

	// Декодирование JSON в структуру
	var request MediaRequest
	err = json.Unmarshal(body, &request)
	if err != nil {
		http.Error(w, "Ошибка при обработке JSON", http.StatusBadRequest)
		return
	}

	// Директория с товарами
	storageDir := "./storage"

	// Список ссылок на изображения
	var imageLinks []string

	// Перебор всех переданных productID
	for _, productID := range request.ProductIDs {
		// Путь к папке с изображениями товара
		productDir := filepath.Join(storageDir, fmt.Sprintf("%d", productID))

		// Проверяем, существует ли папка
		if _, err := ioutil.ReadDir(productDir); err != nil {
			// Если папка не найдена, пропускаем товар
			continue
		}

		// Получаем все файлы в папке товара
		files, err := ioutil.ReadDir(productDir)
		if err != nil {
			http.Error(w, "Ошибка при чтении папки с изображениями", http.StatusInternalServerError)
			return
		}

		// Для каждого файла в папке генерируем ссылку
		for _, file := range files {
			// Сгенерировать ссылку на файл
			imageLink := fmt.Sprintf("%s/%d/%s", localHost, productID, file.Name())
			imageLinks = append(imageLinks, imageLink)
		}
	}

	// Если ссылки на изображения найдены, отправляем их
	if len(imageLinks) > 0 {
		// Отправляем список ссылок в формате JSON
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(imageLinks)
	} else {
		// Если изображения не найдены, возвращаем ошибку
		http.Error(w, "Изображения не найдены", http.StatusNotFound)
	}
}

func (s *Server) handleImageRequest(w http.ResponseWriter, r *http.Request) {
	// Получаем productID и имя файла из URL
	// Пример: /4285/0.jpg -> productID = 4285, fileName = 0.jpg
	pathParts := strings.Split(r.URL.Path, "/")
	if len(pathParts) < 3 {
		http.Error(w, "Неверный путь запроса", http.StatusBadRequest)
		return
	}

	productID := pathParts[1]
	fileName := pathParts[2]

	// Путь к папке с изображениями товара
	imageDir := "./storage/" + productID

	// Формируем полный путь к файлу изображения
	imagePath := filepath.Join(imageDir, fileName)

	// Проверяем, существует ли файл
	if _, err := os.Stat(imagePath); os.IsNotExist(err) {
		http.Error(w, "Изображение не найдено", http.StatusNotFound)
		return
	}

	// Устанавливаем тип контента в зависимости от расширения файла
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

	// Отдаем изображение клиенту
	http.ServeFile(w, r, imagePath)
}

func main() {
	// Ensure the storage directory exists
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

	// Register HTTP handlers
	http.HandleFunc("/api/media/update", server.handleMediaRequest)
	http.HandleFunc("/api/media", server.handleDirectLinks)
	http.HandleFunc("/", server.handleImageRequest)

	// Start the server
	port := 8080
	log.Printf("Server is running on port %d", port)
	if err := http.ListenAndServe(":"+strconv.Itoa(port), nil); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}
