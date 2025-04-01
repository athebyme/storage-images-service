package main

import (
	mydb "awesomeProject1/database"
	"awesomeProject1/metrics"
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"sync"
	"syscall"
	"time"
)

const (
	remoteAPI  = "http://147.45.79.183:8081/api/media"
	localHost  = "http://media.athebyme-market.ru"
	storageDir = "./storage"
)

type Server struct {
	db          mydb.Driver
	contentRoot string
}

func NewServer(db mydb.Driver) *Server {
	return &Server{
		db:          db,
		contentRoot: storageDir,
	}
}

func main() {
	// Оптимизируем использование процессоров
	numCPU := runtime.NumCPU()
	runtime.GOMAXPROCS(numCPU)

	// Настраиваем логирование
	logFile, err := os.OpenFile("app.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Printf("Предупреждение: не удалось открыть файл логов: %v. Используем стандартный вывод.", err)
	} else {
		log.SetOutput(logFile)
		defer logFile.Close()
	}

	log.Printf("Запуск сервера с использованием %d CPU", numCPU)

	// Создаем директорию для хранения, если она не существует
	if err := os.MkdirAll(storageDir, os.ModePerm); err != nil {
		log.Fatalf("Ошибка при создании директории хранения: %v", err)
	}

	// Создаем директорию для анонимных пакетов
	if err := os.MkdirAll(storageDir+"/!additional", os.ModePerm); err != nil {
		log.Printf("Предупреждение: не удалось создать директорию для анонимных пакетов: %v", err)
	}

	// Инициализируем кэш
	initCache()
	log.Printf("Кэш изображений инициализирован")

	// Загружаем конфигурацию базы данных
	dbConfig := mydb.PostgresConfiguration{}
	cfg, err := dbConfig.LoadConfig("./config/config.yaml")
	if err != nil {
		log.Fatalf("Ошибка при загрузке конфигурации: %v", err)
	}

	// Подключаемся к базе данных
	db, err := mydb.NewPostgresDriver(cfg.GetConnectionString())
	if err != nil {
		log.Fatalf("Ошибка подключения к базе данных: %v", err)
	}
	defer db.Close()

	// Применяем миграции
	if err = db.MigrateUp(); err != nil {
		log.Printf("Предупреждение при применении миграций: %v. Продолжаем работу.", err)
	}

	// Создаем сервер
	server := NewServer(db)

	// Запускаем сервер метрик
	startMetrics()

	// Предварительное заполнение кэша для часто используемых изображений
	go preloadCommonImages()

	// Настраиваем HTTP сервер с таймаутами
	httpServer := &http.Server{
		Addr:         ":8081",
		Handler:      setupRoutes(server),
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	// Создаем канал для сигналов остановки
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	// Запускаем HTTP сервер в отдельной горутине
	go func() {
		log.Printf("Сервер запущен на порту 8081")
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Ошибка при запуске сервера: %v", err)
		}
	}()

	// Ожидаем сигнала остановки
	<-stop

	log.Println("Получен сигнал остановки. Завершение работы...")

	// Создаем контекст с таймаутом для graceful shutdown
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Останавливаем сервер
	if err := httpServer.Shutdown(ctx); err != nil {
		log.Fatalf("Ошибка при остановке сервера: %v", err)
	}

	log.Println("Сервер остановлен")
}

// Функция startMetrics запускает сервер метрик Prometheus
func startMetrics() {
	port := 2112
	metricsHandler := metrics.MetricsHandler()

	// Запускаем сервер метрик в отдельной горутине
	go func() {
		log.Printf("Запуск сервера метрик на порту: %d", port)
		metricsServer := &http.Server{
			Addr:    fmt.Sprintf(":%d", port),
			Handler: metricsHandler,
		}

		if err := metricsServer.ListenAndServe(); err != nil {
			log.Printf("Ошибка при запуске сервера метрик: %v", err)
		}
	}()
}

// preloadCommonImages предварительно загружает часто используемые изображения в кэш
func preloadCommonImages() {
	// Предзагрузка анонимных пакетов
	anonymousDir := storageDir + "/!additional"
	files, err := os.ReadDir(anonymousDir)
	if err != nil {
		log.Printf("Предупреждение: не удалось прочитать директорию анонимных пакетов: %v", err)
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
				log.Printf("Не удалось прочитать файл %s: %v", fullPath, err)
				return
			}

			// Сохраняем в кэше
			imageCache.imageData.Store(fullPath, data)

			// Создаем метаданные
			fileInfo, err := os.Stat(fullPath)
			if err == nil {
				contentType := getContentType(filename)
				etag := fmt.Sprintf(`"%x-%x"`, fileInfo.ModTime().Unix(), fileInfo.Size())

				metadata := FileMetadata{
					ETag:         etag,
					LastModified: fileInfo.ModTime(),
					ContentType:  contentType,
					Size:         fileInfo.Size(),
				}
				imageCache.metadata.Store(fullPath, metadata)
			}

			log.Printf("Файл %s предзагружен в кэш", filename)
		}(file.Name())
	}

	wg.Wait()
	log.Printf("Предзагрузка часто используемых изображений завершена")
}
