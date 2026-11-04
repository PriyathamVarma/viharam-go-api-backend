CREATE TABLE IF NOT EXISTS document_records (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  domain text NOT NULL,
  owner_id uuid REFERENCES users(id) ON DELETE SET NULL,
  payload jsonb NOT NULL DEFAULT '{}'::jsonb,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS document_records_domain_created_idx ON document_records (domain, created_at DESC);
CREATE INDEX IF NOT EXISTS document_records_owner_domain_idx ON document_records (owner_id, domain, created_at DESC);
CREATE INDEX IF NOT EXISTS document_records_payload_gin_idx ON document_records USING gin (payload);
