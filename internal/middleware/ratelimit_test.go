package middleware

import (
	"net/http/httptest"
	"testing"
	"time"
)

func TestLimiterFixedWindow(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	l := &Limiter{buckets: map[string]*bucket{}, now: func() time.Time { return now }}
	for i := 1; i <= 3; i++ {
		if ok, _ := l.Hit("k", 3, time.Minute); !ok {
			t.Fatalf("hit %d harus lolos", i)
		}
	}
	ok, retry := l.Hit("k", 3, time.Minute)
	if ok || retry != time.Minute {
		t.Fatalf("hit ke-4 harus ditolak dengan retry 60s, dapat %v %v", ok, retry)
	}
	if ok, _ := l.Hit("lain", 3, time.Minute); !ok {
		t.Error("kunci lain punya ember sendiri")
	}
	now = now.Add(time.Minute)
	if ok, _ := l.Hit("k", 3, time.Minute); !ok {
		t.Error("window baru harus reset")
	}
}

func TestAllowWrites429(t *testing.T) {
	l := &Limiter{buckets: map[string]*bucket{}, now: time.Now}
	rule := Rule{Name: "x", Max: 1, Window: time.Hour, Message: "pelan-pelan"}
	if !l.Allow(httptest.NewRecorder(), rule, "a") {
		t.Fatal("pertama harus lolos")
	}
	rec := httptest.NewRecorder()
	if l.Allow(rec, rule, "a") || rec.Code != 429 || rec.Header().Get("Retry-After") != "3600" {
		t.Errorf("kedua harus 429 + Retry-After: %d %q", rec.Code, rec.Header().Get("Retry-After"))
	}
	if !l.Allow(httptest.NewRecorder(), rule, "") {
		t.Error("kunci kosong = dilewati")
	}
}

func TestClientIP(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "172.18.0.5:1234"
	if ClientIP(r) != "172.18.0.5" {
		t.Error("fallback RemoteAddr")
	}
	r.Header.Set("X-Forwarded-For", "1.2.3.4, 10.0.0.1")
	if ClientIP(r) != "1.2.3.4" {
		t.Error("X-Forwarded-For pertama")
	}
	r.Header.Set("CF-Connecting-IP", "5.6.7.8")
	if ClientIP(r) != "5.6.7.8" {
		t.Error("CF-Connecting-IP diutamakan")
	}
}
