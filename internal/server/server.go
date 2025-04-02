package server

import (
	"awesomeProject1/pkg/database"
)

// Server holds the server implementation
type Server struct {
	DB          database.Driver
	ContentRoot string
}

// Constants used throughout the server
const (
	RemoteAPI         = "http://147.45.79.183:8081/api/media"
	LocalHost         = "http://media.athebyme-market.ru"
	StorageDir        = "./storage"
	APIUpdatePassword = "123test"
)

// NewServer creates a new server instance
func NewServer(db database.Driver) *Server {
	return &Server{
		DB:          db,
		ContentRoot: StorageDir,
	}
}

// PreloadCommonImages preloads frequently used images into cache
func (s *Server) PreloadCommonImages() {
	preloadCommonImages()
}
