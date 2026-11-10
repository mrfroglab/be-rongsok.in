const express = require('express');
const router = express.Router();
const { register, login, me, updateMe, forgotPassword, resetPassword } = require('../controllers/auth');
const { protect } = require('../middlewares/auth');
const { limiters } = require('../middlewares/rateLimit');

router.post('/register', limiters.register, register);
router.post('/login', limiters.loginIp, limiters.loginEmail, login);
router.post('/forgot-password', limiters.forgotIp, limiters.forgotEmail, forgotPassword);
router.post('/reset-password', limiters.resetPassword, resetPassword);
router.get('/me', protect, me);
router.patch('/me', protect, limiters.kyc, updateMe);

module.exports = router;
