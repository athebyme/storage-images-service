package middleware

import (
	"awesomeProject1/metrics"
	"net/http"
	"time"
)

// responseWriter оборачивает http.ResponseWriter для сохранения кода ответа.
type responseWriter struct {
	http.ResponseWriter
	status int
}

// WriteHeader перехватывает вызов WriteHeader, сохраняя код ответа.
func (rw *responseWriter) WriteHeader(code int) {
	rw.status = code
	rw.ResponseWriter.WriteHeader(code)
}

// PrometheusMiddleware оборачивает HTTP-обработчик для сбора метрик.
func PrometheusMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		rw := &responseWriter{ResponseWriter: w, status: http.StatusOK} // По умолчанию статус 200

		next.ServeHTTP(rw, r)

		duration := time.Since(start)

		metrics.RecordRequest(r.Method, r.URL.Path, rw.status, duration)
	})
}
