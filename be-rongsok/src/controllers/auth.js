const prisma = require('../config/prisma');
const { hashPassword, comparePassword, generateToken } = require('../utils/auth');
const { z } = require('zod');
const crypto = require('crypto');
const { sendEmail } = require('../utils/email');
const { isPlausibleNik, resolveKyc, isKycEnforced } = require('../utils/kyc');

// Foto KTP TIDAK disimpan (privasi) — ktpUrl dari client diabaikan.
// kycAttestation = token bertanda tangan dari OCR server (lihat utils/kyc.js).
const nikField = z.string()
  .regex(/^\d{16}$/, 'NIK harus 16 digit')
  .refine(isPlausibleNik, 'NIK tidak valid');

const registerSchema = z.object({
  name: z.string().min(2),
  email: z.string().email(),
  password: z.string().min(8),
  role: z.enum(['CUSTOMER', 'COLLECTOR']),
  phone: z.string().optional(),
  avatarUrl: z.string().optional(),
  addressText: z.string().max(500).optional(),
  // KYC (opsional — akun tetap bisa dibuat walau verifikasi belum berhasil)
  nik: nikField.optional(),
  ktpName: z.string().min(2).max(100).optional(),
  kycAttestation: z.string().max(2048).optional()
});

const loginSchema = z.object({
  email: z.string().email(),
  password: z.string()
});

const updateMeSchema = z.object({
  name: z.string().min(2).optional(),
  phone: z.string().optional(),
  avatarUrl: z.string().optional(),
  addressText: z.string().max(500).optional(),
  lat: z.number().min(-90).max(90).optional(),
  lng: z.number().min(-180).max(180).optional(),
  // KYC (opsional) — dipakai jalur verifikasi in-app /profile/verify di FE
  nik: nikField.optional(),
  ktpName: z.string().min(2).max(100).optional(),
  kycAttestation: z.string().max(2048).optional()
});

const register = async (req, res, next) => {
  try {
    const data = registerSchema.parse(req.body);

    const existingUser = await prisma.user.findUnique({ where: { email: data.email } });
    if (existingUser) {
      return res.status(400).json({ status: 'error', message: 'Email already registered' });
    }

    // KYC anti wash-trading: 1 KTP = 1 akun = 1 role.
    // NIK adalah kunci unik utama. Nama dicek sebagai lapis tambahan (case-insensitive,
    // dibatasi ke akun yang sudah punya NIK terdaftar agar tidak false-positive nama umum).
    const kyc = resolveKyc(data);
    if (kyc.nik) {
      const dupNik = await prisma.user.findUnique({ where: { nik: kyc.nik } });
      if (dupNik) {
        return res.status(409).json({ status: 'error', message: 'Identitas sudah terdaftar.' });
      }
      // Cek nama kembar hanya di mode legacy (NIK tak terbukti). Kalau NIK sudah
      // dibuktikan via attestation, nama sama ≠ orang sama (banyak "MUHAMMAD RIZKI").
      const refName = isKycEnforced() ? '' : (kyc.ktpName || data.name || '').trim();
      if (refName) {
        const dupName = await prisma.user.findFirst({
          where: {
            nik: { not: null },
            ktpName: { equals: refName, mode: 'insensitive' }
          }
        });
        if (dupName) {
          return res.status(409).json({ status: 'error', message: 'Identitas sudah terdaftar.' });
        }
      }
    }

    const hashedPassword = await hashPassword(data.password);
    // Terverifikasi hanya bila NIK terbukti dari OCR server (attestation).
    // Foto KTP sengaja tidak disimpan demi privasi.
    const user = await prisma.user.create({
      data: {
        name: data.name,
        email: data.email,
        passwordHash: hashedPassword,
        role: data.role,
        phone: data.phone,
        avatarUrl: data.avatarUrl,
        addressText: data.addressText || null,
        nik: kyc.nik,
        ktpName: kyc.ktpName,
        isVerified: kyc.verified
      }
    });

    const token = generateToken(user);
    res.status(201).json({
      status: 'success',
      data: {
        access_token: token,
        user: { id: user.id, name: user.name, email: user.email, phone: user.phone, role: user.role, avatarUrl: user.avatarUrl, isVerified: user.isVerified }
      }
    });
  } catch (error) {
    if (error instanceof z.ZodError) {
      return res.status(422).json({ status: 'error', message: 'Validation failed', errors: error.errors });
    }
    if (error && error.code === 'P2002') {
      // Unique constraint (email / NIK) kena saat request bersamaan
      return res.status(409).json({ status: 'error', message: 'Identitas atau email sudah terdaftar.' });
    }
    next(error);
  }
};

