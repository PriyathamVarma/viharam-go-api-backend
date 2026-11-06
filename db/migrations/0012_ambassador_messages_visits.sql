-- Messages an ambassador sends to the guests of an assignment. Assignments are
-- JSONB rows in document_records (domain 'ambassadorassignments').
CREATE TABLE IF NOT EXISTS assignment_messages (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  assignment_id uuid NOT NULL REFERENCES document_records(id) ON DELETE CASCADE,
  sender_user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  body text NOT NULL CHECK (char_length(body) BETWEEN 1 AND 1000),
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS assignment_messages_assignment_idx ON assignment_messages (assignment_id, created_at DESC);

-- Host-family visits planned by an ambassador.
CREATE TABLE IF NOT EXISTS ambassador_visits (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  ambassador_user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  host_profile_id uuid NOT NULL REFERENCES host_profiles(id) ON DELETE CASCADE,
  scheduled_for date NOT NULL,
  scheduled_time time,
  notes text NOT NULL DEFAULT '',
  status text NOT NULL DEFAULT 'scheduled' CHECK (status IN ('scheduled','completed','cancelled')),
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS ambassador_visits_owner_idx ON ambassador_visits (ambassador_user_id, scheduled_for);
CREATE INDEX IF NOT EXISTS ambassador_visits_host_idx ON ambassador_visits (host_profile_id, status);
