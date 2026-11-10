const prisma = require('../config/prisma');
const { z } = require('zod');

const ratingSchema = z.object({
  orderId: z.string(),
  rateeId: z.string(),
  score: z.number().int().min(1).max(5),
  reviewText: z.string().max(2000).optional(),
  photoUrl: z.string().max(2048).optional()
});

const submitRating = async (req, res, next) => {
  try {
    const data = ratingSchema.parse(req.body);

    // Rating hanya untuk order COMPLETED, oleh salah satu pihak, kepada lawan
    // transaksinya. Dulu tanpa cek apa pun → siapa saja bisa menjatuhkan /
    // menggelembungkan avgRating user mana pun.
    const order = await prisma.order.findUnique({
      where: { id: data.orderId },
      select: { customerId: true, collectorId: true, status: true }
    });
    if (!order || order.status !== 'COMPLETED' || !order.collectorId) {
      return res.status(400).json({ status: 'error', message: 'Rating hanya bisa diberikan untuk transaksi yang sudah selesai.' });
    }
    const counterpart =
      order.customerId === req.user.id ? order.collectorId :
      order.collectorId === req.user.id ? order.customerId : null;
    if (!counterpart) {
      return res.status(403).json({ status: 'error', message: 'Anda bukan bagian dari transaksi ini.' });
    }
    if (data.rateeId !== counterpart) {
      return res.status(400).json({ status: 'error', message: 'Penerima rating tidak sesuai dengan transaksi.' });
    }

    // 1. Save rating
    const rating = await prisma.rating.create({
      data: {
        orderId: data.orderId,
        raterId: req.user.id,
        rateeId: data.rateeId,
        score: data.score,
        reviewText: data.reviewText,
        photoUrl: data.photoUrl
      }
    });

    // 2. Update average rating of ratee
    const ratings = await prisma.rating.aggregate({
      where: { rateeId: data.rateeId },
      _avg: { score: true }
    });

    await prisma.user.update({
      where: { id: data.rateeId },
      data: { avgRating: ratings._avg.score }
    });

    res.status(201).json({ status: 'success', data: rating });
  } catch (error) {
    if (error instanceof z.ZodError) {
      return res.status(422).json({ status: 'error', message: 'Validation failed', errors: error.errors });
    }
    // Unique constraint (orderId, raterId) → user sudah pernah menilai order ini
    if (error && error.code === 'P2002') {
      return res.status(409).json({ status: 'error', message: 'Kamu sudah memberi rating untuk transaksi ini.' });
    }
    next(error);
  }
};

const getUserRatings = async (req, res, next) => {
  try {
    const { userId } = req.params;
    const ratings = await prisma.rating.findMany({
      where: { rateeId: userId },
      include: { rater: { select: { id: true, name: true, avatarUrl: true } } },
      orderBy: { createdAt: 'desc' }
    });
    res.status(200).json({ status: 'success', data: ratings });
  } catch (error) {
    next(error);
  }
};

module.exports = { submitRating, getUserRatings };
