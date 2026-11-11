// Package app merangkai semua dependensi jadi satu aplikasi (dipakai main & tes).
package app

import (
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrfroglab/be-rongsok.in/internal/cloudinary"
	"github.com/mrfroglab/be-rongsok.in/internal/config"
	"github.com/mrfroglab/be-rongsok.in/internal/handler"
	"github.com/mrfroglab/be-rongsok.in/internal/jobs"
	"github.com/mrfroglab/be-rongsok.in/internal/mail"
	"github.com/mrfroglab/be-rongsok.in/internal/middleware"
	"github.com/mrfroglab/be-rongsok.in/internal/realtime"
	"github.com/mrfroglab/be-rongsok.in/internal/security"
	"github.com/mrfroglab/be-rongsok.in/internal/server"
)

type App struct {
	Handler http.Handler
	Hub     *realtime.Hub
	Sweeper *jobs.Sweeper
}

func New(cfg *config.Config, pool *pgxpool.Pool) *App {
	kyc := security.NewKYC(cfg.KYCSecret)
	if !kyc.Enforced() {
		slog.Warn("KYC_ATTESTATION_SECRET belum diset (min 32 karakter) — NIK dari client dipercaya mentah (mode legacy)")
	}
	tokens := security.NewTokens(cfg.JWTSecret)
	auth := &middleware.Authenticator{Tokens: tokens, DB: pool}
	hub := realtime.NewHub()
	h := &handler.Handler{
		DB: pool, Tokens: tokens, KYC: kyc, Hub: hub, Limiter: middleware.NewLimiter(),
		Mail: &mail.Sender{Host: cfg.SMTPHost, Port: cfg.SMTPPort, User: cfg.SMTPUser, Pass: cfg.SMTPPass},
		Cloudinary: &cloudinary.Client{CloudName: cfg.CloudinaryCloudName, APIKey: cfg.CloudinaryAPIKey,
			APISecret: cfg.CloudinaryAPISecret},
		FrontendURL: cfg.FrontendURL,
	}
	return &App{
		Handler: server.New(server.Deps{
			Handler: h, Auth: auth, CORSOrigins: cfg.CORSOrigins,
			Realtime: &realtime.Server{Hub: hub, Auth: auth, DB: pool, Origins: cfg.CORSOrigins},
		}),
		Hub:     hub,
		Sweeper: &jobs.Sweeper{DB: pool, Hub: hub},
	}
}
