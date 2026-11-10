const express = require('express');
const router = express.Router();
const upload = require('../middlewares/upload');
const { protect } = require('../middlewares/auth');
const { limiters } = require('../middlewares/rateLimit');
const { uploadImage } = require('../controllers/upload');

// POST /api/v1/upload - expects single file field named 'image'.
// Wajib login. Dulu anonim (demi foto KTP saat register) → siapa pun bisa
// memakai kuota Cloudinary sebagai hosting gratis. Foto KTP kini tidak lagi
// diunggah sama sekali (privasi), jadi tak ada lagi kebutuhan upload anonim.
router.post('/', protect, limiters.upload, upload.single('image'), uploadImage);

module.exports = router;