const login = async (req, res, next) => {
  try {
    const data = loginSchema.parse(req.body);

    const user = await prisma.user.findUnique({ where: { email: data.email } });
    if (!user || !(await comparePassword(data.password, user.passwordHash))) {
      return res.status(401).json({ status: 'error', message: 'Invalid email or password' });
    }

    const token = generateToken(user);
    res.status(200).json({
      status: 'success',
      data: {
        access_token: token,
        user: { id: user.id, name: user.name, email: user.email, phone: user.phone, role: user.role, avatarUrl: user.avatarUrl, isVerified: user.isVerified }
      }
    });
  } catch (error) {
    if (error instanceof z.ZodError) {
      return res.status(422).json({ status: 'error', message: 'Validation failed', errors: error.errors });
    }
    next(error);
  }
};

const me = async (req, res, next) => {
  try {
    // Pakai raw query untuk parse lokasi PostGIS jadi lat/lng
    const rows = await prisma.$queryRaw`
      SELECT
        id, name, email, role, "avgRating", "avatarUrl", phone, "createdAt",
        "isVerified", nik, "ktpName", "addressText",
        ST_Y(location::geometry) AS lat,
        ST_X(location::geometry) AS lng
      FROM "User"
      WHERE id = ${req.user.id}
      LIMIT 1
    `;
    res.status(200).json({ status: 'success', data: rows[0] || null });
  } catch (error) {
    next(error);
  }
};

// PATCH /auth/me — update profil user + lokasi (dipakai customer & collector di FE)
const updateMe = async (req, res, next) => {
  try {
    const data = updateMeSchema.parse(req.body);
    const userId = req.user.id;

    const updateData = {};
    if (data.name !== undefined) updateData.name = data.name;
    if (data.phone !== undefined) updateData.phone = data.phone;
    if (data.avatarUrl !== undefined) updateData.avatarUrl = data.avatarUrl;
    if (data.addressText !== undefined) updateData.addressText = data.addressText;

    // KYC: simpan NIK / nama-KTP / URL-KTP + tandai verified.
    // Cek duplikat identitas (anti wash-trading) sama seperti saat register,
    // tapi kecualikan akun sendiri.
    if (data.nik !== undefined) {
      const kyc = resolveKyc(data);
      if (!kyc.verified) {
        return res.status(422).json({
          status: 'error',
          message: 'Verifikasi KTP gagal. Foto ulang KTP dengan jelas lalu coba lagi.'
        });
      }
      const current = await prisma.user.findUnique({ where: { id: userId }, select: { nik: true } });
      if (current && current.nik && current.nik !== kyc.nik) {
        return res.status(409).json({ status: 'error', message: 'Akun ini sudah terverifikasi dengan identitas lain.' });
      }
      const dupNik = await prisma.user.findFirst({
        where: { nik: kyc.nik, id: { not: userId } }
      });
      if (dupNik) {
        return res.status(409).json({ status: 'error', message: 'Identitas sudah terdaftar.' });
      }
      const refName = isKycEnforced() ? '' : (kyc.ktpName || '').trim();
      if (refName) {
        const dupName = await prisma.user.findFirst({
          where: {
            id: { not: userId },
            nik: { not: null },
            ktpName: { equals: refName, mode: 'insensitive' }
          }
        });
        if (dupName) {
          return res.status(409).json({ status: 'error', message: 'Identitas sudah terdaftar.' });
        }
      }
      updateData.nik = kyc.nik;
      updateData.ktpName = kyc.ktpName;
      updateData.isVerified = true;
    }

    if (Object.keys(updateData).length > 0) {
      await prisma.user.update({ where: { id: userId }, data: updateData });
    }

    // Update lokasi PostGIS (kolom Unsupported, harus raw)
    if (data.lat !== undefined && data.lng !== undefined) {
      await prisma.$executeRaw`
        UPDATE "User"
        SET location = ST_SetSRID(ST_MakePoint(${data.lng}, ${data.lat}), 4326)::geography
        WHERE id = ${userId}
      `;
    }

    const rows = await prisma.$queryRaw`
      SELECT
        id, name, email, role, "avgRating", "avatarUrl", phone, "createdAt",
        "isVerified", nik, "ktpName", "addressText",
        ST_Y(location::geometry) AS lat,
        ST_X(location::geometry) AS lng
      FROM "User"
      WHERE id = ${userId}
      LIMIT 1
    `;

    res.status(200).json({ status: 'success', data: rows[0] || null });
  } catch (error) {
    if (error instanceof z.ZodError) {
      return res.status(422).json({ status: 'error', message: 'Validation failed', errors: error.errors });
    }
    if (error && error.code === 'P2002') {
      // Unique constraint (email / NIK) kena saat request bersamaan
      return res.status(409).json({ status: 'error', message: 'Identitas atau email sudah terdaftar.' });
    }
    next(error);
  }
};

