CREATE TABLE IF NOT EXISTS referrals (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  referrer_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  referee_id uuid REFERENCES users(id) ON DELETE RESTRICT,
  referral_code text NOT NULL UNIQUE,
  status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','completed','expired')),
  referrer_reward_amount numeric(12,2) NOT NULL DEFAULT 500,
  referee_discount_amount numeric(12,2) NOT NULL DEFAULT 200,
  expires_at timestamptz NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS referrals_one_pending_referrer_idx ON referrals (referrer_id) WHERE status='pending';
CREATE INDEX IF NOT EXISTS referrals_user_idx ON referrals (referrer_id, referee_id, created_at DESC);

CREATE TABLE IF NOT EXISTS loyalty_profiles (
  user_id uuid PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  user_email text NOT NULL,
  total_xp integer NOT NULL DEFAULT 0 CHECK (total_xp >= 0),
  level text NOT NULL DEFAULT 'Explorer',
  transactions jsonb NOT NULL DEFAULT '[]'::jsonb,
  badges jsonb NOT NULL DEFAULT '[]'::jsonb,
  trips_completed integer NOT NULL DEFAULT 0,
  reviews_given integer NOT NULL DEFAULT 0,
  referrals_converted integer NOT NULL DEFAULT 0,
  total_spent numeric(12,2) NOT NULL DEFAULT 0,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);
