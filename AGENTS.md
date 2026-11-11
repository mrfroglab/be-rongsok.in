# Panduan untuk agent / kontributor

Backend Go untuk Rongsok.in. Baca README untuk struktur. Aturan kerja:

- **Kontrak API dengan FE (`rongsokin-web`) jangan berubah diam-diam.** Nama field JSON, status code, dan
  pesan error dipakai FE. Format: `{"status":"success","data":...}` / `{"status":"error","message":...}`.
  Waktu diserialisasi `store.Time` (ISO UTC dengan milidetik).
- **Satu modul = satu file handler** (`internal/handler/<modul>.go`) + query di `internal/store/`.
  Route didaftarkan di `internal/server/server.go`. SQL hanya di `store` (handler tidak menulis SQL).
- **Otorisasi**: role selalu dari DB (`middleware.User(r)`), bukan dari JWT. Akses order lewat
  `store.CanViewOrder` / `store.IsOrderParty`. Transisi status order lewat `store.Transition` (atomik).
- **Kolom `updatedAt` Order wajib diisi manual** (dulu otomatis oleh Prisma) — `store.Transition` sudah melakukannya.
- **Skema**: tambah file baru di `migrations/`, jangan ubah file lama.
- **Tes**: setiap perubahan perilaku → tambah/ubah tes di `tests/` (butuh `TEST_DATABASE_URL`).
  Wajib lulus: `gofmt -l .` kosong, `go vet ./...`, `go test -race ./...`.
- Komentar & pesan user dalam Bahasa Indonesia, konsisten dengan kode yang ada.
