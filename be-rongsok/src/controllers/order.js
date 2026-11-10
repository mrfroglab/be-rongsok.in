const prisma = require('../config/prisma');
const { z } = require('zod');
const { canViewOrder } = require('../utils/orderAccess');

// Field user yang aman dikirim ke lawan transaksi. JANGAN pakai `include: { customer: true }`
// — itu ikut mengirim passwordHash, NIK, token reset password, dll.
const PUBLIC_USER_SELECT = {
  id: true, name: true, phone: true, avatarUrl: true, avgRating: true,
  isVerified: true, addressText: true, role: true
};

const forbidden = (res, message = 'Anda bukan bagian dari pesanan ini') =>
  res.status(403).json({ status: 'error', message });

const orderSchema = z.object({
  categoryId: z.string().optional(),
  estimatedWeight: z.number().positive().max(100000).optional(),
  notes: z.string().max(1000).optional(),
  method: z.enum(['PICKUP', 'DROPOFF']),
  photoUrl: z.string().optional(),
  addressText: z.string().max(500).optional(),
  lat: z.number().min(-90).max(90),
  lng: z.number().min(-180).max(180),
  collectorId: z.string().optional(),
  items: z.array(z.object({
    categoryId: z.string(),
    estimatedWeight: z.number().positive().max(100000),
    notes: z.string().max(1000).optional()
  })).max(50).optional()
});

