package database

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"gopkg.in/yaml.v3"
	"log"
	"os"
	"strings"
	"sync"

	_ "github.com/lib/pq"
)

type Article struct {
	ID     int      `json:"id"`
	Photos []string `json:"photos"`
}

type PostgresConfiguration struct {
	Host string `yaml:"host"`
	Port string `yaml:"port"`
	User string `yaml:"usr"`
	Pass string `yaml:"passwd"`
	Name string `yaml:"db"`
}

func (pc *PostgresConfiguration) GetConnectionString() string {
	return fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
		pc.Host, pc.Port, pc.User, pc.Pass, pc.Name)
}
func (pc *PostgresConfiguration) LoadConfig(filename string) (*PostgresConfiguration, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	decoder := yaml.NewDecoder(file)
	config := &PostgresConfiguration{}
	if err := decoder.Decode(config); err != nil {
		return nil, err
	}
	return config, nil
}

type DatabaseDriver interface {
	GetArticlesByIDs(ids []int) (map[int][]string, error)
	GetArticles() (map[int]struct{}, error)
}

type PostgresDriver struct {
	sync.Mutex
	db *sql.DB
}

func NewPostgresDriver(connectionString string) (*PostgresDriver, error) {
	db, err := sql.Open("postgres", connectionString)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}
	return &PostgresDriver{db: db}, nil
}

func (p *PostgresDriver) GetArticlesByIDs(ids []int) (map[int][]string, error) {
	placeholders := make([]string, len(ids))
	args := make([]interface{}, len(ids))
	for i, id := range ids {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
		args[i] = id
	}

	query := fmt.Sprintf(`SELECT articular, urls FROM storage.images WHERE articular IN (%s)`, strings.Join(placeholders, ", "))
	rows, err := p.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query articles: %w", err)
	}
	defer rows.Close()

	result := make(map[int][]string)
	for rows.Next() {
		var id int
		var photos string
		if err := rows.Scan(&id, &photos); err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}
		result[id] = parsePhotos(photos)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows error: %w", err)
	}

	return result, nil
}

func (p *PostgresDriver) GetArticles() (map[int]struct{}, error) {
	query := fmt.Sprintf(`SELECT articular FROM storage.images`)
	rows, err := p.db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("failed to query articles: %w", err)
	}
	defer rows.Close()

	result := make(map[int]struct{})
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}
		result[id] = struct{}{}
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows error: %w", err)
	}

	return result, nil
}

func (p *PostgresDriver) Close() error {
	return p.db.Close()
}

func (p *PostgresDriver) CreateRecord(record Article) error {
	// Преобразуем массив фоток в JSON
	photosJSON, err := json.Marshal(record.Photos)
	if err != nil {
		return fmt.Errorf("failed to serialize photos: %w", err)
	}

	// SQL-запрос для вставки записи
	query := `
		INSERT INTO storage.images (articular, urls)
		VALUES ($1, $2)
		ON CONFLICT (articular) DO NOTHING;
	`

	// Выполняем запрос
	p.Lock()
	_, err = p.db.Exec(query, record.ID, photosJSON)
	if err != nil {
		return fmt.Errorf("failed to insert or update record: %w", err)
	}
	p.Unlock()

	return nil
}

func (p *PostgresDriver) MigrateUp() error {
	driver, err := postgres.WithInstance(p.db, &postgres.Config{})
	if err != nil {
		log.Fatalf("failed to create migration driver: %v", err)
		return err
	}

	// Укажите путь к миграциям (относительно рабочей директории)
	m, err := migrate.NewWithDatabaseInstance(
		"file://migrations", // Путь к папке с миграциями
		"postgres",          // Название базы данных
		driver,
	)
	if err != nil {
		log.Fatalf("failed to create migrate instance: %v", err)
		return err
	}

	// Применяем миграции
	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		log.Fatalf("failed to apply migrations: %v", err)
		return err
	}

	fmt.Println("Migrations applied successfully")
	return nil
}

func parsePhotos(photos string) []string {
	// Assume photos are stored as a JSON array in the database.
	var parsedPhotos []string
	if err := json.Unmarshal([]byte(photos), &parsedPhotos); err != nil {
		log.Printf("failed to parse photos: %v", err)
		return []string{}
	}
	return parsedPhotos
}
