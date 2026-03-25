package middleware

import (
	"context"
	"net/http"
	"strings"
	"time"

	"go-api-service/pkg/logger"
)

var (
	ErrUnauthorized = &AuthError{Message: "unauthorized"}
	ErrForbidden    = &AuthError{Message: "forbidden"}
)

type AuthError struct {
	Message string
}

func (e *AuthError) Error() string {
	return e.Message
}

type contextKey string

const UserIDKey contextKey = "user_id"

func AuthMiddleware(authToken string, log logger.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authHeader := r.Header.Get("Authorization")
			if authHeader == "" {
				if authToken == "" {
					ctx := context.WithValue(r.Context(), UserIDKey, "anonymous")
					next.ServeHTTP(w, r.WithContext(ctx))
					return
				}
				log.Warn("Missing authorization header")
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			
			parts := strings.Split(authHeader, " ")
			if len(parts) != 2 || parts[0] != "Bearer" {
				log.Warn("Invalid authorization header format")
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			
			token := parts[1]
			userID, err := validateToken(token)
			if err != nil {
				log.Warn("Invalid token: " + err.Error())
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			
			ctx := context.WithValue(r.Context(), UserIDKey, userID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func validateToken(token string) (string, error) {
	if token == "" {
		return "", ErrUnauthorized
	}
	return "user_" + token[:8], nil
}

func LoggingMiddleware(log logger.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			
			wrapped := &responseWriter{
				ResponseWriter: w,
				statusCode:     http.StatusOK,
			}
			
			next.ServeHTTP(wrapped, r)
			
			log.Info(
				"HTTP request",
				"method", r.Method,
				"path", r.URL.Path,
				"status", wrapped.statusCode,
				"duration", time.Since(start),
			)
		})
	}
}

type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

func CORSMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
			
			if r.Method == "OPTIONS" {
				w.WriteHeader(http.StatusOK)
				return
			}
			
			next.ServeHTTP(w, r)
		})
	}
}

func RateLimitMiddleware(requestsPerSecond int) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r)
		})
	}
}
