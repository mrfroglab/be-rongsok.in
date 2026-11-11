// Package tests: tes integrasi & keamanan end-to-end (HTTP + WebSocket + PostGIS).
//
// Butuh DB PostGIS khusus tes (data dibuat dengan email unik, jadi DB boleh dipakai ulang):
//
//	TEST_DATABASE_URL=postgres://u:p@127.0.0.1:5432/d go test ./tests/ -v
//
// Tanpa TEST_DATABASE_URL semua tes di paket ini dilewati.
package tests

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrfroglab/be-rongsok.in/internal/app"
	"github.com/mrfroglab/be-rongsok.in/internal/config"
	"github.com/mrfroglab/be-rongsok.in/internal/db"
	"github.com/mrfroglab/be-rongsok.in/internal/security"
	"github.com/mrfroglab/be-rongsok.in/migrations"
)

const (
	jwtSecret = "test-jwt-secret-test-jwt-secret-0123456789"
	kycSecret = "test-kyc-secret-test-kyc-secret-0123456789"
)

var (
	pool    *pgxpool.Pool
	srv     *httptest.Server
	testApp *app.App
	dbURL   string
)

func TestMain(m *testing.M) {
	dbURL = os.Getenv("TEST_DATABASE_URL")
	// Log request membanjiri output tes; tampilkan hanya error.
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})))
	if dbURL == "" {
		fmt.Println("TEST_DATABASE_URL tidak diset — tes integrasi dilewati")
		os.Exit(0)
	}
	ctx := context.Background()
	var err error
	if pool, err = db.Connect(ctx, dbURL); err != nil {
		fmt.Println("db:", err)
		os.Exit(1)
	}
	if err := db.Migrate(ctx, pool, migrations.FS); err != nil {
		fmt.Println("migrate:", err)
		os.Exit(1)
	}
	cfg := &config.Config{DatabaseURL: dbURL, JWTSecret: jwtSecret, KYCSecret: kycSecret, FrontendURL: "http://localhost:3000"}
	testApp = app.New(cfg, pool)
	srv = httptest.NewServer(testApp.Handler)
	code := m.Run()
	srv.Close()
	pool.Close()
	os.Exit(code)
}

// ── HTTP helper ───────────────────────────────────────────────────────────

type resp struct {
	Status int
	Body   map[string]any
	Raw    []byte
}

// data mengembalikan body.data sebagai map.
func (r resp) data() map[string]any { m, _ := r.Body["data"].(map[string]any); return m }

func (r resp) list() []any { l, _ := r.Body["data"].([]any); return l }

var ipCounter atomic.Int64

// freshIP: IP unik per request supaya tes tidak saling kena rate limit per-IP.
func freshIP() string {
	n := ipCounter.Add(1)
	return fmt.Sprintf("10.%d.%d.%d", (n>>16)&255, (n>>8)&255, n&255)
}

func call(t *testing.T, method, path, token string, body any, headers ...string) resp {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, srv.URL+"/api/v1"+path, rd)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("CF-Connecting-IP", freshIP())
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	return do(t, req)
}

func do(t *testing.T, req *http.Request) resp {
	t.Helper()
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", req.Method, req.URL.Path, err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	r := resp{Status: res.StatusCode, Raw: raw}
	_ = json.Unmarshal(raw, &r.Body)
	return r
}

func expectStatus(t *testing.T, r resp, want int, msg string) {
	t.Helper()
	if r.Status != want {
		t.Errorf("%s: status %d, want %d — %s", msg, r.Status, want, r.Raw)
	}
}

var seq atomic.Int64

func uniqueEmail(prefix string) string {
	return fmt.Sprintf("%s%d_%d@t.io", prefix, time.Now().UnixNano(), seq.Add(1))
}

type user struct {
	ID, Token, Email string
}

func register(t *testing.T, role string, extra map[string]any) (user, resp) {
	t.Helper()
	body := map[string]any{"name": "Budi", "email": uniqueEmail("u"), "password": "password123", "role": role}
	for k, v := range extra {
		body[k] = v
	}
	r := call(t, "POST", "/auth/register", "", body)
	if r.Status != http.StatusCreated {
		return user{}, r
	}
	d := r.data()
	u := d["user"].(map[string]any)
	return user{ID: u["id"].(string), Token: d["access_token"].(string), Email: body["email"].(string)}, r
}

func mustRegister(t *testing.T, role string) user {
	t.Helper()
	u, r := register(t, role, nil)
	if u.ID == "" {
		t.Fatalf("register gagal: %d %s", r.Status, r.Raw)
	}
	return u
}

