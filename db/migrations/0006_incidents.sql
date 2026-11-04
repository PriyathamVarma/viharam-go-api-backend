CREATE TABLE IF NOT EXISTS incidents (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  reporter_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  reporter_name text NOT NULL,
  booking_id text,
  experience_id text,
  type text NOT NULL,
  severity text NOT NULL,
  description text NOT NULL,
  location jsonb NOT NULL DEFAULT '{}'::jsonb,
  occurred_at timestamptz NOT NULL,
  photos jsonb NOT NULL DEFAULT '[]'::jsonb,
  status text NOT NULL DEFAULT 'open' CHECK (status IN ('open','investigating','resolved','closed')),
  assigned_to text,
  resolution text,
  resolved_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS incidents_status_created_idx ON incidents (status, created_at DESC);
CREATE INDEX IF NOT EXISTS incidents_reporter_idx ON incidents (reporter_id, created_at DESC);
