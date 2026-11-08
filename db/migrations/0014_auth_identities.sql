ALTER TABLE users ADD COLUMN IF NOT EXISTS auth_provider text;
ALTER TABLE users ADD COLUMN IF NOT EXISTS auth_subject text;

CREATE UNIQUE INDEX IF NOT EXISTS users_auth_identity_unique
  ON users (auth_provider, auth_subject)
  WHERE auth_provider IS NOT NULL AND auth_subject IS NOT NULL;
