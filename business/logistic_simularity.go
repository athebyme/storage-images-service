package business

import (
	"encoding/json"
	"github.com/xrash/smetrics"
	"io"
	"net/http"
)

func similarity(a, b string) float64 {
	return smetrics.Jaro(a, b)
}

func SimilarityHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Only POST method is supported", http.StatusMethodNotAllowed)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 10*1024*1024) // 10 MB limit

	var payload struct {
		String1 string `json:"s1"`
		String2 string `json:"s2"`
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Error reading request body", http.StatusBadRequest)
		return
	}

	err = json.Unmarshal(body, &payload)
	if err != nil {
		http.Error(w, "Invalid JSON payload", http.StatusBadRequest)
		return
	}

	if payload.String1 == "" || payload.String2 == "" {
		http.Error(w, "Both s1 and s2 are required", http.StatusBadRequest)
		return
	}

	if len(payload.String1) > 5000 || len(payload.String2) > 5000 {
		http.Error(w, "Strings must not exceed 5000 characters", http.StatusBadRequest)
		return
	}

	sim := similarity(payload.String1, payload.String2)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]float64{
		"similarity": sim,
	})
}
