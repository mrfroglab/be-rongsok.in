const jwt = require('jsonwebtoken');
const bcrypt = require('bcryptjs');

// JWT_SECRET WAJIB diset. Dulu ada fallback 'secret' — kalau env lupa diset,
// siapa pun bisa memalsukan token (termasuk role ADMIN). Sekarang server
// menolak start (lihat assertJwtSecret di server.js).
const MIN_SECRET_LENGTH = 32;

const getJwtSecret = () => {
  const secret = process.env.JWT_SECRET;
  if (!secret) throw new Error('JWT_SECRET belum diset');
  return secret;
};

const assertJwtSecret = () => {
  const secret = process.env.JWT_SECRET;
  if (!secret || secret.length < MIN_SECRET_LENGTH) {
    throw new Error(
      `JWT_SECRET wajib diset & minimal ${MIN_SECRET_LENGTH} karakter. ` +
      "Generate: node -e \"console.log(require('crypto').randomBytes(48).toString('hex'))\""
    );
  }
};

// Terima user object (atau id string untuk backward-compat) — encode id + role ke JWT
const generateToken = (user) => {
  const payload =
    typeof user === 'object' && user !== null
      ? { id: user.id, role: user.role }
      : { id: user };
  return jwt.sign(payload, getJwtSecret(), {
    algorithm: 'HS256',
    expiresIn: '24h'
  });
};

const verifyToken = (token) => jwt.verify(token, getJwtSecret(), { algorithms: ['HS256'] });

const hashPassword = async (password) => {
  const salt = await bcrypt.genSalt(10);
  return bcrypt.hash(password, salt);
};

const comparePassword = async (password, hash) => {
  return bcrypt.compare(password, hash);
};

module.exports = {
  generateToken,
  verifyToken,
  assertJwtSecret,
  hashPassword,
  comparePassword
};
