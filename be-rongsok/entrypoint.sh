#!/bin/sh
# entrypoint.sh for Rongsok.in API
#
# PENTING: JANGAN `exit 1` kalau db push gagal. Dulu satu perubahan schema yang bikin
# db push error langsung mematikan container -> CapRover crash-loop -> 502 total.
# Sekarang db push best-effort: kalau gagal, server TETAP start dgn schema yang ada
# supaya API tetap hidup (endpoint lama jalan), bukan seluruh backend mati.

echo "=================================================="
echo "      Rongsok.in API Docker Bootstrapping"
echo "=================================================="

echo "🚀 Sinkron schema (prisma db push, best-effort)..."
RETRIES=10
DB_OK=0
while [ "$RETRIES" -gt 0 ]; do
  # TANPA --accept-data-loss: perubahan schema yang menghapus kolom/data harus
  # lewat migrasi yang disengaja, bukan diam-diam saat container start.
  if npx prisma db push; then
    DB_OK=1
    break
  fi
  RETRIES=$((RETRIES - 1))
  echo "⏳ db push gagal (DB belum siap / perubahan schema bermasalah). Sisa retry: $RETRIES"
  sleep 3
done

if [ "$DB_OK" -eq 1 ]; then
  echo "✅ Schema tersinkron."
else
  echo "⚠️ db push tidak berhasil — LANJUT start server pakai schema yang ada (hindari downtime 502)."
fi

# Seeder best-effort — jangan pernah menjatuhkan container.
echo "🌱 Menjalankan seeder (opsional)..."
# seed.js sendiri menolak jalan bila DB sudah berisi data (kecuali SEED_FORCE=true).
node prisma/seed.js || echo "⚠️ Seeding gagal / dilewati."

echo "🚀 Menjalankan Rongsok.in API Server..."
exec npm start
