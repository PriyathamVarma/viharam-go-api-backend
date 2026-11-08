CREATE TABLE IF NOT EXISTS account_deletion_requests (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','rejected','completed')),
  requested_at timestamptz NOT NULL DEFAULT now(),
  eligible_at timestamptz NOT NULL DEFAULT (now() + interval '7 days'),
  reviewed_at timestamptz,
  reviewed_by uuid REFERENCES users(id) ON DELETE SET NULL,
  completed_at timestamptz,
  reason text
);

CREATE UNIQUE INDEX IF NOT EXISTS account_deletion_one_pending_idx
  ON account_deletion_requests (user_id) WHERE status = 'pending';
CREATE INDEX IF NOT EXISTS account_deletion_review_idx
  ON account_deletion_requests (status, eligible_at, requested_at);
