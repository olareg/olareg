// Package httplog provides a log/slog logging middleware for http servers.
package httplog

import (
	"context"
	"log/slog"
	"net/http"
	"time"
)

// New returns a logging http handler based on slog.
func New(next http.Handler, log *slog.Logger, level slog.Level) http.Handler {
	if !log.Enabled(context.Background(), level) {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rw := &respWrap{
			ResponseWriter: w,
			status:         http.StatusOK,
		}
		start := time.Now()
		next.ServeHTTP(rw, r)
		duration := time.Since(start)
		reqHead := r.Header.Clone()
		if reqHead.Get("Authorization") != "" {
			reqHead.Set("Authorization", "[censored]")
		}
		log.Log(r.Context(), level, "ServeHTTP", "duration", duration, "method", r.Method, "url", r.URL.String(), "status", rw.status, "req-headers", reqHead, "resp-headers", rw.ResponseWriter.Header())
	})
}

type respWrap struct {
	http.ResponseWriter
	status int
}

func (rw *respWrap) WriteHeader(status int) {
	rw.status = status
	rw.ResponseWriter.WriteHeader(status)
}
