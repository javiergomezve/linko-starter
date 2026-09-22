package main

import (
	"context"
	"errors"
	"net/http"

	"golang.org/x/crypto/bcrypt"

	pkgerr "github.com/pkg/errors"
)

type contextKey string

const UserContextKey contextKey = "user"

var allowedUsers = map[string]string{
	"frodo":   "$2a$10$B6O/n6teuCzpuh66jrUAdeaJ3WvXcxRkzpN0x7H.di9G9e/NGb9Me",
	"samwise": "$2a$10$EWZpvYhUJtJcEMmm/IBOsOGIcpxUnGIVMRiDlN/nxl1RRwWGkJtty",
	// frodo: "ofTheNineFingers"
	// samwise: "theStrong"
	"saruman": "invalidFormat",
}

func (s *server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, span := tracer.Start(r.Context(), "middleware.auth")
		defer span.End()

		username, password, ok := r.BasicAuth()
		if !ok {
			httpError(ctx, w, http.StatusUnauthorized, errors.New("unauthorized"))
			return
		}
		stored, exists := allowedUsers[username]
		if !exists {
			httpError(ctx, w, http.StatusUnauthorized, errors.New("unauthorized"))
			return
		}
		ok, err := s.validatePassword(ctx, password, stored)
		if err != nil {
			s.logger.Error("error validating password",
				"user", username,
				"error", err,
			)
			httpError(ctx, w, http.StatusInternalServerError, err)
			return
		}
		if !ok {
			httpError(ctx, w, http.StatusUnauthorized, errors.New("unauthorized"))
			return
		}

		if logContext, ok := ctx.Value(logContextKey).(*LogContext); ok {
			logContext.Username = username
		}

		r = r.WithContext(context.WithValue(ctx, UserContextKey, username))

		next.ServeHTTP(w, r)
	})
}

func (s *server) validatePassword(ctx context.Context, password, stored string) (bool, error) {
	_, span := tracer.Start(ctx, "auth.validate_password")
	defer span.End()

	err := bcrypt.CompareHashAndPassword([]byte(stored), []byte(password))
	if err == bcrypt.ErrMismatchedHashAndPassword {
		return false, nil
	}

	if err != nil {
		return false, pkgerr.WithStack(err)
	}

	return true, nil
}
