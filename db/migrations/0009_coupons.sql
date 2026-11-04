CREATE TABLE IF NOT EXISTS coupons (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  code text NOT NULL UNIQUE,
  description text NOT NULL,
  type text NOT NULL CHECK (type IN ('percentage','fixed')),
  value numeric(12,2) NOT NULL CHECK (value >= 0),
  min_booking_amount numeric(12,2) NOT NULL DEFAULT 0,
  max_discount_amount numeric(12,2),
  max_uses integer NOT NULL DEFAULT 0 CHECK (max_uses >= 0),
  used_count integer NOT NULL DEFAULT 0 CHECK (used_count >= 0),
  applicable_experience_ids jsonb NOT NULL DEFAULT '[]'::jsonb,
  valid_from timestamptz NOT NULL,
  valid_to timestamptz NOT NULL,
  is_active boolean NOT NULL DEFAULT true,
  usages jsonb NOT NULL DEFAULT '[]'::jsonb,
  created_by uuid REFERENCES users(id) ON DELETE SET NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  CHECK (valid_to >= valid_from)
);
CREATE INDEX IF NOT EXISTS coupons_active_window_idx ON coupons (is_active, valid_from, valid_to);
