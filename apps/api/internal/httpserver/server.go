package httpserver

import (
	"log/slog"
	"net/http"
	"time"
)

type Config struct {
	Addr string
}

func New(cfg Config, logger *slog.Logger, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              cfg.Addr,
		Handler:           withRequestLogging(logger, handler),
		ReadHeaderTimeout: 5 * time.Second,
	}
}

func withRequestLogging(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		logger.Info("http request",
			"method", r.Method,
			"path", r.URL.Path,
			"remote_addr", r.RemoteAddr,
			"duration_ms", time.Since(start).Milliseconds(),
		)
	})
}
