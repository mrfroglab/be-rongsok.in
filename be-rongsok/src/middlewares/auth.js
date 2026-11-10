const prisma = require('../config/prisma');
const { verifyToken } = require('../utils/auth');

const protect = async (req, res, next) => {
  const authHeader = req.headers.authorization;
  if (!authHeader || !authHeader.startsWith('Bearer ')) {
    return res.status(401).json({ status: 'error', message: 'Unauthorized, no token' });
  }

  const token = authHeader.split(' ')[1];
  try {
    const decoded = verifyToken(token);

    // Verify user still exists in the database. Role diambil dari DB (bukan dari
    // JWT) supaya perubahan role langsung berlaku & token lama tanpa role tetap benar.
    const user = await prisma.user.findUnique({
      where: { id: decoded.id },
      select: { id: true, role: true }
    });

    if (!user) {
      return res.status(401).json({ status: 'error', message: 'Unauthorized, user no longer exists' });
    }

    req.user = { id: user.id, role: user.role };
    next();
  } catch (error) {
    return res.status(401).json({ status: 'error', message: 'Unauthorized, invalid token' });
  }
};

// Optional auth — pasang req.user kalau ada token valid, tapi JANGAN tolak kalau tidak ada.
// Dipakai di /upload supaya foto KTP saat REGISTER (user belum punya token) tetap bisa diunggah.
const optionalAuth = (req, res, next) => {
  const authHeader = req.headers.authorization;
  if (authHeader && authHeader.startsWith('Bearer ')) {
    try {
      req.user = verifyToken(authHeader.split(' ')[1]);
    } catch (error) {
      // token tidak valid → perlakukan sebagai anonim, jangan blokir
    }
  }
  next();
};

// Role guard — JWT sekarang sudah include role (lihat utils/auth.generateToken)
const authorize = (...roles) => {
  return (req, res, next) => {
    if (!req.user || !req.user.role || !roles.includes(req.user.role)) {
      return res.status(403).json({
        status: 'error',
        message: `Forbidden — butuh role: ${roles.join(', ')}`
      });
    }
    next();
  };
};

module.exports = { protect, authorize, optionalAuth };
