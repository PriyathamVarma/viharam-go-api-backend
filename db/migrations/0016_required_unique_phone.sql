-- Every account must have a unique phone number. The partial expression index
-- ignores formatting differences and excludes legacy null/empty values so the
-- migration remains safe for existing accounts; the API blocks new access
-- until legacy accounts add a phone number.
CREATE UNIQUE INDEX IF NOT EXISTS users_phone_unique
  ON users (regexp_replace(phone_number, '[^0-9]', '', 'g'))
  WHERE phone_number IS NOT NULL
    AND regexp_replace(phone_number, '[^0-9]', '', 'g') <> '';
