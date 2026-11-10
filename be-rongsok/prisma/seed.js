require('dotenv').config();
const { PrismaClient } = require('@prisma/client');
const bcrypt = require('bcryptjs');
const prisma = new PrismaClient();

const CATEGORIES = [
  {
    name: 'Plastik',
    unit: 'kg',
    description: 'Botol & gelas bening, emberan, botol sabun/oli, terpal, karung, plastik LIT/bening, mainan, & plastik keras',
  },
  {
    name: 'Kertas & Kardus',
    unit: 'kg',
    description: 'Kardus packing, kertas putih/HVS, buku, koran, majalah, & kertas duplex (kotak kemasan)',
  },
  {
    name: 'Logam & Besi',
    unit: 'kg',
    description: 'Besi tua/seng, aluminium (kaleng/wajan), kuningan (keran/gembok), kabel, & tembaga',
  },
  {
    name: 'Kaca & Botol',
    unit: 'kg',
    description: 'Botol kaca utuh (kecap/sirup/bir) & pecahan kaca/beling kiloan',
  },
  {
    name: 'Elektronik',
    unit: 'pcs',
    description: 'Aki bekas, TV/kulkas/mesin cuci rusak, kompresor, HP/laptop mati, & komponen sirkuit',
  },
  {
    name: 'Lain-lain',
    unit: 'liter',
    description: 'Minyak jelantah (minyak goreng bekas)',
  },
];

