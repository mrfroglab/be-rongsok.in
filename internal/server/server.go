// Package server merangkai router HTTP.
package server

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/mrfroglab/be-rongsok.in/docs"
	"github.com/mrfroglab/be-rongsok.in/internal/handler"
	"github.com/mrfroglab/be-rongsok.in/internal/httpx"
	mw "github.com/mrfroglab/be-rongsok.in/internal/middleware"
	"github.com/mrfroglab/be-rongsok.in/internal/realtime"
	"github.com/mrfroglab/be-rongsok.in/internal/store"
)

type Deps struct {
	Handler     *handler.Handler
	Auth        *mw.Authenticator
	Realtime    *realtime.Server
	CORSOrigins []string
}

// New mengembalikan handler HTTP lengkap.
func New(d Deps) http.Handler {
	h, lim := d.Handler, d.Handler.Limiter
	protect := d.Auth.Protect

	r := chi.NewRouter()
	r.Use(mw.Logger, mw.SecurityHeaders, mw.CORS(d.CORSOrigins))

	r.Get("/health", func(w http.ResponseWriter, _ *http.Request) {
		httpx.JSON(w, http.StatusOK, struct {
			Status  string `json:"status"`
			Message string `json:"message"`
		}{"ok", "Rongsok.in API is running"})
	})
	r.Get("/api-docs", swaggerUI)
	r.Get("/api-docs/", swaggerUI)
	r.Get("/api-docs/swagger.yaml", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
		w.Write(docs.Swagger) //nolint:errcheck
	})

	r.Route("/api/v1", func(r chi.Router) {
		// WebSocket realtime (pengganti Socket.IO). Auth lewat pesan pertama.
		r.Get("/ws", d.Realtime.ServeHTTP)

		r.Route("/auth", func(r chi.Router) {
			r.With(lim.ByIP(mw.RuleRegister)).Post("/register", h.Register)
			r.With(lim.ByIP(mw.RuleLoginIP)).Post("/login", h.Login)
			r.With(lim.ByIP(mw.RuleForgotIP)).Post("/forgot-password", h.ForgotPassword)
			r.With(lim.ByIP(mw.RuleReset)).Post("/reset-password", h.ResetPassword)
			r.With(protect).Get("/me", h.Me)
			r.With(protect).Patch("/me", h.UpdateMe)
		})

		r.Route("/collector", func(r chi.Router) {
			r.Use(protect)
			r.Get("/profile", h.GetCollectorProfile)
			r.Post("/profile", h.UpsertCollectorProfile)
			r.Patch("/profile", h.UpsertCollectorProfile)
			r.Patch("/catalogs", h.UpdateCatalogs)
		})

		// Publik — landing page & pencarian bisa diakses tanpa login.
		r.Route("/discovery", func(r chi.Router) {
			r.Get("/search", h.Search)
			r.Get("/categories", h.Categories)
			r.Get("/stats", h.PublicStats)
			r.Get("/leaderboard", h.Leaderboard)
			r.Get("/collectors/{id}", h.CollectorByID)
		})

		r.Route("/orders", func(r chi.Router) {
			r.Use(protect)
			r.Post("/", h.CreateOrder)
			r.Get("/", h.ListOrders)
			r.Get("/{id}", h.GetOrder)
			r.Patch("/{id}", h.UpdateOrderStatus)
		})

		r.Route("/ratings", func(r chi.Router) {
			r.Use(protect)
			r.Post("/", h.SubmitRating)
			r.Get("/user/{userId}", h.UserRatings)
		})

		r.Route("/admin", func(r chi.Router) {
			r.Use(protect, mw.Authorize(store.RoleAdmin))
			r.Get("/stats", h.AdminStats)
			r.Get("/orders", h.AdminOrders)
			r.Post("/categories", h.CreateCategory)
			r.Patch("/categories/{id}", h.UpdateCategory)
			r.Delete("/categories/{id}", h.DeleteCategory)
		})

		// Wajib login: foto KTP tak lagi diunggah, jadi tak ada kebutuhan upload anonim.
		r.With(protect, lim.ByUser(mw.RuleUpload)).Post("/upload", h.UploadImage)
	})

	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		httpx.Error(w, http.StatusNotFound, "Route "+r.Method+" "+r.URL.Path+" tidak ditemukan")
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		httpx.Error(w, http.StatusNotFound, "Route "+r.Method+" "+r.URL.Path+" tidak ditemukan")
	})
	return r
}

const swaggerHTML = `<!doctype html>
<html lang="id">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Rongsok.in API Docs</title>
<link rel="stylesheet" href="https://cdn.jsdelivr.net/npm/swagger-ui-dist@5.17.14/swagger-ui.css">
</head>
<body>
<div id="swagger-ui"></div>
<script src="https://cdn.jsdelivr.net/npm/swagger-ui-dist@5.17.14/swagger-ui-bundle.js"></script>
<script>window.ui = SwaggerUIBundle({ url: '/api-docs/swagger.yaml', dom_id: '#swagger-ui' });</script>
</body>
</html>`

func swaggerUI(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(swaggerHTML)) //nolint:errcheck
}
