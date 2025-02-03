package main

import (
	"fmt"
	"github.com/xrash/smetrics"
	"net/http"
)

func similarity(a, b string) float64 {
	return smetrics.Jaro(a, b)
}

func similarityHandler(w http.ResponseWriter, r *http.Request) {
	str1 := r.URL.Query().Get("s1")
	str2 := r.URL.Query().Get("s2")

	if str1 == "" || str2 == "" {
		http.Error(w, "Both str1 and str2 are required", http.StatusBadRequest)
		return
	}

	sim := similarity(str1, str2)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, err := fmt.Fprintf(w, `{"similarity": %.4f}`, sim)
	if err != nil {
		return
	}
}
