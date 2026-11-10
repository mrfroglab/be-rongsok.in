// Rate limiter in-memory (fixed window). Cukup untuk deployment 1 instance
// (docker compose di home server). Kalau nanti di-scale >1 instance, ganti ke
// store bersama (Redis).

// IP klien asli. API di-bind ke 127.0.0.1 dan hanya bisa dicapai lewat
// Cloudflare Tunnel, jadi header dari proxy bisa dipercaya. Tanpa ini, semua
// request terlihat dari IP tunnel yang sama → satu ember untuk semua user.
const clientIp = (req) => {
  const cf = req.headers['cf-connecting-ip'];
  if (typeof cf === 'string' && cf) return cf.trim();
  const xff = req.headers['x-forwarded-for'];
  if (typeof xff === 'string' && xff) return xff.split(',')[0].trim();
  return req.ip || (req.socket && req.socket.remoteAddress) || 'unknown';
};

const buckets = new Map(); // key -> { count, resetAt }

// Sapu entri kedaluwarsa supaya Map tidak tumbuh tanpa batas.
setInterval(() => {
  const now = Date.now();
  for (const [k, b] of buckets) if (b.resetAt <= now) buckets.delete(k);
}, 60 * 1000).unref();

// Kembalikan true kalau masih di bawah batas (dan catat hit-nya).
const hit = (key, max, windowMs) => {
  const now = Date.now();
  let b = buckets.get(key);
  if (!b || b.resetAt <= now) {
    b = { count: 0, resetAt: now + windowMs };
    buckets.set(key, b);
  }
  b.count += 1;
  return { allowed: b.count <= max, retryAfterSec: Math.ceil((b.resetAt - now) / 1000) };
};

/**
 * @param {object} opts
 * @param {string} opts.name         namespace bucket
 * @param {number} opts.max          maks request per window
 * @param {number} opts.windowMs     panjang window
 * @param {(req) => string|null} [opts.key]  kunci tambahan (mis. email); default IP
 * @param {string} [opts.message]
 */
const rateLimit = ({ name, max, windowMs, key, message }) => (req, res, next) => {
  const k = key ? key(req) : clientIp(req);
  if (!k) return next();
  const { allowed, retryAfterSec } = hit(`${name}:${k}`, max, windowMs);
  if (allowed) return next();
  res.set('Retry-After', String(retryAfterSec));
  return res.status(429).json({
    status: 'error',
    message: message || 'Terlalu banyak percobaan. Coba lagi beberapa saat lagi.'
  });
};

const emailKey = (req) => {
  const e = req.body && typeof req.body.email === 'string' ? req.body.email.trim().toLowerCase() : '';
  return e || null;
};

const MIN = 60 * 1000;
const HOUR = 60 * MIN;

const limiters = {
  loginIp: rateLimit({ name: 'login-ip', max: 30, windowMs: 15 * MIN }),
  loginEmail: rateLimit({ name: 'login-email', max: 10, windowMs: 15 * MIN, key: emailKey,
    message: 'Terlalu banyak percobaan login untuk akun ini. Coba lagi 15 menit lagi.' }),
  register: rateLimit({ name: 'register', max: 10, windowMs: HOUR }),
  forgotIp: rateLimit({ name: 'forgot-ip', max: 10, windowMs: HOUR }),
  forgotEmail: rateLimit({ name: 'forgot-email', max: 3, windowMs: HOUR, key: emailKey }),
  resetPassword: rateLimit({ name: 'reset', max: 10, windowMs: 15 * MIN }),
  // Hanya menghitung PATCH /auth/me yang membawa NIK (percobaan verifikasi),
  // bukan update profil/lokasi biasa.
  kyc: rateLimit({ name: 'kyc', max: 10, windowMs: HOUR,
    key: (req) => (req.body && req.body.nik && req.user ? req.user.id : null),
    message: 'Terlalu banyak percobaan verifikasi. Coba lagi nanti.' }),
  upload: rateLimit({ name: 'upload', max: 60, windowMs: HOUR, key: (req) => req.user && req.user.id,
    message: 'Batas unggah foto tercapai. Coba lagi nanti.' })
};

module.exports = { rateLimit, limiters, clientIp };
