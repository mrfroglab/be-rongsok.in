-- updatedAt Order otomatis diperbarui pada setiap UPDATE (dulu tugas Prisma @updatedAt).
-- Nilai eksplisit dari aplikasi tetap dihormati; trigger hanya mengisi bila kolom tidak diubah.
CREATE OR REPLACE FUNCTION order_touch_updated_at() RETURNS trigger AS $$
BEGIN
  IF NEW."updatedAt" IS NOT DISTINCT FROM OLD."updatedAt" THEN
    NEW."updatedAt" := (now() AT TIME ZONE 'UTC')::timestamp(3);
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER "Order_touch_updated_at"
  BEFORE UPDATE ON "Order"
  FOR EACH ROW EXECUTE FUNCTION order_touch_updated_at();