async function main() {
  // PENGAMAN: seeder ini MENGHAPUS SEMUA DATA. Dulu dipanggil di setiap start
  // container → tiap redeploy/reboot database produksi terhapus. Sekarang hanya
  // jalan bila DB masih kosong, atau dipaksa eksplisit dengan SEED_FORCE=true.
  const existingUsers = await prisma.user.count();
  if (existingUsers > 0 && process.env.SEED_FORCE !== 'true') {
    console.log(`DB sudah berisi ${existingUsers} user — seeder dilewati (set SEED_FORCE=true untuk reset total).`);
    return;
  }

  console.log("Wiping existing database records to perform a clean seed...");
  
  // 1. Wipe data in Foreign Key safe order
  await prisma.rating.deleteMany();
  await prisma.receipt.deleteMany();
  await prisma.orderCollector.deleteMany();
  await prisma.orderItem.deleteMany();
  await prisma.order.deleteMany();
  await prisma.collectorCatalog.deleteMany();
  await prisma.collectorProfile.deleteMany();
  await prisma.user.deleteMany();
  await prisma.wasteCategory.deleteMany();

  console.log("Seeding waste categories...");
  // 2. Seed Waste Categories
  const categoryMap = {};
  for (let i = 0; i < CATEGORIES.length; i++) {
    const c = CATEGORIES[i];
    const createdCat = await prisma.wasteCategory.create({
      data: {
        name: c.name,
        description: c.description,
        unit: c.unit,
        sortOrder: i,
        parentId: null,
      },
    });
    categoryMap[c.name] = createdCat;
  }

  const paperCategory = categoryMap['Kertas & Kardus'];
  const plasticCategory = categoryMap['Plastik'];

  console.log("Generating password hashes...");
  // 3. Generate password hashes
  const salt = await bcrypt.genSalt(10);
  // Password admin dari env; tanpa env → acak (tidak ada lagi admin123 publik).
  const adminPassword = process.env.SEED_ADMIN_PASSWORD || require('crypto').randomBytes(18).toString('base64url');
  if (!process.env.SEED_ADMIN_PASSWORD) {
    console.log('SEED_ADMIN_PASSWORD tidak diset — admin dibuat dengan password acak (reset lewat lupa password).');
  }
  const adminHash = await bcrypt.hash(adminPassword, salt);
  const collectorHash = await bcrypt.hash('collector123', salt);
  const customerHash = await bcrypt.hash('customer123', salt);

  console.log("Seeding Admin account...");
  // 4. Seed Admin
  await prisma.user.create({
    data: {
      name: 'Admin Rongsok',
      email: 'admin@rongsok.in',
      passwordHash: adminHash,
      role: 'ADMIN',
    },
  });

  console.log("Seeding 2 Collector accounts (Open & Closed)...");
  // 5. Seed Collectors (Budi & Eco)
  const budi = await prisma.user.create({
    data: {
      name: 'Budi Pengepul',
      email: 'budi@collector.com',
      passwordHash: collectorHash,
      role: 'COLLECTOR',
      phone: '089876543210',
      isVerified: true,
    },
  });

  const ecoCollector = await prisma.user.create({
    data: {
      name: 'Pak Pengepul Eco',
      email: 'eco.collector@example.com',
      passwordHash: collectorHash,
      role: 'COLLECTOR',
      phone: '089999888777',
      isVerified: true,
    },
  });

  // Set Collector Locations in Yogyakarta
  await prisma.$executeRaw`
    UPDATE "User"
    SET location = ST_SetSRID(ST_MakePoint(110.3695, -7.7956), 4326)::geography
    WHERE id IN (${budi.id}, ${ecoCollector.id})
  `;

  // Seed Profiles
  const budiProfile = await prisma.collectorProfile.create({
    data: {
      userId: budi.id,
      shopName: 'Lapak Budi Yogyakarta',
      description: 'Pengepul Daur Ulang Terpercaya & Cepat',
      radiusKm: 25,
      isOpen: true,
    },
  });

  const ecoProfile = await prisma.collectorProfile.create({
    data: {
      userId: ecoCollector.id,
      shopName: 'Lapak Eco Yogyakarta',
      description: 'Mitra Daur Ulang Khusus Ramah Lingkungan',
      radiusKm: 15,
      isOpen: false, // Testing closed shop toggle
    },
  });

  // Seed Catalogs for both collectors
  const allCategories = Object.values(categoryMap);
  for (const cat of allCategories) {
    await prisma.collectorCatalog.create({
      data: {
        collectorId: budiProfile.id,
        categoryId: cat.id,
        minPrice: 1000,
        maxPrice: 5000,
        isActive: true,
      },
    });

    await prisma.collectorCatalog.create({
      data: {
        collectorId: ecoProfile.id,
        categoryId: cat.id,
        minPrice: 1500,
        maxPrice: 4500,
        isActive: true,
      },
    });
  }

  console.log("Seeding 6 Customer accounts (across different ecoimpact tiers)...");
  // 6. Seed Customers & completed orders targets
  const customerSpecs = [
    {
      name: "Rizky Customer",
      email: "rizky@example.com",
      weightKg: 15.0,
      tier: "Pejuang Daur Ulang (15.0 kg)",
      hasCancelledOrder: true
    },
    {
      name: "Tegar Pemula Pasif",
      email: "tegar.pasif@example.com",
      weightKg: 0,
      tier: "Pemula Hijau (0 kg)",
      hasCancelledOrder: false
    },
    {
      name: "Arif Pemula Hijau",
      email: "arif.pemula@example.com",
      weightKg: 5.5,
      tier: "Pemula Hijau (5.5 kg)",
      hasCancelledOrder: false
    },
    {
      name: "Dinda Pejuang",
      email: "dinda.pejuang@example.com",
      weightKg: 28.3,
      tier: "Pejuang Daur Ulang (28.3 kg)",
      hasCancelledOrder: false
    },
    {
      name: "Gita Pahlawan",
      email: "gita.pahlawan@example.com",
      weightKg: 76.5,
      tier: "Pahlawan Lingkungan (76.5 kg)",
      hasCancelledOrder: false
    },
    {
      name: "Bintang Legenda",
      email: "bintang.legenda@example.com",
      weightKg: 135.2,
      tier: "Legenda Bumi (135.2 kg)",
      hasCancelledOrder: true
    }
  ];

  for (const spec of customerSpecs) {
    const user = await prisma.user.create({
      data: {
        name: spec.name,
        email: spec.email,
        passwordHash: customerHash,
        role: "CUSTOMER",
        phone: `0812${Math.floor(10000000 + Math.random() * 90000000)}`,
        isVerified: true,
        avatarUrl: `https://api.dicebear.com/7.x/adventurer/svg?seed=${encodeURIComponent(spec.name)}`
      }
    });

    // Set coordinates in Yogyakarta with a small random offset (0.5km to 5km)
    const offsetLng = (Math.random() - 0.5) * 0.015;
    const offsetLat = (Math.random() - 0.5) * 0.015;
    await prisma.$executeRaw`
      UPDATE "User"
      SET location = ST_SetSRID(ST_MakePoint(${110.3695 + offsetLng}, ${-7.7956 + offsetLat}), 4326)::geography
      WHERE id = ${user.id}
    `;

    // Seed Completed Orders
    if (spec.weightKg > 0) {
      const order = await prisma.order.create({
        data: {
          customerId: user.id,
          collectorId: budi.id,
          method: "DROPOFF",
          status: "COMPLETED",
          estimatedWeight: spec.weightKg,
          actualWeight: spec.weightKg,
          agreedPrice: 2000,
          totalPrice: spec.weightKg * 2000,
          createdAt: new Date(Date.now() - 86400000 * 2), // 2 days ago
          updatedAt: new Date(Date.now() - 86400000 * 2 + 3600000),
          items: {
            create: [
              {
                categoryId: paperCategory.id,
                estimatedWeight: spec.weightKg,
                actualWeight: spec.weightKg,
                agreedPrice: 2000,
                notes: "Setoran kertas kardus daur ulang"
              }
            ]
          }
        }
      });

      // Digital Receipt
      await prisma.receipt.create({
        data: {
          orderId: order.id,
          detailsJson: {
            customer: user.id,
            collector: budi.id,
            total: spec.weightKg * 2000,
            items: [
              {
                category: paperCategory.name,
                estimatedWeight: spec.weightKg,
                actualWeight: spec.weightKg,
                agreedPrice: 2000,
                total: spec.weightKg * 2000
              }
            ]
          }
        }
      });
    }

    // Seed optional Cancelled Order
    if (spec.hasCancelledOrder) {
      await prisma.order.create({
        data: {
          customerId: user.id,
          collectorId: null,
          method: "PICKUP",
          status: "CANCELLED",
          estimatedWeight: 2.0,
          createdAt: new Date(Date.now() - 3600000), // 1 hour ago
          updatedAt: new Date(),
          items: {
            create: [
              {
                categoryId: plasticCategory.id,
                estimatedWeight: 2.0,
                notes: "Mencoba order jemput lalu batal"
              }
            ]
          }
        }
      });
    }
  }

  console.log("\nDatabase Seeder completed successfully!");
  console.log("=========================================");
  console.log(`- Created ${Object.keys(categoryMap).length} categories.`);
  console.log(`- Created Admin: admin@rongsok.in / admin123`);
  console.log(`- Created 2 Collectors (Budi & Eco) / collector123`);
  console.log(`- Created 6 Customers (Rizky, Tegar, Arif, Dinda, Gita, Bintang) / customer123`);
  console.log("=========================================");
}

main()
  .catch((e) => {
    console.error("Error during database seed:", e);
    process.exit(1);
  })
  .finally(async () => {
    await prisma.$disconnect();
  });
