package main

import (
	"context"
	"crypto/rand"
	"io"
	"log/slog"
	"net/http"
	"time"
)

const xrequestIDKey = "X-Request-ID"

const logContextKey contextKey = "log_context"

type LogContext struct {
	Username string
	Error    error
}

type spyReadCloser struct {
	io.ReadCloser
	bytesRead int
}

func (r *spyReadCloser) Read(p []byte) (int, error) {
	n, err := r.ReadCloser.Read(p)
	r.bytesRead += n
	return n, err
}

type spyResponseWriter struct {
	http.ResponseWriter
	bytesWritten int
	statusCode   int
}

func (w *spyResponseWriter) Write(p []byte) (int, error) {
	if w.statusCode == 0 {
		w.statusCode = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(p)
	w.bytesWritten += n
	return n, err
}

func (w *spyResponseWriter) WriteHeader(statusCode int) {
	w.statusCode = statusCode
	w.ResponseWriter.WriteHeader(statusCode)
}

func requestLogger(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			logContext := &LogContext{}

			r = r.WithContext(context.WithValue(r.Context(), logContextKey, logContext))

			spyReader := &spyReadCloser{ReadCloser: r.Body}
			r.Body = spyReader

			spyWriter := &spyResponseWriter{
				ResponseWriter: w,
			}

			next.ServeHTTP(spyWriter, r)

			attrs := []any{
				"method", r.Method,
				"path", r.URL.Path,
				"client_ip", r.RemoteAddr,
				slog.Duration("duration", time.Since(start)),
				slog.Int("request_body_bytes", spyReader.bytesRead),
				slog.Int("response_status", spyWriter.statusCode),
				slog.Int("response_body_bytes", spyWriter.bytesWritten),
				slog.String("request_id", w.Header().Get(xrequestIDKey)),
			}

			if logContext.Username != "" {
				attrs = append(attrs, "user", logContext.Username)
			}

			if logContext.Error != nil {
				attrs = append(attrs, "error", logContext.Error)
			}

			logger.Info("Served request", attrs...)
		})
	}
}

func httpError(ctx context.Context, w http.ResponseWriter, status int, err error) {
	if logCtx, ok := ctx.Value(logContextKey).(*LogContext); ok {
		logCtx.Error = err
	}

	http.Error(w, err.Error(), status)
}

func requestIdMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqID := r.Header.Get(xrequestIDKey)
		if reqID == "" {
			reqID = rand.Text()
		}

		w.Header().Set(xrequestIDKey, reqID)

		next.ServeHTTP(w, r)
	})
}