const createOrder = async (req, res, next) => {
  try {
    const data = orderSchema.parse(req.body);
    const io = req.app.get('io');

    // Order forward wajib ditujukan ke pengepul sungguhan (bukan sembarang userId)
    // dan bukan ke diri sendiri (cegah wash-trading poin eco/leaderboard/rating).
    let forwardCollector = null;
    if (data.collectorId) {
      if (data.collectorId === req.user.id) {
        return res.status(400).json({ status: 'error', message: 'Tidak bisa memesan ke lapak sendiri' });
      }
      forwardCollector = await prisma.user.findUnique({
        where: { id: data.collectorId },
        select: { id: true, role: true, collectorProfile: { select: { id: true } } }
      });
      if (!forwardCollector || forwardCollector.role !== 'COLLECTOR' || !forwardCollector.collectorProfile) {
        return res.status(400).json({ status: 'error', message: 'Pengepul tujuan tidak ditemukan' });
      }
    }

    // Anti-scam: transaksi PERTAMA wajib DROP-OFF (antar sendiri). PICKUP baru
    // terbuka setelah customer punya >=1 order COMPLETED — mencegah pesanan jemput fiktif.
    if (data.method === 'PICKUP') {
      const completedCount = await prisma.order.count({
        where: { customerId: req.user.id, status: 'COMPLETED' }
      });
      if (completedCount === 0) {
        return res.status(403).json({
          status: 'error',
          message: 'Transaksi pertama wajib antar sendiri (drop-off). Opsi dijemput terbuka setelah 1 transaksi selesai.'
        });
      }
    }

    // Parse items to create
    let itemsToCreate = [];
    if (data.items && data.items.length > 0) {
      itemsToCreate = data.items;
    } else if (data.categoryId && data.estimatedWeight) {
      itemsToCreate = [{
        categoryId: data.categoryId,
        estimatedWeight: data.estimatedWeight,
        notes: data.notes
      }];
    } else {
      return res.status(422).json({
        status: 'error',
        message: 'Either items array or both categoryId and estimatedWeight must be provided'
      });
    }

    const totalEstimatedWeight = itemsToCreate.reduce((sum, item) => sum + item.estimatedWeight, 0);

    // 1. Create Order record
    const order = await prisma.order.create({
      data: {
        customerId: req.user.id,
        categoryId: itemsToCreate.length === 1 ? itemsToCreate[0].categoryId : null,
        estimatedWeight: totalEstimatedWeight,
        method: data.method,
        photoUrl: data.photoUrl,
        addressText: data.addressText || null,
        collectorId: data.collectorId || null,
        status: 'PENDING',
        items: {
          create: itemsToCreate.map(item => ({
            categoryId: item.categoryId,
            estimatedWeight: item.estimatedWeight,
            notes: item.notes || null
          }))
        }
      },
      include: {
        items: true
      }
    });

    const categoryIds = itemsToCreate.map(item => item.categoryId);

    // 2. Find collectors to notify
    let collectors = [];
    if (data.collectorId) {
      // Direct selection (forward) — sudah divalidasi di atas
      collectors = [{
        id: forwardCollector.collectorProfile.id,
        userId: forwardCollector.id
      }];
    } else {
      // Broadcast (REBUTAN) ke SEMUA pengepul yang BUKA — TANPA batas radius.
      // Pengepul tidak lagi punya radius layanan: semua tawaran masuk ke antrean
      // mereka, dan mereka bebas MENOLAK kalau kejauhan / kategori tak diterima.
      // Diurutkan dari yang terdekat supaya pengepul sekitar melihat lebih dulu.
      collectors = await prisma.$queryRaw`
        SELECT cp.id, cp."userId"
        FROM "CollectorProfile" cp
        JOIN "User" u ON cp."userId" = u.id
        WHERE
          cp."isOpen" = true AND
          u.location IS NOT NULL AND
          u.id <> ${req.user.id}
        ORDER BY ST_Distance(u.location, ST_SetSRID(ST_MakePoint(${parseFloat(data.lng)}, ${parseFloat(data.lat)}), 4326)::geography) ASC
      `;
    }

    // 3. Map order to collectors and broadcast
    const mappingData = collectors.map(c => ({
      orderId: order.id,
      collectorId: c.userId,
      status: 'notified'
    }));

    if (mappingData.length > 0) {
      await prisma.orderCollector.createMany({ data: mappingData });
      
      // Notify each collector via Socket.IO
      collectors.forEach(c => {
        io.to(`collector:${c.userId}`).emit('new_order', {
          orderId: order.id,
          createdAt: order.createdAt,
          // War (broadcast) = null; Forward (private, dipilih langsung dari lapak) =
          // userId pengepul. Dulu field ini TAK dikirim sama sekali → FE selalu
          // menganggap order baru sebagai War sampai polling REST 8 detik berikutnya
          // "membetulkan" — ada jendela order forward nongol salah panel.
          collectorId: data.collectorId || null,
          category: categoryIds[0],
          categories: categoryIds,
          estWeight: totalEstimatedWeight,
          method: data.method,
          // Foto live tumpukan rongsok (anti pesanan fiktif) — pengepul wajib lihat
          photoUrl: data.photoUrl,
          // Items multi-kategori supaya FE tak bergantung legacy single-category
          items: itemsToCreate.map(it => ({
            categoryId: it.categoryId,
            estimatedWeight: it.estimatedWeight
          }))
        });
      });
    }

    res.status(201).json({ status: 'success', data: order });
  } catch (error) {
    if (error instanceof z.ZodError) {
      return res.status(422).json({ status: 'error', message: 'Validation failed', errors: error.errors });
    }
    next(error);
  }
};

// Angka berat/harga dari client: wajib finite & tidak negatif.
const toAmount = (v) => {
  const n = typeof v === 'string' ? parseFloat(v) : v;
  return typeof n === 'number' && Number.isFinite(n) && n >= 0 && n <= 1e9 ? n : null;
};

const ACTIVE_COLLECTOR_STATUSES = ['CONFIRMED', 'ON_THE_WAY', 'IN_PROGRESS', 'AWAITING_CONFIRMATION'];

