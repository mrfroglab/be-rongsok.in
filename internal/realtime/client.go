package realtime

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrfroglab/be-rongsok.in/internal/store"
)

const (
	authTimeout  = 10 * time.Second
	pingInterval = 25 * time.Second // < idle timeout Cloudflare (100 dtk)
	pingTimeout  = 15 * time.Second
	readLimit    = 64 << 10
	sendBuffer   = 64

	// CloseUnauthorized = kode close saat auth gagal (FE tidak reconnect).
	CloseUnauthorized websocket.StatusCode = 4401
)

// TokenResolver mengubah JWT jadi user (nil = tidak valid / user tidak ada).
type TokenResolver interface {
	UserFromToken(ctx context.Context, token string) (*store.AuthUser, error)
}

// Server = handler HTTP endpoint WebSocket.
type Server struct {
	Hub  *Hub
	Auth TokenResolver
	DB   *pgxpool.Pool
	// Origins = CORS_ORIGINS. Kosong = izinkan semua origin.
	Origins []string
}

type Client struct {
	hub    *Hub
	conn   *websocket.Conn
	user   *store.AuthUser
	send   chan []byte
	rooms  map[string]struct{} // dijaga hub.mu
	once   sync.Once
	cancel context.CancelFunc
}

// enqueue tanpa blok; client yang terlalu lambat diputus.
func (c *Client) enqueue(msg []byte) {
	select {
	case c.send <- msg:
	default:
		c.close(websocket.StatusPolicyViolation, "terlalu lambat")
	}
}

func (c *Client) close(code websocket.StatusCode, reason string) {
	c.once.Do(func() {
		c.cancel()
		go c.conn.Close(code, reason) //nolint:errcheck
	})
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	opts := &websocket.AcceptOptions{}
	if len(s.Origins) == 0 {
		opts.InsecureSkipVerify = true
	} else {
		for _, o := range s.Origins {
			if u, err := url.Parse(o); err == nil && u.Host != "" {
				opts.OriginPatterns = append(opts.OriginPatterns, u.Host)
			}
		}
	}
	conn, err := websocket.Accept(w, r, opts)
	if err != nil {
		return // Accept sudah menulis respons error
	}
	conn.SetReadLimit(readLimit)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	user, err := s.authenticate(ctx, conn)
	if err != nil {
		// Gangguan server (mis. DB) — bukan token salah; pakai 1011 agar klien mencoba lagi.
		conn.Close(websocket.StatusInternalError, "server error") //nolint:errcheck
		return
	}
	if user == nil {
		msg, _ := json.Marshal(outgoing{Event: "error", Data: map[string]string{"message": "Unauthorized"}})
		wctx, wcancel := context.WithTimeout(ctx, 2*time.Second)
		conn.Write(wctx, websocket.MessageText, msg) //nolint:errcheck
		wcancel()
		conn.Close(CloseUnauthorized, "Unauthorized") //nolint:errcheck
		return
	}

	c := &Client{hub: s.Hub, conn: conn, user: user, send: make(chan []byte, sendBuffer),
		rooms: map[string]struct{}{}, cancel: cancel}
	defer s.Hub.leaveAll(c)
	defer c.close(websocket.StatusNormalClosure, "")

	ready, _ := json.Marshal(outgoing{Event: "ready", Data: map[string]string{"userId": user.ID}})
	c.send <- ready

	go c.writeLoop(ctx)
	go c.pingLoop(ctx)
	s.readLoop(ctx, c)
}

// authenticate: (nil, nil) = tidak terautentikasi; error = gangguan server.
func (s *Server) authenticate(ctx context.Context, conn *websocket.Conn) (*store.AuthUser, error) {
	actx, cancel := context.WithTimeout(ctx, authTimeout)
	defer cancel()
	_, data, err := conn.Read(actx)
	if err != nil {
		return nil, nil
	}
	var env Envelope
	if json.Unmarshal(data, &env) != nil || env.Event != "auth" {
		return nil, nil
	}
	var payload struct {
		Token string `json:"token"`
	}
	if json.Unmarshal(env.Data, &payload) != nil || payload.Token == "" {
		return nil, nil
	}
	user, err := s.Auth.UserFromToken(actx, payload.Token)
	if err != nil {
		slog.Error("realtime: auth gagal", "err", err)
		return nil, err
	}
	return user, nil
}