// collector membuat pengepul berlokasi + profil lapak (opsional terverifikasi).
func collector(t *testing.T, lat, lng float64, verified bool) user {
	t.Helper()
	c := mustRegister(t, "COLLECTOR")
	expectStatus(t, call(t, "PATCH", "/auth/me", c.Token, map[string]any{"lat": lat, "lng": lng}), 200, "set lokasi pengepul")
	expectStatus(t, call(t, "POST", "/collector/profile", c.Token, map[string]any{"shopName": "Lapak " + c.ID[:4]}), 200, "buat profil")
	if verified {
		exec(t, `UPDATE "User" SET "isVerified" = true WHERE id = $1`, c.ID)
	}
	return c
}

func customer(t *testing.T, lat, lng float64) user {
	t.Helper()
	c := mustRegister(t, "CUSTOMER")
	expectStatus(t, call(t, "PATCH", "/auth/me", c.Token, map[string]any{"lat": lat, "lng": lng}), 200, "set lokasi customer")
	return c
}

func newCategory(t *testing.T) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(), `INSERT INTO "WasteCategory" (id, name) VALUES (gen_random_uuid()::text, $1) RETURNING id`,
		fmt.Sprintf("Kat%d_%d", time.Now().UnixNano(), seq.Add(1))).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func exec(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatal(err)
	}
}

func queryVal[T any](t *testing.T, sql string, args ...any) T {
	t.Helper()
	var v T
	if err := pool.QueryRow(context.Background(), sql, args...).Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

// signKYC meniru rongsokin-web/lib/kycAttestation.ts.
func signKYC(nik, nama string) string {
	payload, _ := json.Marshal(map[string]any{"nik": nik, "nama": nama, "exp": time.Now().Unix() + 900})
	body := base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, []byte(kycSecret))
	mac.Write([]byte(body))
	return body + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// mkNIK: NIK acak yang lolos validasi struktur (DIY; tanggal, bulan, tahun &
// nomor urut acak supaya tak bentrok dengan NIK dari run tes sebelumnya).
func mkNIK() string {
	day := 1 + rand.IntN(28)
	if rand.IntN(2) == 0 {
		day += 40 // perempuan
	}
	return fmt.Sprintf("340401%02d%02d%02d%04d", day, 1+rand.IntN(12), rand.IntN(100), 1+rand.IntN(9998))
}

// ── WebSocket helper ──────────────────────────────────────────────────────

type wsEvent struct {
	Event string          `json:"event"`
	Data  json.RawMessage `json:"data"`
}

type wsClient struct {
	conn   *websocket.Conn
	events chan wsEvent
}

// dial menghubungkan & autentikasi. ok=false bila server menolak.
func dial(t *testing.T, token string) (*wsClient, bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+"/api/v1/ws", nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	c := &wsClient{conn: conn, events: make(chan wsEvent, 64)}
	c.send(t, "auth", map[string]string{"token": token})
	_, msg, err := conn.Read(ctx)
	if err != nil {
		return c, false
	}
	var first wsEvent
	_ = json.Unmarshal(msg, &first)
	if first.Event != "ready" {
		conn.CloseNow()
		return c, false
	}
	go func() {
		for {
			_, msg, err := conn.Read(context.Background())
			if err != nil {
				close(c.events)
				return
			}
			var e wsEvent
			if json.Unmarshal(msg, &e) == nil {
				c.events <- e
			}
		}
	}()
	t.Cleanup(func() { conn.CloseNow() })
	return c, true
}

func (c *wsClient) send(t *testing.T, event string, data any) {
	t.Helper()
	b, _ := json.Marshal(map[string]any{"event": event, "data": data})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := c.conn.Write(ctx, websocket.MessageText, b); err != nil {
		t.Fatalf("ws write: %v", err)
	}
}

// waitFor menunggu event tertentu (event lain dibuang). nil bila timeout.
func (c *wsClient) waitFor(event string, timeout time.Duration) *wsEvent {
	deadline := time.After(timeout)
	for {
		select {
		case e, ok := <-c.events:
			if !ok {
				return nil
			}
			if e.Event == event {
				return &e
			}
		case <-deadline:
			return nil
		}
	}
}

// drain mengembalikan semua event yang datang dalam d.
func (c *wsClient) drain(d time.Duration) []wsEvent {
	var out []wsEvent
	deadline := time.After(d)
	for {
		select {
		case e, ok := <-c.events:
			if !ok {
				return out
			}
			out = append(out, e)
		case <-deadline:
			return out
		}
	}
}

// waitRoom menunggu sampai room punya n anggota (join_room diproses async).
func waitRoom(t *testing.T, room string, n int) {
	t.Helper()
	for i := 0; i < 100; i++ {
		if testApp.Hub.RoomSize(room) >= n {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("room %s tidak mencapai %d anggota", room, n)
}

func testTokens() *security.Tokens { return security.NewTokens(jwtSecret) }

func mustReq(t *testing.T, method, url string) *http.Request {
	t.Helper()
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	return req
}
