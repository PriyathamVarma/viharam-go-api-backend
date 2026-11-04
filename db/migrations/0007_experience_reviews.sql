CREATE TABLE IF NOT EXISTS experience_reviews (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  traveler_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  traveler_name text NOT NULL,
  booking_id text NOT NULL UNIQUE,
  subject_type text NOT NULL CHECK (subject_type IN ('experience','host','ambassador')),
  subject_id text NOT NULL,
  ratings jsonb NOT NULL,
  title text NOT NULL,
  comment text NOT NULL,
  photos jsonb NOT NULL DEFAULT '[]'::jsonb,
  status text NOT NULL DEFAULT 'approved' CHECK (status IN ('pending','approved','rejected','flagged')),
  moderation_note text,
  moderated_by text,
  moderated_at timestamptz,
  reports jsonb NOT NULL DEFAULT '[]'::jsonb,
  helpful_votes integer NOT NULL DEFAULT 0 CHECK (helpful_votes >= 0),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS experience_reviews_subject_idx ON experience_reviews (subject_type, subject_id, status, created_at DESC);
