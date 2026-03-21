// Package middleware provides HTTP middleware for the RentLoop admin panel.
package middleware

import (
	"context"
	"net/http"

	"github.com/codercollo/rentloop/internal/auth"
)

type contextKey string

const adminKey contextKey = "admin_claims"

// RequireAdmin validates the JWT cookie and redirects to /admin/login on failure.
// Injects *auth.Claims into the request context on success.
func RequireAdmin(svc *auth.Service) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cookie, err := r.Cookie("rentloop_admin")
			if err != nil {
				http.Redirect(w, r, "/admin/login", http.StatusFound)
				return
			}

			claims, err := svc.VerifyJWT(cookie.Value)
			if err != nil {
				// Clear the invalid cookie
				http.SetCookie(w, &http.Cookie{
					Name:   "rentloop_admin",
					Value:  "",
					MaxAge: -1,
					Path:   "/",
				})
				http.Redirect(w, r, "/admin/login", http.StatusFound)
				return
			}

			ctx := context.WithValue(r.Context(), adminKey, claims)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// AdminClaims extracts *auth.Claims from the request context.
// Returns nil if not present — only possible if RequireAdmin was skipped.
func AdminClaims(ctx context.Context) *auth.Claims {
	c, _ := ctx.Value(adminKey).(*auth.Claims)
	return c
}
