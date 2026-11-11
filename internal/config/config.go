// Package config membaca konfigurasi dari environment (dan file .env saat dev).
package config

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"
)

// Panjang minimal secret. JWT_SECRET wajib (server menolak start); secret KYC
// yang lebih pendek dianggap tidak diset (mode legacy).
const MinSecretLength = 32

type Config struct {
	Port        string
	DatabaseURL string
	JWTSecret   string
	// KYCSecret kosong = mode legacy (NIK dari client dipercaya mentah).
	KYCSecret   string
	CORSOrigins []string
	FrontendURL string

	CloudinaryCloudName string
	CloudinaryAPIKey    string
	CloudinaryAPISecret string

	SMTPHost string
	SMTPPort string
	SMTPUser string
	SMTPPass string

	SeedAdminPassword string
}

// Load membaca .env (kalau ada, tanpa menimpa env yang sudah diset) lalu env.
func Load() (*Config, error) {
	loadDotEnv(".env")

	c := &Config{
		Port:                env("PORT", "3000"),
		DatabaseURL:         os.Getenv("DATABASE_URL"),
		JWTSecret:           os.Getenv("JWT_SECRET"),
		FrontendURL:         env("FRONTEND_URL", "http://localhost:3000"),
		CloudinaryCloudName: os.Getenv("CLOUDINARY_CLOUD_NAME"),
		CloudinaryAPIKey:    os.Getenv("CLOUDINARY_API_KEY"),
		CloudinaryAPISecret: os.Getenv("CLOUDINARY_API_SECRET"),
		SMTPHost:            env("SMTP_HOST", "smtp.gmail.com"),
		SMTPPort:            env("SMTP_PORT", "465"),
		SMTPUser:            os.Getenv("SMTP_USER"),
		SMTPPass:            os.Getenv("SMTP_PASS"),
		SeedAdminPassword:   os.Getenv("SEED_ADMIN_PASSWORD"),
	}
	if s := os.Getenv("KYC_ATTESTATION_SECRET"); len(s) >= MinSecretLength {
		c.KYCSecret = s
	}
	for _, o := range strings.Split(os.Getenv("CORS_ORIGINS"), ",") {
		if o = strings.TrimSpace(o); o != "" {
			c.CORSOrigins = append(c.CORSOrigins, o)
		}
	}

	if c.DatabaseURL == "" {
		return nil, errors.New("DATABASE_URL belum diset")
	}
	// Dulu ada fallback 'secret' — kalau env lupa diset, siapa pun bisa
	// memalsukan token (termasuk role ADMIN). Sekarang server menolak start.
	if len(c.JWTSecret) < MinSecretLength {
		return nil, fmt.Errorf("JWT_SECRET wajib diset & minimal %d karakter. Generate: openssl rand -hex 48", MinSecretLength)
	}
	return c, nil
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// loadDotEnv: parser .env minimal (KEY=VALUE, komentar #, kutip opsional).
func loadDotEnv(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if len(v) >= 2 && (v[0] == '"' && v[len(v)-1] == '"' || v[0] == '\'' && v[len(v)-1] == '\'') {
			v = v[1 : len(v)-1]
		}
		if _, set := os.LookupEnv(k); !set {
			os.Setenv(k, v)
		}
	}
}
