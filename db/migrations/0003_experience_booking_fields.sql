ALTER TABLE experience_bookings ADD COLUMN IF NOT EXISTS refund_reason text;
ALTER TABLE experience_bookings ADD COLUMN IF NOT EXISTS rescheduled_at timestamptz;
ALTER TABLE experience_bookings ADD COLUMN IF NOT EXISTS rescheduled_by uuid REFERENCES users(id);
ALTER TABLE experience_bookings ADD COLUMN IF NOT EXISTS original_slot_id uuid REFERENCES experience_slots(id);
ALTER TABLE experience_bookings ADD COLUMN IF NOT EXISTS original_date date;
