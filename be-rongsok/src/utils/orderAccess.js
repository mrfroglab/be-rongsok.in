const prisma = require('../config/prisma');

// Siapa yang boleh melihat sebuah order (REST detail & room socket order:<id>):
// - admin
// - customer pemilik order
// - pengepul yang sudah ditetapkan (forward, atau yang menerima war)
// - pengepul kandidat war (order PENDING tanpa collector) yang dinotifikasi & belum menolak
//
// `order` minimal berisi { id, customerId, collectorId, status }.
const canViewOrder = async (user, order) => {
  if (!user || !order) return false;
  if (user.role === 'ADMIN') return true;
  if (order.customerId === user.id || order.collectorId === user.id) return true;

  if (order.status === 'PENDING' && !order.collectorId) {
    const candidate = await prisma.orderCollector.findFirst({
      where: { orderId: order.id, collectorId: user.id, status: 'notified' },
      select: { id: true }
    });
    return Boolean(candidate);
  }
  return false;
};

// Pihak transaksi yang boleh mengirim live location: customer atau pengepul yang ditetapkan.
const isOrderParty = (userId, order) =>
  Boolean(order) && (order.customerId === userId || order.collectorId === userId);

module.exports = { canViewOrder, isOrderParty };
