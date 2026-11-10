const crypto = require('crypto');

// ── Attestation KYC ──────────────────────────────────────────────────────────
// NIK dulu dipercaya mentah dari client (OCR jalan di browser), jadi siapa pun
// bisa mengetik NIK orang lain dan langsung dapat badge "Terverifikasi", atau
// "menyerobot" NIK orang supaya pemilik aslinya tak bisa daftar.
//
// Sekarang OCR yang sah jalan di server FE (Next route /api/ktp-ocr, Gemini).
// Route itu menandatangani hasil bacaannya dengan HMAC-SHA256 memakai secret
// bersama KYC_ATTESTATION_SECRET (diset di Vercel DAN di BE). BE hanya memberi
// isVerified=true kalau tanda tangan valid, belum kedaluwarsa, dan NIK-nya cocok.
//
// Format token: base64url(JSON{ nik, nama, exp }) + "." + base64url(HMAC)
// (harus identik dengan rongsokin-web/lib/kycAttestation.ts)

const MIN_SECRET_LENGTH = 32;

const kycSecret = () => {
  const s = process.env.KYC_ATTESTATION_SECRET;
  return s && s.length >= MIN_SECRET_LENGTH ? s : null;
};

const isKycEnforced = () => Boolean(kycSecret());

const verifyAttestation = (token) => {
  const secret = kycSecret();
  if (!secret || typeof token !== 'string' || token.length > 2048) return null;
  const [body, sig] = token.split('.');
  if (!body || !sig) return null;

  const expected = crypto.createHmac('sha256', secret).update(body).digest();
  let given;
  try {
    given = Buffer.from(sig, 'base64url');
  } catch {
    return null;
  }
  if (given.length !== expected.length || !crypto.timingSafeEqual(given, expected)) return null;

  let payload;
  try {
    payload = JSON.parse(Buffer.from(body, 'base64url').toString('utf8'));
  } catch {
    return null;
  }
  if (!payload || typeof payload.nik !== 'string' || typeof payload.exp !== 'number') return null;
  if (payload.exp < Math.floor(Date.now() / 1000)) return null;
  return { nik: payload.nik, nama: typeof payload.nama === 'string' ? payload.nama : '' };
};

// ── Validasi struktur NIK ────────────────────────────────────────────────────
// 16 digit: PP KK CC DDMMYY NNNN. Tanggal lahir di digit 7–12; perempuan
// tanggal +40. Ini bukan bukti kepemilikan, cuma menyaring NIK asal ketik.
const isPlausibleNik = (nik) => {
  if (typeof nik !== 'string' || !/^\d{16}$/.test(nik)) return false;
  if (nik.slice(0, 2) === '00' || nik.slice(2, 4) === '00' || nik.slice(12) === '0000') return false;
  let day = parseInt(nik.slice(6, 8), 10);
  const month = parseInt(nik.slice(8, 10), 10);
  if (day > 40) day -= 40;
  return day >= 1 && day <= 31 && month >= 1 && month <= 12;
};

/**
 * Tentukan hasil KYC dari input client.
 * @returns {{ verified: boolean, nik: string|null, ktpName: string|null }}
 *   nik hanya diisi kalau terverifikasi (atau mode legacy) — NIK yang tak
 *   terbukti TIDAK disimpan, supaya tak bisa dipakai menyerobot identitas orang.
 */
const resolveKyc = ({ nik, ktpName, kycAttestation }) => {
  if (!nik) return { verified: false, nik: null, ktpName: null };

  if (!isKycEnforced()) {
    // Mode legacy (KYC_ATTESTATION_SECRET belum diset): perilaku lama.
    return { verified: true, nik, ktpName: ktpName || null };
  }

  const att = verifyAttestation(kycAttestation);
  if (!att || att.nik !== nik) return { verified: false, nik: null, ktpName: null };
  // Nama boleh dikoreksi user (OCR kadang salah eja); yang dikunci adalah NIK.
  return { verified: true, nik, ktpName: ktpName || att.nama || null };
};

module.exports = { isKycEnforced, verifyAttestation, isPlausibleNik, resolveKyc };
