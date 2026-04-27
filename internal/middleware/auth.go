package middleware

import (
	"api-bridge/internal/account"
	"context"
	"log/slog"
	"net/http"
	"strings"
)

type contextKey string

const AccountKey contextKey = "account"

func AuthMiddleware(adminKey string, accountManager *account.Manager) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := extractToken(r)

			if token == "" {
				slog.Warn("auth failed: no token in request",
					"method", r.Method,
					"path", r.URL.Path,
					"remote_addr", clientIP(r),
					"has_authorization", r.Header.Get("Authorization") != "",
					"has_x_api_key", r.Header.Get("X-API-Key") != "",
				)
				writeError(w, http.StatusUnauthorized, "missing authorization header")
				return
			}

			if strings.HasPrefix(r.URL.Path, "/admin/") {
				if token != adminKey {
					slog.Warn("auth failed: invalid admin key",
						"method", r.Method,
						"path", r.URL.Path,
						"remote_addr", clientIP(r),
					)
					writeError(w, http.StatusUnauthorized, "invalid admin key")
					return
				}
				next.ServeHTTP(w, r)
				return
			}

			acc, err := accountManager.GetByKey(token)
			if err != nil || acc == nil {
				slog.Warn("auth failed: invalid account key",
					"method", r.Method,
					"path", r.URL.Path,
					"remote_addr", clientIP(r),
				)
				writeError(w, http.StatusUnauthorized, "invalid account key")
				return
			}

			ctx := context.WithValue(r.Context(), AccountKey, acc)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func extractToken(r *http.Request) string {
	if authHeader := r.Header.Get("Authorization"); authHeader != "" {
		return strings.TrimPrefix(authHeader, "Bearer ")
	}

	if apiKey := r.Header.Get("X-API-Key"); apiKey != "" {
		return apiKey
	}

	return ""
}

func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		return strings.TrimSpace(parts[0])
	}
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return xri
	}
	return r.RemoteAddr
}

func writeError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	w.Write([]byte(`{"error":"` + msg + `"}`))
}
