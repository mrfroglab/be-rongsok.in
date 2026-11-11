// Package migrations menyimpan file SQL skema (di-embed ke binary).
//
// Aturan: perubahan skema = file baru `<YYYYMMDDHHMMSS>_<nama>.sql`. Jangan
// pernah mengubah file yang sudah ter-deploy. Dua file pertama adalah salinan
// migrasi Prisma lama (nama versinya sama) supaya DB produksi yang dulu
// dikelola `prisma migrate` dikenali sebagai sudah ter-migrasi.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
