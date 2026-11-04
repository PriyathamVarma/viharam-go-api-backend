CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE IF NOT EXISTS users (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  name text NOT NULL,
  email text NOT NULL,
  password_hash text NOT NULL,
  role text NOT NULL DEFAULT 'Traveller' CHECK (role IN ('Traveller','Host','Ambassador','Admin','SuperAdmin')),
  phone_number text,
  photo_url text,
  bio text,
  is_verified boolean NOT NULL DEFAULT false,
  status text NOT NULL DEFAULT 'active' CHECK (status IN ('active','suspended','deleted')),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS users_email_lower_unique ON users (lower(email));

CREATE TABLE IF NOT EXISTS host_profiles (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id uuid NOT NULL UNIQUE REFERENCES users(id) ON DELETE CASCADE,
  display_name text NOT NULL,
  host_type text NOT NULL,
  description text NOT NULL DEFAULT '',
  phone text NOT NULL,
  address jsonb NOT NULL DEFAULT '{}'::jsonb,
  kyc jsonb NOT NULL DEFAULT '{}'::jsonb,
  bank_details jsonb NOT NULL DEFAULT '{}'::jsonb,
  documents jsonb NOT NULL DEFAULT '[]'::jsonb,
  safety_checklist jsonb NOT NULL DEFAULT '[]'::jsonb,
  agreement_accepted boolean NOT NULL DEFAULT false,
  verification_status text NOT NULL DEFAULT 'pending' CHECK (verification_status IN ('pending','under_review','verified','suspended','rejected')),
  onboarding_step integer NOT NULL DEFAULT 0,
  onboarding_completed boolean NOT NULL DEFAULT false,
  rating numeric(3,2) NOT NULL DEFAULT 0,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS experiences (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  host_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  slug text NOT NULL UNIQUE,
  title text NOT NULL,
  short_description text NOT NULL DEFAULT '',
  description text NOT NULL,
  category text NOT NULL,
  city text NOT NULL,
  state text NOT NULL,
  village text,
  price_per_person_paise bigint NOT NULL CHECK (price_per_person_paise >= 0),
  duration_minutes integer NOT NULL CHECK (duration_minutes > 0),
  min_group_size integer NOT NULL DEFAULT 1 CHECK (min_group_size > 0),
  max_group_size integer NOT NULL CHECK (max_group_size >= min_group_size),
  content jsonb NOT NULL DEFAULT '{}'::jsonb,
  status text NOT NULL DEFAULT 'draft' CHECK (status IN ('draft','pending_review','published','archived')),
  is_featured boolean NOT NULL DEFAULT false,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS experiences_catalogue_idx ON experiences (status, is_featured DESC, updated_at DESC);
CREATE INDEX IF NOT EXISTS experiences_location_idx ON experiences (lower(city), lower(state));

CREATE TABLE IF NOT EXISTS experience_slots (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  experience_id uuid NOT NULL REFERENCES experiences(id) ON DELETE CASCADE,
  date date NOT NULL,
  start_time time NOT NULL,
  end_time time NOT NULL,
  capacity integer NOT NULL CHECK (capacity > 0),
  booked_count integer NOT NULL DEFAULT 0 CHECK (booked_count >= 0),
  locked_count integer NOT NULL DEFAULT 0 CHECK (locked_count >= 0),
  is_blocked boolean NOT NULL DEFAULT false,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (experience_id, date, start_time)
);

CREATE TABLE IF NOT EXISTS booking_locks (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  lock_id uuid NOT NULL UNIQUE DEFAULT gen_random_uuid(),
  experience_id uuid NOT NULL REFERENCES experiences(id),
  slot_id uuid NOT NULL REFERENCES experience_slots(id),
  traveler_id uuid REFERENCES users(id),
  guests integer NOT NULL CHECK (guests > 0),
  expires_at timestamptz NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS booking_locks_expiry_idx ON booking_locks (expires_at);

CREATE TABLE IF NOT EXISTS experience_bookings (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  booking_ref text NOT NULL UNIQUE,
  experience_id uuid NOT NULL REFERENCES experiences(id),
  slot_id uuid NOT NULL REFERENCES experience_slots(id),
  traveler_id uuid NOT NULL REFERENCES users(id),
  traveler_name text NOT NULL,
  traveler_email text NOT NULL,
  traveler_phone text NOT NULL,
  emergency_contact text NOT NULL,
  special_requests text,
  guests integer NOT NULL CHECK (guests > 0),
  price_per_person_paise bigint NOT NULL CHECK (price_per_person_paise >= 0),
  subtotal_paise bigint NOT NULL CHECK (subtotal_paise >= 0),
  service_fee_paise bigint NOT NULL CHECK (service_fee_paise >= 0),
  total_paise bigint NOT NULL CHECK (total_paise >= 0),
  status text NOT NULL DEFAULT 'pending_payment',
  payment_status text NOT NULL DEFAULT 'pending',
  status_history jsonb NOT NULL DEFAULT '[]'::jsonb,
  ambassador_id uuid REFERENCES users(id),
  cancellation_reason text,
  refund_amount_paise bigint,
  booked_at timestamptz NOT NULL DEFAULT now(),
  cancelled_at timestamptz,
  completed_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS experience_bookings_traveler_idx ON experience_bookings (traveler_id, created_at DESC);
CREATE INDEX IF NOT EXISTS experience_bookings_status_idx ON experience_bookings (status, booked_at DESC);

CREATE TABLE IF NOT EXISTS audit_logs (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  actor_id uuid REFERENCES users(id),
  action text NOT NULL,
  entity_type text NOT NULL,
  entity_id uuid,
  metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS audit_logs_entity_idx ON audit_logs (entity_type, entity_id, created_at DESC);
