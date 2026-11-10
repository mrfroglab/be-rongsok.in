// Tes otorisasi order, socket, rating & forgot-password (butuh API + DB berjalan).
// Jalankan: npm test  (lihat tests/run.sh)
const { io } = require('socket.io-client');
const { PrismaClient } = require('@prisma/client');
const prisma = new PrismaClient();
const SOCK = process.env.API_URL || 'http://127.0.0.1:3999';
const B = SOCK + '/api/v1';
let pass = 0, fail = 0;
const ok = (cond, msg) => { cond ? pass++ : fail++; console.log((cond ? 'PASS ' : 'FAIL ') + msg); };
const req = async (method, path, token, body) => {
  const r = await fetch(B + path, { method, headers: { 'content-type': 'application/json', ...(token ? { authorization: 'Bearer ' + token } : {}) }, body: body ? JSON.stringify(body) : undefined });
  return { status: r.status, body: await r.json().catch(() => null) };
};
const reg = async (name, role) => {
  const r = await req('POST', '/auth/register', null, { name, email: `${name}${Date.now()}@t.io`, password: 'password123', role });
  return { token: r.body.data.access_token, id: r.body.data.user.id };
};
const sock = (token) => new Promise((res) => {
  const s = io(SOCK, { auth: { token }, transports: ['websocket'], reconnection: false });
  s.on('connect', () => res({ s, ok: true })); s.on('connect_error', () => res({ s, ok: false }));
});
const wait = (ms) => new Promise(r => setTimeout(r, ms));
(async () => {
  const cat = await prisma.wasteCategory.create({ data: { name: 'Plastik' + Date.now() } });
  const A = await reg('custa', 'CUSTOMER'), X = await reg('attacker', 'CUSTOMER');
  const C1 = await reg('coll1', 'COLLECTOR'), C2 = await reg('coll2', 'COLLECTOR');
  for (const [c, lat] of [[C1, -7.78], [C2, -7.79]]) {
    await req('PATCH', '/auth/me', c.token, { lat, lng: 110.37 });
    await req('POST', '/collector/profile', c.token, { shopName: 'Lapak ' + c.id.slice(0, 4) });
  }
  await prisma.user.updateMany({ where: { id: { in: [C1.id, C2.id] } }, data: { isVerified: true } });
  await req('PATCH', '/auth/me', A.token, { lat: -7.8, lng: 110.36 });

  // forward ke non-collector / diri sendiri
  let r = await req('POST', '/orders', A.token, { method: 'DROPOFF', lat: -7.8, lng: 110.36, collectorId: X.id, items: [{ categoryId: cat.id, estimatedWeight: 3 }] });
  ok(r.status === 400, 'forward ke non-pengepul ditolak (' + r.status + ')');

  r = await req('POST', '/orders', A.token, { method: 'DROPOFF', lat: -7.8, lng: 110.36, items: [{ categoryId: cat.id, estimatedWeight: 3 }] });
  ok(r.status === 201, 'customer buat order war');
  const orderId = r.body.data.id;

  // socket
  const noTok = await sock(undefined); ok(!noTok.ok, 'socket tanpa token ditolak'); noTok.s.close();
  const sx = (await sock(X.token)).s, sa = (await sock(A.token)).s, sc1 = (await sock(C1.token)).s;
  let xGot = 0, aGotLoc = null;
  sx.on('order_status_update', () => xGot++); sx.on('location_update', () => xGot++); sx.on('new_order', () => xGot++);
  sa.on('location_update', (p) => aGotLoc = p);
  sx.emit('join_room', `order:${orderId}`); sx.emit('join_room', `collector:${C1.id}`);
  sa.emit('join_room', `order:${orderId}`); sc1.emit('join_room', `order:${orderId}`);
  await wait(300);

  r = await req('GET', `/orders/${orderId}`, X.token); ok(r.status === 403, 'attacker GET detail order -> 403 (' + r.status + ')');
  r = await req('GET', `/orders/${orderId}`, C1.token); ok(r.status === 200, 'pengepul kandidat war bisa lihat detail');
  r = await req('GET', `/orders/${orderId}`, A.token);
  ok(r.status === 200 && !('passwordHash' in r.body.data.customer) && !('nik' in r.body.data.customer), 'detail order tanpa passwordHash/nik');

  r = await req('PATCH', `/orders/${orderId}`, X.token, { action: 'accept' }); ok(r.status === 403, 'customer lain tidak bisa accept (' + r.status + ')');
  const [a1, a2] = await Promise.all([req('PATCH', `/orders/${orderId}`, C1.token, { action: 'accept' }), req('PATCH', `/orders/${orderId}`, C2.token, { action: 'accept' })]);
  ok([a1.status, a2.status].filter(s => s === 200).length === 1, `accept bersamaan: tepat satu menang (${a1.status},${a2.status})`);
  const winner = a1.status === 200 ? C1 : C2, loser = winner === C1 ? C2 : C1;
  const sw = winner === C1 ? sc1 : (await sock(C2.token)).s; sw.emit('join_room', `order:${orderId}`); await wait(200);

  sw.emit('location_update', { orderId, lat: -7.79, lng: 110.37, role: 'X' }); await wait(300);
  ok(aGotLoc && aGotLoc.lat === -7.79, 'customer menerima live location dari pengepul');
  sx.emit('location_update', { orderId, lat: 1, lng: 1 }); aGotLoc = null; await wait(300);
  ok(aGotLoc === null, 'attacker tidak bisa kirim location palsu');

  r = await req('PATCH', `/orders/${orderId}`, A.token, { action: 'arrive' }); ok(r.status === 200, 'arrive oleh customer');
  const item = (await prisma.orderItem.findFirst({ where: { orderId } }));
  r = await req('PATCH', `/orders/${orderId}`, A.token, { action: 'validate', items: [{ id: item.id, actualWeight: 3, agreedPrice: 1 }] });
  ok(r.status === 403, 'customer tidak bisa validate harga sendiri (' + r.status + ')');
  r = await req('PATCH', `/orders/${orderId}`, loser.token, { action: 'validate', items: [{ id: item.id, actualWeight: 3, agreedPrice: 1 }] });
  ok(r.status === 403, 'pengepul yang kalah tidak bisa validate');
  r = await req('PATCH', `/orders/${orderId}`, winner.token, { action: 'validate', items: [{ id: item.id, actualWeight: -3, agreedPrice: 1 }] });
  ok(r.status === 422, 'berat negatif ditolak');
  r = await req('PATCH', `/orders/${orderId}`, winner.token, { action: 'validate', items: [{ id: '00000000-0000-0000-0000-000000000000', actualWeight: 3, agreedPrice: 1 }] });
  ok(r.status === 422, 'item id milik order lain ditolak');
  r = await req('PATCH', `/orders/${orderId}`, winner.token, { action: 'validate', items: [{ id: item.id, actualWeight: 3, agreedPrice: 2000 }] });
  ok(r.status === 200, 'pengepul pemenang validate');
  r = await req('PATCH', `/orders/${orderId}`, winner.token, { action: 'confirm' }); ok(r.status === 403, 'pengepul tidak bisa confirm sendiri');
  const [c1, c2] = await Promise.all([req('PATCH', `/orders/${orderId}`, A.token, { action: 'confirm' }), req('PATCH', `/orders/${orderId}`, A.token, { action: 'confirm' })]);
  ok([c1.status, c2.status].filter(s => s === 200).length === 1 && ![c1.status, c2.status].includes(500), `double confirm: satu sukses, tanpa 500 (${c1.status},${c2.status})`);
  const o = await prisma.order.findUnique({ where: { id: orderId }, include: { receipt: true } });
  ok(o.status === 'COMPLETED' && o.totalPrice === 6000 && o.receipt, 'order COMPLETED, total 6000, receipt ada');

  r = await req('POST', '/ratings', X.token, { orderId, rateeId: winner.id, score: 1 }); ok(r.status === 403, 'attacker tidak bisa rating (' + r.status + ')');
  r = await req('POST', '/ratings', A.token, { orderId, rateeId: X.id, score: 1 }); ok(r.status === 400, 'rating ke pihak luar ditolak');
  r = await req('POST', '/ratings', A.token, { orderId, rateeId: winner.id, score: 5 }); ok(r.status === 201, 'customer rating pengepul');

  ok(xGot === 0, 'attacker tidak menerima event apa pun dari room orang lain');
  r = await req('POST', '/auth/forgot-password', null, { email: 'tidakada@t.io' }); ok(r.status === 200, 'forgot-password email tak terdaftar -> 200 generik');

  console.log(`\n${pass} pass, ${fail} fail`);
  [sx, sa, sc1, sw].forEach(s => s.close()); await prisma.$disconnect(); process.exit(fail ? 1 : 0);
})().catch(e => { console.error(e); process.exit(1); });
