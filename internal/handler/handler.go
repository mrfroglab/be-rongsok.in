// Package handler: handler HTTP per modul (satu file per modul route).
package handler

import (
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrfroglab/be-rongsok.in/internal/cloudinary"
	"github.com/mrfroglab/be-rongsok.in/internal/mail"
	"github.com/mrfroglab/be-rongsok.in/internal/middleware"
	"github.com/mrfroglab/be-rongsok.in/internal/realtime"
	"github.com/mrfroglab/be-rongsok.in/internal/security"
)

// Handler memegang dependensi bersama semua route.
type Handler struct {
	DB          *pgxpool.Pool
	Tokens      *security.Tokens
	KYC         *security.KYC
	Hub         *realtime.Hub
	Limiter     *middleware.Limiter
	Mail        *mail.Sender
	Cloudinary  *cloudinary.Client
	FrontendURL string
}

// str: nilai string dari pointer (kosong bila nil).
func str(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// nonEmpty: nil bila s kosong (setara `x || null` di JS).
func nonEmpty(p *string) *string {
	if p == nil || *p == "" {
		return nil
	}
	return p
}
