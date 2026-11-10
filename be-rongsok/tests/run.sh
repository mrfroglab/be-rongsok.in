#!/usr/bin/env bash
# Jalankan seluruh tes integrasi terhadap API lokal.
# Butuh: DATABASE_URL (DB kosong, PostGIS) — skrip menerapkan migrasi, start API, lalu tes.
set -euo pipefail
cd "$(dirname "$0")/.."
: "${DATABASE_URL:?set DATABASE_URL ke DB PostGIS khusus tes}"
export PORT=${PORT:-3999} API_URL=${API_URL:-http://127.0.0.1:${PORT:-3999}}
export JWT_SECRET=${JWT_SECRET:-$(node -e "console.log(require('crypto').randomBytes(48).toString('hex'))")}
export KYC_ATTESTATION_SECRET=${KYC_ATTESTATION_SECRET:-$(node -e "console.log(require('crypto').randomBytes(32).toString('hex'))")}
npx prisma migrate deploy >/dev/null
node src/server.js > /tmp/rongsokin-test-api.log 2>&1 &
API_PID=$!
trap 'kill $API_PID 2>/dev/null || true' EXIT
for _ in $(seq 1 30); do curl -sf "$API_URL/health" >/dev/null && break; sleep 1; done
rc=0
for t in tests/*.test.js; do echo "=== $t"; node "$t" || rc=1; done
exit $rc
