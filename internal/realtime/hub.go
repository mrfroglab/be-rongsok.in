// Package realtime: WebSocket (pengganti Socket.IO) dengan room & event.
//
// Protokol (semua frame teks JSON {"event": string, "data": any}):
//
//	client → server  auth             {"token": "<JWT>"}       wajib pertama, maks 10 detik
//	server → client  ready            {"userId": "..."}        auth berhasil
//	server → client  error            {"message": "..."}       auth gagal → koneksi ditutup (4401)
//	client → server  join_room        "collector:<id>" | "customer:<id>" | "order:<id>"
//	client → server  location_update  {"orderId", "lat", "lng"}
//	server → client  new_order | order_taken | order_status_update | location_update
//
// Nama event & payload sama dengan versi Socket.IO, jadi handler FE tidak berubah.
package realtime

import (
	"encoding/json"
	"log/slog"
	"sync"
)

// Envelope = satu pesan.
type Envelope struct {
	Event string          `json:"event"`
	Data  json.RawMessage `json:"data,omitempty"`
}

type outgoing struct {
	Event string `json:"event"`
	Data  any    `json:"data,omitempty"`
}

// Hub menyimpan keanggotaan room. Aman dipakai bersamaan.
type Hub struct {
	mu    sync.RWMutex
	rooms map[string]map[*Client]struct{}
}

func NewHub() *Hub { return &Hub{rooms: map[string]map[*Client]struct{}{}} }

func (h *Hub) join(c *Client, room string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	members := h.rooms[room]
	if members == nil {
		members = map[*Client]struct{}{}
		h.rooms[room] = members
	}
	members[c] = struct{}{}
	c.rooms[room] = struct{}{}
}

func (h *Hub) leaveAll(c *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for room := range c.rooms {
		if members := h.rooms[room]; members != nil {
			delete(members, c)
			if len(members) == 0 {
				delete(h.rooms, room)
			}
		}
	}
	c.rooms = map[string]struct{}{}
}

// Emit mengirim event ke semua anggota room.
func (h *Hub) Emit(room, event string, data any) { h.emit(room, event, data, nil) }

// emit ke anggota room kecuali `except` (setara socket.to(room) di Socket.IO).
func (h *Hub) emit(room, event string, data any, except *Client) {
	msg, err := json.Marshal(outgoing{Event: event, Data: data})
	if err != nil {
		slog.Error("realtime: gagal encode event", "event", event, "err", err)
		return
	}
	h.mu.RLock()
	targets := make([]*Client, 0, len(h.rooms[room]))
	for c := range h.rooms[room] {
		if c != except {
			targets = append(targets, c)
		}
	}
	h.mu.RUnlock()
	for _, c := range targets {
		c.enqueue(msg)
	}
}

// RoomSize: jumlah koneksi di room (untuk tes/diagnostik).
func (h *Hub) RoomSize(room string) int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.rooms[room])
}
