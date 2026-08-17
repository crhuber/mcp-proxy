package server

import (
	"log/slog"
	"net/http"
	"time"
)

// LoggingMiddleware logs each HTTP request's method, path, status, and
// duration at Info level. It deliberately logs nothing about headers,
// arguments, or bodies — those may contain upstream/proxy secrets or
// sensitive tool-call data, and redaction of arbitrary request bytes isn't
// attempted here.
func LoggingMiddleware(next http.Handler, logger *slog.Logger) http.Handler {
	if logger == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusCapturingWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		logger.Info("http request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", sw.status,
			"duration", time.Since(start),
		)
	})
}

type statusCapturingWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusCapturingWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

// Flush delegates to the underlying ResponseWriter's http.Flusher, if any.
// The Streamable HTTP transport relies on flushing to stream SSE responses;
// without this, wrapping it in this middleware would silently break that.
func (w *statusCapturingWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
