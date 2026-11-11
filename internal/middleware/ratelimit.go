package middleware

import (
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mrfroglab/be-rongsok.in/internal/httpx"
)

// Rate limiter in-memory (fixed window). Cukup untuk deployment 1 instance
// (docker compose di home server). Kalau nanti di-scale >1 instance, ganti ke
// store bersama (Redis).

// ClientIP = IP klien asli. API hanya bisa dicapai lewat Cloudflare Tunnel +
// Caddy, jadi header dari proxy bisa dipercaya. Tanpa ini semua request
// terlihat dari IP proxy yang sama → satu ember untuk semua user.
func ClientIP(r *http.Request) string {
	if cf := strings.TrimSpace(r.Header.Get("CF-Connecting-IP")); cf != "" {
		return cf
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		first, _, _ := strings.Cut(xff, ",")
		if first = strings.TrimSpace(first); first != "" {
			return first
		}
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

type bucket struct {
	count   int
	resetAt time.Time
}

// Limiter menyimpan semua ember (namespace dipisah lewat prefix kunci).
type Limiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket
	now     func() time.Time
}

func NewLimiter() *Limiter {
	l := &Limiter{buckets: map[string]*bucket{}, now: time.Now}
	go l.sweep()
	return l
}

// sweep membuang ember kedaluwarsa tiap menit supaya map tidak tumbuh tanpa batas.
func (l *Limiter) sweep() {
	for range time.Tick(time.Minute) {
		now := l.now()
		l.mu.Lock()
		for k, b := range l.buckets {
			if !b.resetAt.After(now) {
				delete(l.buckets, k)
			}
		}
		l.mu.Unlock()
	}
}

// Hit mencatat satu percobaan. allowed=false bila melewati max dalam window.
func (l *Limiter) Hit(key string, max int, window time.Duration) (allowed bool, retryAfter time.Duration) {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	b := l.buckets[key]
	if b == nil || !b.resetAt.After(now) {
		b = &bucket{resetAt: now.Add(window)}
		l.buckets[key] = b
	}
	b.count++
	return b.count <= max, b.resetAt.Sub(now)
}

// Rule = satu aturan batas.
type Rule struct {
	Name    string
	Max     int
	Window  time.Duration
	Message string
}

const defaultLimitMessage = "Terlalu banyak percobaan. Coba lagi beberapa saat lagi."

// Allow menerapkan rule untuk key (kosong = lewati). false = respons 429 sudah ditulis.
func (l *Limiter) Allow(w http.ResponseWriter, rule Rule, key string) bool {
	if key == "" {
		return true
	}
	ok, retry := l.Hit(rule.Name+":"+key, rule.Max, rule.Window)
	if ok {
		return true
	}
	secs := int((retry + time.Second - 1) / time.Second)
	w.Header().Set("Retry-After", strconv.Itoa(secs))
	msg := rule.Message
	if msg == "" {
		msg = defaultLimitMessage
	}
	httpx.Error(w, http.StatusTooManyRequests, msg)
	return false
}

// ByIP: middleware rate limit per IP klien.
func (l *Limiter) ByIP(rule Rule) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if l.Allow(w, rule, ClientIP(r)) {
				next.ServeHTTP(w, r)
			}
		})
	}
}

// ByUser: middleware rate limit per user login (pasang setelah Protect).
func (l *Limiter) ByUser(rule Rule) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := ""
			if u := User(r); u != nil {
				key = u.ID
			}
			if l.Allow(w, rule, key) {
				next.ServeHTTP(w, r)
			}
		})
	}
}

// Aturan batas per endpoint (nilai sama dengan versi Express).
var (
	RuleLoginIP    = Rule{Name: "login-ip", Max: 30, Window: 15 * time.Minute}
	RuleLoginEmail = Rule{Name: "login-email", Max: 10, Window: 15 * time.Minute,
		Message: "Terlalu banyak percobaan login untuk akun ini. Coba lagi 15 menit lagi."}
	RuleRegister    = Rule{Name: "register", Max: 10, Window: time.Hour}
	RuleForgotIP    = Rule{Name: "forgot-ip", Max: 10, Window: time.Hour}
	RuleForgotEmail = Rule{Name: "forgot-email", Max: 3, Window: time.Hour}
	RuleReset       = Rule{Name: "reset", Max: 10, Window: 15 * time.Minute}
	// Hanya menghitung PATCH /auth/me yang membawa NIK (percobaan verifikasi).
	RuleKYC = Rule{Name: "kyc", Max: 10, Window: time.Hour,
		Message: "Terlalu banyak percobaan verifikasi. Coba lagi nanti."}
	RuleUpload = Rule{Name: "upload", Max: 60, Window: time.Hour,
		Message: "Batas unggah foto tercapai. Coba lagi nanti."}
)

// EmailKey menormalkan email untuk kunci rate limit.
func EmailKey(email string) string { return strings.ToLower(strings.TrimSpace(email)) }
