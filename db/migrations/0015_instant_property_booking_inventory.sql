ALTER TABLE property_bookings
  ADD COLUMN IF NOT EXISTS rooms_required integer NOT NULL DEFAULT 1 CHECK (rooms_required > 0);

CREATE INDEX IF NOT EXISTS property_bookings_inventory_idx
  ON property_bookings (property_id, check_in, check_out, status);
