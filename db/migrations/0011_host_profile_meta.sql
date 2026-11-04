ALTER TABLE host_profiles ADD COLUMN IF NOT EXISTS profile_meta jsonb NOT NULL DEFAULT '{}'::jsonb;
