// Tes KYC (attestation), upload & rate limit. Butuh API berjalan dengan KYC_ATTESTATION_SECRET yang sama.
const { PrismaClient } = require('@prisma/client');
const crypto = require('crypto');
// Penanda tangan identik dengan rongsokin-web/lib/kycAttestation.ts
const signKycAttestation = (nik, nama) => {
  const body = Buffer.from(JSON.stringify({ nik, nama, exp: Math.floor(Date.now() / 1000) + 900 })).toString('base64url');
  return `${body}.${crypto.createHmac('sha256', process.env.KYC_ATTESTATION_SECRET).update(body).digest('base64url')}`;
};
const prisma = new PrismaClient();
const B = (process.env.API_URL || 'http://127.0.0.1:3999') + '/api/v1';
let pass = 0, fail = 0;
const ok = (c, m) => { c ? pass++ : fail++; console.log((c ? 'PASS ' : 'FAIL ') + m); };
const req = async (method, path, token, body, headers = {}) => {
  const r = await fetch(B + path, { method, headers: { 'content-type': 'application/json', ...(token ? { authorization: 'Bearer ' + token } : {}), ...headers }, body: body ? JSON.stringify(body) : undefined });
  return { status: r.status, body: await r.json().catch(() => null) };
};
const rnd = () => String(Math.floor(Math.random() * 1e4)).padStart(4, '1');
const mkNik = () => `3404${'01'}${'15'}${'08'}${'95'}${rnd()}`; // DIY, 15-08-1995
let n = 0; const ip = () => ({ 'cf-connecting-ip': `10.0.${Date.now() % 250}.${++n}` });
const reg = (extra, role = 'CUSTOMER') => req('POST', '/auth/register', null, { name: 'Budi', email: `k${Date.now()}${++n}@t.io`, password: 'password123', role, ...extra }, ip());

(async () => {
const nik1 = mkNik();
let r = await reg({ nik: nik1, ktpName: 'BUDI' });
ok(r.status === 201 && r.body.data.user.isVerified === false, 'register NIK tanpa attestation -> akun dibuat tapi TIDAK terverifikasi');
ok((await prisma.user.findUnique({ where: { id: r.body.data.user.id } })).nik === null, 'NIK tanpa bukti tidak disimpan (anti serobot NIK)');
const unverifiedTok = r.body.data.access_token;

r = await reg({ nik: nik1, ktpName: 'BUDI', kycAttestation: signKycAttestation(nik1, 'BUDI') });
ok(r.status === 201 && r.body.data.user.isVerified === true, 'register dengan attestation valid -> terverifikasi');

r = await reg({ nik: nik1, ktpName: 'BUDI', kycAttestation: signKycAttestation(nik1, 'BUDI') });
ok(r.status === 409, 'NIK yang sama tidak bisa dipakai akun kedua (' + r.status + ')');

const nik2 = mkNik();
r = await reg({ nik: nik2, kycAttestation: signKycAttestation(mkNik(), 'X') });
ok(r.status === 201 && r.body.data.user.isVerified === false, 'attestation untuk NIK lain -> tidak terverifikasi');

const att = signKycAttestation(nik2, 'BUDI');
const tampered = att.slice(0, -3) + (att.endsWith('AAA') ? 'BBB' : 'AAA');
r = await req('PATCH', '/auth/me', unverifiedTok, { nik: nik2, kycAttestation: tampered });
ok(r.status === 422, 'PATCH /me attestation dipalsukan -> 422');
r = await req('PATCH', '/auth/me', unverifiedTok, { nik: nik2 });
ok(r.status === 422, 'PATCH /me tanpa attestation -> 422');
r = await reg({ nik: '1234567890123456' });
ok(r.status === 422, 'NIK asal ketik (tanggal tidak valid) -> 422');

// pengepul belum verifikasi tidak bisa accept; setelah verifikasi bisa
const c = await reg({}, 'COLLECTOR'); const ct = c.body.data.access_token;
await req('PATCH', '/auth/me', ct, { lat: -7.78, lng: 110.37 });
await req('POST', '/collector/profile', ct, { shopName: 'Lapak Uji' });
const cust = await reg({}); const cu = cust.body.data.access_token;
await req('PATCH', '/auth/me', cu, { lat: -7.8, lng: 110.36 });
const cat = await prisma.wasteCategory.create({ data: { name: 'Kat' + Date.now() } });
r = await req('POST', '/orders', cu, { method: 'DROPOFF', lat: -7.8, lng: 110.36, collectorId: c.body.data.user.id, items: [{ categoryId: cat.id, estimatedWeight: 2 }] });
const oid = r.body.data.id;
r = await req('PATCH', `/orders/${oid}`, ct, { action: 'accept' });
ok(r.status === 403, 'pengepul belum verifikasi tidak bisa accept (' + r.status + ')');
const nik3 = mkNik();
r = await req('PATCH', '/auth/me', ct, { nik: nik3, ktpName: 'PENGEPUL', kycAttestation: signKycAttestation(nik3, 'PENGEPUL') });
ok(r.status === 200 && r.body.data.isVerified === true, 'pengepul verifikasi via attestation');
r = await req('PATCH', `/orders/${oid}`, ct, { action: 'accept' });
ok(r.status === 200, 'setelah verifikasi, accept berhasil');

// upload
const fd = new FormData(); fd.append('image', new Blob([Buffer.from('x')], { type: 'image/png' }), 'a.png');
let u = await fetch(B + '/upload', { method: 'POST', body: fd });
ok(u.status === 401, 'upload tanpa login -> 401');
const fd2 = new FormData(); fd2.append('image', new Blob(['<svg/>'], { type: 'image/svg+xml' }), 'a.svg');
u = await fetch(B + '/upload', { method: 'POST', body: fd2, headers: { authorization: 'Bearer ' + cu } });
ok(u.status === 400, 'upload SVG ditolak (' + u.status + ')');

// rate limit login per email
const email = (await prisma.user.findFirst({ where: { role: 'CUSTOMER' } })).email;
const codes = [];
for (let i = 0; i < 12; i++) codes.push((await req('POST', '/auth/login', null, { email, password: 'salahsalah' }, ip())).status);
ok(codes.slice(0, 10).every(c => c === 401) && codes[10] === 429, 'login: 10x salah lalu 429 (' + codes.join(',') + ')');
const fixedIp = { 'cf-connecting-ip': '203.0.113.9' };
const fc = [];
for (let i = 0; i < 4; i++) fc.push((await req('POST', '/auth/forgot-password', null, { email: 'nobody@t.io' }, fixedIp)).status);
ok(fc[3] === 429, 'forgot-password per email dibatasi (' + fc.join(',') + ')');
r = await req('PATCH', '/auth/me', cu, { lat: -7.8, lng: 110.36 });
ok(r.status === 200, 'update lokasi biasa tidak kena limit KYC');

console.log(`\n${pass} pass, ${fail} fail`);
await prisma.$disconnect(); process.exit(fail ? 1 : 0);
})().catch((e) => { console.error(e); process.exit(1); });