func (c *Client) writeLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-c.send:
			wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			err := c.conn.Write(wctx, websocket.MessageText, msg)
			cancel()
			if err != nil {
				c.close(websocket.StatusGoingAway, "")
				return
			}
		}
	}
}

func (c *Client) pingLoop(ctx context.Context) {
	t := time.NewTicker(pingInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			pctx, cancel := context.WithTimeout(ctx, pingTimeout)
			err := c.conn.Ping(pctx)
			cancel()
			if err != nil {
				c.close(websocket.StatusGoingAway, "ping timeout")
				return
			}
		}
	}
}

func (s *Server) readLoop(ctx context.Context, c *Client) {
	for {
		_, data, err := c.conn.Read(ctx)
		if err != nil {
			if !errors.Is(err, context.Canceled) && websocket.CloseStatus(err) == -1 {
				slog.Debug("realtime: koneksi putus", "user", c.user.ID, "err", err)
			}
			return
		}
		var env Envelope
		if json.Unmarshal(data, &env) != nil {
			continue
		}
		switch env.Event {
		case "join_room":
			var room string
			if json.Unmarshal(env.Data, &room) == nil {
				s.joinRoom(ctx, c, room)
			}
		case "location_update":
			s.locationUpdate(ctx, c, env.Data)
		}
	}
}

var roomRe = regexp.MustCompile(`^(collector|customer|order):([0-9a-fA-F-]{36})$`)

// joinRoom: hanya room milik sendiri (collector:<id>/customer:<id>) atau room
// order yang memang boleh dilihat user ini. Tanpa ini siapa pun bisa menyadap
// lokasi live / antrean order orang lain.
func (s *Server) joinRoom(ctx context.Context, c *Client, room string) {
	m := roomRe.FindStringSubmatch(room)
	if m == nil {
		return
	}
	kind, id := m[1], m[2]
	switch kind {
	case "collector":
		if id != c.user.ID || c.user.Role != store.RoleCollector {
			return
		}
	case "customer":
		if id != c.user.ID {
			return
		}
	case "order":
		order, err := store.GetOrder(ctx, s.DB, id)
		if err != nil {
			slog.Error("realtime: join_room gagal", "err", err)
			return
		}
		ok, err := store.CanViewOrder(ctx, s.DB, c.user, order)
		if err != nil {
			slog.Error("realtime: join_room gagal", "err", err)
			return
		}
		if !ok {
			return
		}
	}
	s.Hub.join(c, room)
}

// locationUpdate me-relay posisi pihak yang sedang menuju lokasi ke lawan
// transaksi. Hanya customer / pengepul yang ditetapkan pada order itu yang boleh
// mengirim; identitas & role pengirim ditentukan server.
func (s *Server) locationUpdate(ctx context.Context, c *Client, raw json.RawMessage) {
	var p struct {
		OrderID string `json:"orderId"`
		Lat     any    `json:"lat"`
		Lng     any    `json:"lng"`
	}
	if json.Unmarshal(raw, &p) != nil || p.OrderID == "" {
		return
	}
	lat, okLat := toNumber(p.Lat)
	lng, okLng := toNumber(p.Lng)
	if !okLat || !okLng || math.Abs(lat) > 90 || math.Abs(lng) > 180 {
		return
	}
	order, err := store.GetOrder(ctx, s.DB, p.OrderID)
	if err != nil {
		slog.Error("realtime: location_update gagal", "err", err)
		return
	}
	if !store.IsOrderParty(c.user.ID, order) {
		return
	}
	switch order.Status {
	case store.StatusOnTheWay, store.StatusConfirmed, store.StatusInProgress:
	default:
		return
	}
	role := store.RoleCustomer
	if order.CollectorID != nil && *order.CollectorID == c.user.ID {
		role = store.RoleCollector
	}
	s.Hub.emit("order:"+p.OrderID, "location_update", map[string]any{
		"orderId": p.OrderID, "lat": lat, "lng": lng, "role": role, "userId": c.user.ID,
	}, c)
}

// toNumber meniru Number(x) untuk angka & string angka; false bila tidak finite.
func toNumber(v any) (float64, bool) {
	var f float64
	switch x := v.(type) {
	case float64:
		f = x
	case string:
		var err error
		if f, err = strconv.ParseFloat(x, 64); err != nil {
			return 0, false
		}
	default:
		return 0, false
	}
	return f, !math.IsNaN(f) && !math.IsInf(f, 0)
}
