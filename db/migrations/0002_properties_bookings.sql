CREATE TABLE IF NOT EXISTS properties (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  host_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  name text NOT NULL,
  description text NOT NULL,
  type text NOT NULL DEFAULT 'hotel' CHECK (type IN ('hotel','retreat','homestay','villa','resort')),
  status text NOT NULL DEFAULT 'draft' CHECK (status IN ('active','inactive','draft','archived')),
  city text NOT NULL,
  country text NOT NULL,
  address text,
  latitude double precision,
  longitude double precision,
  price_per_night_paise bigint NOT NULL CHECK (price_per_night_paise >= 0),
  max_guests integer NOT NULL DEFAULT 2 CHECK (max_guests > 0),
  bedrooms integer,
  bathrooms integer,
  image text,
  images jsonb NOT NULL DEFAULT '[]'::jsonb,
  amenities jsonb NOT NULL DEFAULT '[]'::jsonb,
  rating numeric(3,2) NOT NULL DEFAULT 0,
  review_count integer NOT NULL DEFAULT 0,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS properties_city_status_idx ON properties (lower(city), status);
CREATE INDEX IF NOT EXISTS properties_host_status_idx ON properties (host_id, status);

CREATE TABLE IF NOT EXISTS property_bookings (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  traveler_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  property_id uuid NOT NULL REFERENCES properties(id) ON DELETE RESTRICT,
  host_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  check_in date NOT NULL,
  check_out date NOT NULL,
  guests integer NOT NULL CHECK (guests > 0),
  total_price_paise bigint NOT NULL CHECK (total_price_paise >= 0),
  price_per_night_paise bigint NOT NULL CHECK (price_per_night_paise >= 0),
  nights integer NOT NULL CHECK (nights > 0),
  status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','confirmed','cancelled','completed')),
  notes text,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  CHECK (check_out > check_in)
);
CREATE INDEX IF NOT EXISTS property_bookings_scope_idx ON property_bookings (traveler_id, status, created_at DESC);
CREATE INDEX IF NOT EXISTS property_bookings_property_dates_idx ON property_bookings (property_id, check_in, check_out);
