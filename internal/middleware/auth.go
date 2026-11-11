// Package middleware: autentikasi, rate limit, CORS, logging.
package middleware

import (
	"context"
	"net/http"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrfroglab/be-rongsok.in/internal/httpx"
	"github.com/mrfroglab/be-rongsok.in/internal/security"
	"github.com/mrfroglab/be-rongsok.in/internal/store"
)

type ctxKey int

const userKey ctxKey = iota

// User mengembalikan user terautentikasi (dipasang Protect).
func User(r *http.Request) *store.AuthUser {
	u, _ := r.Context().Value(userKey).(*store.AuthUser)
	return u
}

// Authenticator memverifikasi token lalu memastikan user masih ada di DB.
// Role diambil dari DB (bukan dari JWT) supaya perubahan role langsung berlaku.
type Authenticator struct {
	Tokens *security.Tokens
	DB     *pgxpool.Pool
}

// UserFromToken: nil bila token tidak valid / user sudah tidak ada.
func (a *Authenticator) UserFromToken(ctx context.Context, token string) (*store.AuthUser, error) {
	id, err := a.Tokens.Verify(token)
	if err != nil {
		return nil, nil //nolint:nilerr // token tidak valid = anonim
	}
	return store.GetAuthUser(ctx, a.DB, id)
}

// Protect: wajib header "Authorization: Bearer <token>".
func (a *Authenticator) Protect(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := r.Header.Get("Authorization")
		if !strings.HasPrefix(h, "Bearer ") {
			httpx.Error(w, http.StatusUnauthorized, "Unauthorized, no token")
			return
		}
		id, err := a.Tokens.Verify(strings.TrimPrefix(h, "Bearer "))
		if err != nil {
			httpx.Error(w, http.StatusUnauthorized, "Unauthorized, invalid token")
			return
		}
		user, err := store.GetAuthUser(r.Context(), a.DB, id)
		if err != nil {
			httpx.Error(w, http.StatusUnauthorized, "Unauthorized, invalid token")
			return
		}
		if user == nil {
			httpx.Error(w, http.StatusUnauthorized, "Unauthorized, user no longer exists")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userKey, user)))
	})
}

// Authorize: role guard (pasang setelah Protect).
func Authorize(roles ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			u := User(r)
			if u == nil || !slices.Contains(roles, u.Role) {
				httpx.Error(w, http.StatusForbidden, "Forbidden — butuh role: "+strings.Join(roles, ", "))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