const hashResetToken = (token) => crypto.createHash('sha256').update(token).digest('hex');

const forgotPasswordSchema = z.object({
  email: z.string().email(),
});

const resetPasswordSchema = z.object({
  token: z.string(),
  password: z.string().min(8),
});

const forgotPassword = async (req, res, next) => {
  try {
    const data = forgotPasswordSchema.parse(req.body);
    const user = await prisma.user.findUnique({ where: { email: data.email } });

    // Respons SAMA untuk email terdaftar / tidak — cegah enumerasi akun.
    const genericResponse = { status: 'success', message: 'Jika email terdaftar, tautan pemulihan kata sandi telah dikirim.' };
    if (!user) {
      return res.status(200).json(genericResponse);
    }

    // Token mentah hanya dikirim via email; DB menyimpan hash SHA-256-nya,
    // jadi bocornya DB tidak langsung memberi akses reset password.
    const token = crypto.randomBytes(32).toString('hex');
    const expires = new Date(Date.now() + 15 * 60 * 1000); // 15 menit

    await prisma.user.update({
      where: { id: user.id },
      data: {
        resetPasswordToken: hashResetToken(token),
        resetPasswordExpires: expires,
      },
    });

    const resetUrl = `${process.env.FRONTEND_URL || 'http://localhost:3000'}/reset-password?token=${token}`;
    const html = `
      <div style="font-family: sans-serif; padding: 20px; max-width: 600px; margin: 0 auto; border: 1px solid #e2e8f0; border-radius: 12px; background-color: #ffffff;">
        <h2 style="color: #111827; font-size: 20px; font-weight: 800; margin-bottom: 12px;">Atur Ulang Kata Sandi Rongsok.in</h2>
        <p style="color: #4b5563; font-size: 14px; line-height: 1.6; margin-bottom: 24px;">Halo,</p>
        <p style="color: #4b5563; font-size: 14px; line-height: 1.6; margin-bottom: 24px;">Kami menerima permintaan untuk mengatur ulang kata sandi akun Rongsok.in Anda. Silakan klik tombol di bawah ini untuk melanjutkan:</p>
        <div style="text-align: center; margin: 32px 0;">
          <a href="${resetUrl}" style="background-color: #10B981; color: white; padding: 12px 28px; text-decoration: none; border-radius: 8px; font-size: 14px; font-weight: bold; display: inline-block;">Atur Ulang Kata Sandi</a>
        </div>
        <p style="color: #6b7280; font-size: 12px; line-height: 1.6; margin-top: 24px;">Tautan ini hanya berlaku selama 15 menit. Jika Anda tidak merasa melakukan permintaan ini, silakan abaikan email ini secara aman.</p>
        <hr style="border: none; border-top: 1px solid #e5e7eb; margin: 24px 0;" />
        <p style="font-size: 11px; color: #9ca3af;">Tim Rongsok.in Marketplace</p>
      </div>
    `;

    await sendEmail({ to: user.email, subject: 'Atur Ulang Kata Sandi Rongsok.in', html });

    res.status(200).json(genericResponse);
  } catch (error) {
    if (error instanceof z.ZodError) {
      return res.status(422).json({ status: 'error', message: 'Validation failed', errors: error.errors });
    }
    next(error);
  }
};

const resetPassword = async (req, res, next) => {
  try {
    const data = resetPasswordSchema.parse(req.body);

    const user = await prisma.user.findFirst({
      where: {
        resetPasswordToken: hashResetToken(data.token),
        resetPasswordExpires: { gte: new Date() },
      },
    });

    if (!user) {
      return res.status(400).json({ status: 'error', message: 'Tautan tidak valid atau telah kedaluwarsa.' });
    }

    const hashedPassword = await hashPassword(data.password);

    await prisma.user.update({
      where: { id: user.id },
      data: {
        passwordHash: hashedPassword,
        resetPasswordToken: null,
        resetPasswordExpires: null,
      },
    });

    res.status(200).json({ status: 'success', message: 'Kata sandi berhasil diperbarui.' });
  } catch (error) {
    if (error instanceof z.ZodError) {
      return res.status(422).json({ status: 'error', message: 'Validation failed', errors: error.errors });
    }
    next(error);
  }
};

module.exports = { register, login, me, updateMe, forgotPassword, resetPassword };