const updateStatus = async (req, res, next) => {
  try {
    const { id } = req.params;
    const { action } = req.body;
    const io = req.app.get('io');
    const userId = req.user.id;

    const order = await prisma.order.findUnique({ where: { id } });
    if (!order) return res.status(404).json({ status: 'error', message: 'Order not found' });

    const isCustomer = order.customerId === userId;
    const isCollector = Boolean(order.collectorId) && order.collectorId === userId;

    // Transisi status ATOMIK: update hanya kalau status di DB masih sesuai. Mencegah
    // race (dua pengepul "accept" bersamaan di mode war) & double submit.
    const transition = async (fromStatuses, data, extraWhere = {}) => {
      const { count } = await prisma.order.updateMany({
        where: { id, status: { in: fromStatuses }, ...extraWhere },
        data
      });
      return count === 1;
    };
    const conflict = () =>
      res.status(409).json({ status: 'error', message: 'Status pesanan sudah berubah, silakan muat ulang.' });

    let newStatus = order.status;

    switch (action) {
      case 'accept': {
        if (req.user.role !== 'COLLECTOR') {
          return forbidden(res, 'Hanya pengepul yang bisa menerima pesanan');
        }
        if (order.status !== 'PENDING') {
          return res.status(400).json({ status: 'error', message: 'Order already taken' });
        }
        if (order.collectorId) {
          // Forward/private: hanya pengepul tujuan
          if (order.collectorId !== userId) return forbidden(res, 'Pesanan ini ditujukan ke pengepul lain');
        } else {
          // War/broadcast: hanya pengepul yang dinotifikasi & belum menolak
          const candidate = await prisma.orderCollector.findFirst({
            where: { orderId: id, collectorId: userId, status: 'notified' },
            select: { id: true }
          });
          if (!candidate) return forbidden(res, 'Pesanan ini tidak ditawarkan ke Anda');
        }

        // Pengepul wajib terverifikasi KTP sebelum menerima pesanan (dulu cuma
        // dicegah di FE — tombolnya di-disable — tapi API-nya tetap terbuka).
        const me = await prisma.user.findUnique({ where: { id: userId }, select: { isVerified: true } });
        if (!me || !me.isVerified) {
          return forbidden(res, 'Verifikasi identitas (KTP) dulu sebelum menerima pesanan');
        }

        const collectorProfile = await prisma.collectorProfile.findUnique({ where: { userId } });
        if (!collectorProfile) {
          return res.status(400).json({ status: 'error', message: 'Lengkapi profil lapak terlebih dahulu' });
        }

        // Check collector's concurrent active orders limit
        const activeOrdersCount = await prisma.order.count({
          where: { collectorId: userId, status: { in: ACTIVE_COLLECTOR_STATUSES } }
        });
        if (activeOrdersCount >= collectorProfile.maxConcurrentOrders) {
          return res.status(400).json({
            status: 'error',
            message: `Anda telah mencapai batas maksimal pesanan aktif (${collectorProfile.maxConcurrentOrders} pesanan). Selesaikan pesanan yang ada terlebih dahulu.`
          });
        }

        // Diterima langsung jadi "Dalam Perjalanan" — tak perlu klik terpisah lagi.
        // collectorId ikut di-WHERE: war (null) hanya bisa dimenangkan satu pengepul.
        newStatus = 'ON_THE_WAY';
        const won = await transition(
          ['PENDING'],
          { status: newStatus, collectorId: userId },
          { collectorId: order.collectorId }
        );
        if (!won) return res.status(400).json({ status: 'error', message: 'Order already taken' });
        break;
      }

      case 'arrive': { // Pihak yang menuju lokasi menandai SUDAH SAMPAI (siap timbang)
        if (!isCustomer && !isCollector) return forbidden(res);
        // Terima dari ON_THE_WAY (alur baru) maupun CONFIRMED (backward-compat).
        if (order.status !== 'ON_THE_WAY' && order.status !== 'CONFIRMED') {
          return res.status(400).json({ status: 'error', message: 'Pesanan belum siap untuk ditandai sampai' });
        }
        newStatus = 'IN_PROGRESS';
        if (!(await transition(['ON_THE_WAY', 'CONFIRMED'], { status: newStatus }))) return conflict();
        break;
      }

      case 'validate': { // Pengepul (yang ditetapkan) input berat & harga aktual per kategori.
        if (!isCollector) return forbidden(res, 'Hanya pengepul pesanan ini yang bisa menimbang');
        // Hanya boleh SETELAH ditandai sampai (IN_PROGRESS).
        if (order.status !== 'IN_PROGRESS') {
          return res.status(400).json({ status: 'error', message: 'Invalid state' });
        }

        const { transactionProofUrl, items: validateItems } = req.body;
        if (transactionProofUrl != null && (typeof transactionProofUrl !== 'string' || transactionProofUrl.length > 2048)) {
          return res.status(422).json({ status: 'error', message: 'transactionProofUrl tidak valid' });
        }

        const orderItems = await prisma.orderItem.findMany({ where: { orderId: id }, select: { id: true } });
        const ownItemIds = new Set(orderItems.map((it) => it.id));

        // Normalisasi ke daftar { id, weight, price } — multi-item atau single-item fallback.
        let updates = [];
        if (Array.isArray(validateItems) && validateItems.length > 0) {
          for (const item of validateItems) {
            const weight = toAmount(item && item.actualWeight);
            const price = toAmount(item && item.agreedPrice);
            if (!item || !ownItemIds.has(item.id) || weight === null || price === null) {
              return res.status(422).json({ status: 'error', message: 'Data timbangan tidak valid' });
            }
            updates.push({ id: item.id, weight, price });
          }
        } else {
          const weight = toAmount(req.body.actualWeight);
          const price = toAmount(req.body.agreedPrice);
          if (weight === null || price === null) {
            return res.status(422).json({ status: 'error', message: 'Data timbangan tidak valid' });
          }
          updates = orderItems.length ? [{ id: orderItems[0].id, weight, price }] : [];
          updates.single = { weight, price };
        }

        const totalActualWeight = updates.single
          ? updates.single.weight
          : updates.reduce((sum, u) => sum + u.weight, 0);
        const calculatedTotalPrice = updates.single
          ? updates.single.weight * updates.single.price
          : updates.reduce((sum, u) => sum + u.weight * u.price, 0);

        newStatus = 'AWAITING_CONFIRMATION';
        const ok = await prisma.$transaction(async (tx) => {
          const { count } = await tx.order.updateMany({
            where: { id, status: 'IN_PROGRESS' },
            data: {
              status: newStatus,
              actualWeight: totalActualWeight,
              totalPrice: calculatedTotalPrice,
              ...(updates.single ? { agreedPrice: updates.single.price } : {}),
              transactionProofUrl: transactionProofUrl || null
            }
          });
          if (count !== 1) return false;
          for (const u of updates) {
            await tx.orderItem.update({
              where: { id: u.id },
              data: { actualWeight: u.weight, agreedPrice: u.price }
            });
          }
          return true;
        });
        if (!ok) return conflict();
        break;
      }

      case 'confirm': { // Customer menyetujui harga
        if (!isCustomer) return forbidden(res, 'Hanya customer pesanan ini yang bisa menyetujui');
        if (order.status !== 'AWAITING_CONFIRMATION') {
          return res.status(400).json({ status: 'error', message: 'Invalid state' });
        }
        newStatus = 'COMPLETED';

        const ok = await prisma.$transaction(async (tx) => {
          const { count } = await tx.order.updateMany({
            where: { id, status: 'AWAITING_CONFIRMATION' },
            data: { status: newStatus }
          });
          if (count !== 1) return false;

          const updatedOrder = await tx.order.findUnique({
            where: { id },
            include: { items: { include: { category: true } } }
          });

          // Generate Digital Receipt
          const receiptItems = updatedOrder.items.map(item => ({
            category: item.category.name,
            estimatedWeight: item.estimatedWeight,
            actualWeight: item.actualWeight,
            agreedPrice: item.agreedPrice,
            notes: item.notes,
            total: (item.actualWeight || 0) * (item.agreedPrice || 0)
          }));

          await tx.receipt.create({
            data: {
              orderId: order.id,
              detailsJson: {
                customer: order.customerId,
                collector: order.collectorId,
                total: updatedOrder.totalPrice ?? (updatedOrder.actualWeight || 0) * (updatedOrder.agreedPrice || 0),
                transactionProofUrl: updatedOrder.transactionProofUrl,
                items: receiptItems.length > 0 ? receiptItems : undefined
              }
            }
          });
          return true;
        });
        if (!ok) return conflict();
        break;
      }

      case 'reject': // Collector menolak — order tetap PENDING, hilang dari antrean collector ini
        await prisma.orderCollector.updateMany({
          where: { orderId: id, collectorId: userId },
          data: { status: 'rejected' }
        });
        return res.status(200).json({
          status: 'success',
          data: { orderId: id, status: order.status, rejected: true }
        });

      case 'cancel': { // Customer (atau collector terkait) membatalkan order
        const cancellable = ['PENDING', 'CONFIRMED', 'ON_THE_WAY', 'IN_PROGRESS'];
        if (!cancellable.includes(order.status)) {
          return res.status(400).json({ status: 'error', message: 'Pesanan tidak bisa dibatalkan pada status ini' });
        }
        if (!isCustomer && !isCollector) {
          return forbidden(res, 'Anda tidak berhak membatalkan pesanan ini');
        }
        newStatus = 'CANCELLED';
        if (!(await transition(cancellable, { status: newStatus }))) return conflict();
        break;
      }

      default:
        return res.status(400).json({ status: 'error', message: 'Invalid action' });
    }

    // Broadcast status update ke room order (customer & collector yang join order:id)
    io.to(`order:${order.id}`).emit('order_status_update', { orderId: order.id, status: newStatus });

    // Order keluar dari pool PENDING (diambil collector / dibatalkan customer) →
    // bersihkan antrean SEMUA pengepul yang sempat dinotifikasi, supaya
    // first-come-first-served terlihat instan di dashboard mereka.
    if (action === 'accept' || action === 'cancel') {
      const notified = await prisma.orderCollector.findMany({
        where: { orderId: order.id },
        select: { collectorId: true }
      });
      notified.forEach(c => {
        io.to(`collector:${c.collectorId}`).emit('order_taken', { orderId: order.id });
      });
    }

    res.status(200).json({ status: 'success', data: { orderId: order.id, status: newStatus } });
  } catch (error) {
    next(error);
  }
};

