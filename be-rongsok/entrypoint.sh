#!/bin/sh
# entrypoint.sh for Rongsok.in API
#
# Skema dikelola `prisma migrate deploy` (bukan lagi `db push`): perubahan skema
# harus berupa file migrasi di prisma/migrations yang ter-review & ter-commit.
# Kalau migrasi gagal, server TETAP start dengan skema yang ada (hindari 502
# total) — tapi kegagalannya tercatat jelas di log.

BASELINE=20261110000000_baseline

echo "=================================================="
echo "      Rongsok.in API Docker Bootstrapping"
echo "=================================================="

migrate() { npx prisma migrate deploy 2>&1; }

echo "🚀 Menerapkan migrasi (prisma migrate deploy)..."
RETRIES=10
DB_OK=0
while [ "$RETRIES" -gt 0 ]; do
  OUT=$(migrate); RC=$?
  echo "$OUT" | grep -v "Update available\|│\|┌\|└"
  if [ "$RC" -eq 0 ]; then DB_OK=1; break; fi
  # P3005 = DB sudah berisi tabel tapi belum punya riwayat migrasi (dulu pakai
  # db push). Skemanya identik dengan baseline → tandai baseline sudah diterapkan.
  if echo "$OUT" | grep -q "P3005"; then
    echo "ℹ️  DB lama tanpa riwayat migrasi → menandai $BASELINE sebagai sudah diterapkan."
    npx prisma migrate resolve --applied "$BASELINE" >/dev/null 2>&1 && continue
  fi
  RETRIES=$((RETRIES - 1))
  echo "⏳ migrate deploy gagal (DB belum siap / migrasi bermasalah). Sisa retry: $RETRIES"
  sleep 3
done

if [ "$DB_OK" -eq 1 ]; then
  echo "✅ Skema up to date."
else
  echo "⚠️ MIGRASI GAGAL — server tetap start dengan skema yang ada. Periksa log di atas."
fi

# seed.js sendiri menolak jalan bila DB sudah berisi data (kecuali SEED_FORCE=true).
echo "🌱 Menjalankan seeder (opsional)..."
node prisma/seed.js || echo "⚠️ Seeding gagal / dilewati."

echo "🚀 Menjalankan Rongsok.in API Server..."
exec npm start
