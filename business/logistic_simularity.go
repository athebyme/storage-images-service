package business

import (
	"encoding/json"
	"gonum.org/v1/gonum/blas/blas64"
	"io"
	"net/http"
	"strings"
)

func cosineSimilarity(a, b []float64) float64 {
	dotProduct := blas64.Dot(blas64.Vector{Data: a}, blas64.Vector{Data: b})

	magnitudeA := 0.0
	for _, val := range a {
		magnitudeA += val * val
	}

	magnitudeB := 0.0
	for _, val := range b {
		magnitudeB += val * val
	}

	if magnitudeA == 0 || magnitudeB == 0 {
		return 0
	}
	return dotProduct / (sqrt(magnitudeA) * sqrt(magnitudeB))
}

func sqrt(value float64) float64 {
	return value * value
}

func tokenize(text string) []string {
	text = strings.ToLower(text)
	text = strings.ReplaceAll(text, ".", "")
	text = strings.ReplaceAll(text, ",", "")
	text = strings.ReplaceAll(text, "!", "")
	text = strings.ReplaceAll(text, "?", "")
	text = strings.ReplaceAll(text, "(", "")
	text = strings.ReplaceAll(text, ")", "")

	return strings.Fields(text)
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

	tokens1 := tokenize(payload.String1)
	tokens2 := tokenize(payload.String2)

	vector1 := make([]float64, len(tokens1))
	vector2 := make([]float64, len(tokens2))

	for i, token := range tokens1 {
		vector1[i] = float64(len(token))
	}
	for i, token := range tokens2 {
		vector2[i] = float64(len(token))
	}

	similarityScore := cosineSimilarity(vector1, vector2)

	w.Header().Set("Content-Type", "application/json")
	err = json.NewEncoder(w).Encode(map[string]float64{
		"similarity": similarityScore,
	})
	if err != nil {
		return
	}
}