// GET /orders — list order milik user. Filter: status, role (customer|collector), limit.
const getOrders = async (req, res, next) => {
  try {
    const { status, role, limit = 10 } = req.query;

    const where = {};
    if (role === 'collector' && status === 'PENDING') {
      // Antrean PENDING pengepul ini terdiri dari DUA jenis, dibedakan via collectorId:
      // (1) FORWARD/private — customer memilih lapak ini langsung (collectorId sudah
      //     terisi SEJAK dibuat, meski status masih PENDING). Bukan war, cuma utk dia.
      // (2) WAR/broadcast — order belum ber-collector, ditawarkan ke banyak pengepul
      //     sekaligus (rebutan first-come-first-served) via tabel OrderCollector.
      // FE membedakan war/forward dari field collectorId (null = war, terisi = forward).
      where.status = 'PENDING';
      where.OR = [
        {
          collectorId: req.user.id,
          NOT: { orderCollectors: { some: { collectorId: req.user.id, status: 'rejected' } } }
        },
        {
          collectorId: null,
          orderCollectors: { some: { collectorId: req.user.id, status: 'notified' } }
        }
      ];
    } else if (role === 'collector') {
      where.collectorId = req.user.id;
      if (status) where.status = status;
    } else {
      where.customerId = req.user.id;
      if (status) where.status = status;
    }

    const orders = await prisma.order.findMany({
      where,
      include: {
        customer: { select: PUBLIC_USER_SELECT },
        collector: {
          select: { ...PUBLIC_USER_SELECT, collectorProfile: { select: { shopName: true } } }
        },
        category: true,
        items: { include: { category: true } }
      },
      orderBy: { createdAt: 'desc' },
      take: Math.min(Math.max(parseInt(limit, 10) || 10, 1), 100)
    });

    // Lengkapi koordinat (PostGIS) + jarak untuk peta & kartu detail pesanan di FE.
    // Untuk antrean broadcast (order PENDING belum ber-collector), titik pengepul =
    // lokasi collector yang sedang melihat (req.user).
    const viewerCoords = role === 'collector' ? await getUserCoords(req.user.id) : null;
    const enriched = await Promise.all(
      orders.map(async (o) => {
        const [custCoords, ownColl] = await Promise.all([
          getUserCoords(o.customerId),
          o.collectorId ? getUserCoords(o.collectorId) : Promise.resolve(null)
        ]);
        const collCoords = ownColl || viewerCoords;
        return {
          ...o,
          customerLat: custCoords?.lat ?? null,
          customerLng: custCoords?.lng ?? null,
          collectorLat: collCoords?.lat ?? null,
          collectorLng: collCoords?.lng ?? null,
          distanceKm: haversineKm(custCoords, collCoords)
        };
      })
    );

    res.status(200).json({ status: 'success', data: enriched });
  } catch (error) {
    next(error);
  }
};

