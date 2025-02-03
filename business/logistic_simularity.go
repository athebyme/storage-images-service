package business

import (
	"encoding/json"
	"github.com/texttheater/golang-levenshtein/levenshtein"
	"github.com/xrash/smetrics"
	"io"
	"net/http"
	"strings"
)

func similarity(a, b string) float64 {
	a = strings.TrimSpace(strings.ToLower(a))
	b = strings.TrimSpace(strings.ToLower(b))

	jaroWinkler := smetrics.JaroWinkler(a, b, 0.7, 4)

	levDist := levenshtein.DistanceForStrings([]rune(a), []rune(b), levenshtein.DefaultOptions)
	maxLen := max(len(a), len(b))
	if maxLen == 0 {
		maxLen = 1
	}
	levSimilarity := 1 - float64(levDist)/float64(maxLen)

	ngramSimilarity := ngramSimilarity(a, b, 2)

	combinedSimilarity := 0.4*jaroWinkler + 0.3*levSimilarity + 0.3*ngramSimilarity

	return combinedSimilarity
}

func ngramSimilarity(a, b string, n int) float64 {
	aGrams := generateNGrams(a, n)
	bGrams := generateNGrams(b, n)

	intersection := 0
	for gram := range aGrams {
		if bGrams[gram] {
			intersection++
		}
	}

	union := len(aGrams) + len(bGrams) - intersection
	if union == 0 {
		return 0.0
	}

	return float64(intersection) / float64(union)
}

func generateNGrams(s string, n int) map[string]bool {
	grams := make(map[string]bool)
	for i := 0; i <= len(s)-n; i++ {
		grams[s[i:i+n]] = true
	}
	return grams
}

type RequestBody struct {
	S1 string `json:"s1"`
	S2 string `json:"s2"`
}

func SimilarityHandler(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	var reqBody RequestBody
	err = json.Unmarshal(body, &reqBody)
	if err != nil {
		http.Error(w, "Invalid JSON format", http.StatusBadRequest)
		return
	}

	if reqBody.S1 == "" || reqBody.S2 == "" {
		http.Error(w, "Both s1 and s2 are required", http.StatusBadRequest)
		return
	}

	sim := similarity(reqBody.S1, reqBody.S2)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	err = json.NewEncoder(w).Encode(map[string]float64{"similarity": sim})
	if err != nil {
		return
	}
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
