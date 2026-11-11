# Backend Rongsok.in 🔄

Backend server untuk platform Rongsok.in — marketplace daur ulang sirkular berbasis geolokasi yang menghubungkan pelanggan (Customer) dengan pengepul sampah terdekat (Collector) secara real-time.

> Versi Node.js/Express lama tersimpan di tag `v1-express` dan branch `legacy/express`.

## 🚀 Fitur Utama
- **Smart Search**: pengepul terdekat via PostgreSQL + PostGIS (`ST_DWithin` & `ST_Distance`).
- **Real-time OMS**: WebSocket (`/api/v1/ws`) — pesanan baru, status, live tracking lokasi.
- **Rating Dua Arah**: reputasi untuk pelanggan dan pengepul.
- **KYC**: verifikasi KTP lewat attestation bertanda tangan dari OCR server FE.

## 🏗️ Tech Stack
- **Bahasa & Router**: Go 1.26+ · chi
- **Database**: PostgreSQL + PostGIS · pgx (SQL langsung, tanpa ORM)
- **Real-time**: WebSocket (coder/websocket)
- **Keamanan**: JWT HS256 · bcrypt · rate limit in-memory
- **Gambar**: Cloudinary (signed upload)
- **Deploy**: Docker (distroless) di home server, Caddy + Cloudflare Tunnel

## 📁 Struktur

```
cmd/api/            entrypoint (serve | migrate | seed --force | healthcheck)
internal/
  app/              merangkai dependensi (dipakai main & tes)
  server/           router (semua route didaftarkan di sini)
  handler/          handler HTTP, satu file per modul: auth, collector, discovery, order, rating, admin, upload
  store/            akses data (SQL) + model JSON
  realtime/         hub WebSocket: room, event, auth
  middleware/       auth JWT, rate limit, CORS, logging
  security/         JWT, bcrypt, attestation KYC
  db/               pool pgx, transaksi, migrator
  jobs/             auto-batal order basi (war 15 menit, OTW 3 jam)
  seed/ mail/ cloudinary/ config/ httpx/
migrations/         file SQL skema (di-embed ke binary)
docs/               swagger.yaml (disajikan di /api-docs) + koleksi Postman
tests/              tes integrasi end-to-end (HTTP + WebSocket + PostGIS)
```

## 🛠️ Quick Start

```bash
cp .env.example .env          # isi DATABASE_URL & JWT_SECRET (min 32 karakter)
go run ./cmd/api              # migrasi otomatis → seed bila DB kosong → server :3000
```

Atau semuanya lewat Docker: `JWT_SECRET=$(openssl rand -hex 48) docker compose up --build`.

- API: `http://localhost:3000/api/v1` · Docs: `http://localhost:3000/api-docs` · Health: `/health`
- Akun demo (hanya saat DB kosong): `budi@collector.com / collector123`, `rizky@example.com / customer123`, `admin@rongsok.in / $SEED_ADMIN_PASSWORD`
- Reset total data demo: `go run ./cmd/api seed --force` (**menghapus semua data**)

## 🧪 Tes

```bash
docker run -d --name rongsokin-testdb -e POSTGRES_USER=u -e POSTGRES_PASSWORD=p -e POSTGRES_DB=d \
  -p 127.0.0.1:5433:5432 postgis/postgis:15-3.3-alpine
TEST_DATABASE_URL=postgres://u:p@127.0.0.1:5433/d go test -race ./...
```

Tanpa `TEST_DATABASE_URL`, hanya unit test yang jalan. CI (`.github/workflows/deploy.yml`) menjalankan gofmt, vet & semua tes sebelum build image dan deploy.

## 🔌 Realtime (WebSocket)

`GET /api/v1/ws` — frame teks JSON `{ "event": string, "data": any }`.

| Arah | Event | Data |
|---|---|---|
| client → server | `auth` (wajib pertama, ≤10 dtk) | `{ "token": "<JWT>" }` |
| server → client | `ready` / `error` (lalu close 4401) | `{ "userId" }` / `{ "message" }` |
| client → server | `join_room` | `"collector:<id>"`, `"customer:<id>"`, `"order:<id>"` |
| client → server | `location_update` | `{ orderId, lat, lng }` |
| server → client | `new_order`, `order_taken`, `order_status_update`, `location_update` | sama dengan versi Socket.IO |

## 🗄️ Skema & migrasi

Skema dikelola file SQL di `migrations/` dan diterapkan otomatis saat start (tabel `schema_migrations`, dengan advisory lock).
Perubahan skema = file baru `YYYYMMDDHHMMSS_nama.sql`; file yang sudah ter-deploy tidak boleh diubah.
DB lama yang dulu dikelola Prisma (`_prisma_migrations`) atau `db push` diadopsi otomatis tanpa menjalankan ulang baseline.

---
*Developed for OLIVIA Competition @ Yogyakarta* ♻️