// Jarak garis lurus (km) antara dua titik {lat,lng} — Haversine. null bila salah satu kosong.
const haversineKm = (a, b) => {
  if (!a || !b) return null;
  const R = 6371;
  const toRad = (d) => (d * Math.PI) / 180;
  const dLat = toRad(b.lat - a.lat);
  const dLng = toRad(b.lng - a.lng);
  const lat1 = toRad(a.lat);
  const lat2 = toRad(b.lat);
  const h =
    Math.sin(dLat / 2) ** 2 +
    Math.cos(lat1) * Math.cos(lat2) * Math.sin(dLng / 2) ** 2;
  return 2 * R * Math.asin(Math.sqrt(h));
};

// Ambil lat/lng dari User.location (PostGIS, kolom Unsupported → harus raw query)
const getUserCoords = async (userId) => {
  if (!userId) return null;
  const rows = await prisma.$queryRaw`
    SELECT ST_Y(location::geometry) AS lat, ST_X(location::geometry) AS lng
    FROM "User" WHERE id = ${userId} AND location IS NOT NULL LIMIT 1
  `;
  return rows[0] ? { lat: Number(rows[0].lat), lng: Number(rows[0].lng) } : null;
};

const getOrderDetails = async (req, res, next) => {
  try {
    const order = await prisma.order.findUnique({
      where: { id: req.params.id },
      include: {
        customer: { select: PUBLIC_USER_SELECT },
        collector: {
          select: { ...PUBLIC_USER_SELECT, collectorProfile: true }
        },
        category: true,
        items: {
          include: { category: true }
        },
        ratings: true
      }
    });

    if (!order) {
      return res.status(200).json({ status: 'success', data: null });
    }
    if (!(await canViewOrder(req.user, order))) {
      return forbidden(res, 'Anda tidak berhak melihat pesanan ini');
    }

    // Koordinat kedua pihak untuk peta rute antar/jemput di FE.
    const [custCoords, collCoords] = await Promise.all([
      getUserCoords(order.customerId),
      getUserCoords(order.collectorId)
    ]);

    res.status(200).json({
      status: 'success',
      data: {
        ...order,
        customerLat: custCoords?.lat ?? null,
        customerLng: custCoords?.lng ?? null,
        collectorLat: collCoords?.lat ?? null,
        collectorLng: collCoords?.lng ?? null
      }
    });
  } catch (error) {
    next(error);
  }
};

module.exports = { createOrder, updateStatus, getOrderDetails, getOrders };
