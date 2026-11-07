-- Host-created experiences are public immediately. Backfill records created
-- before the document-to-catalogue promotion was added to the API.
INSERT INTO experiences (
  id, host_id, slug, title, short_description, description, category,
  city, state, village, price_per_person_paise, duration_minutes,
  min_group_size, max_group_size, content, status
)
SELECT
  d.id,
  d.owner_id,
  lower(regexp_replace(trim(d.payload->>'title'), '[^a-zA-Z0-9]+', '-', 'g')) || '-' || left(d.id::text, 8),
  d.payload->>'title',
  coalesce(d.payload->>'shortDescription', ''),
  d.payload->>'description',
  d.payload->>'category',
  coalesce(nullif(d.payload->>'city', ''), d.payload->>'location'),
  coalesce(d.payload->>'state', ''),
  nullif(d.payload->>'village', ''),
  round(coalesce(nullif(d.payload->>'pricePerPerson', '')::numeric, 0) * 100),
  CASE
    WHEN lower(coalesce(d.payload->>'duration', '')) ~ '^[0-9]+[[:space:]]*min' THEN (regexp_match(lower(d.payload->>'duration'), '([0-9]+)'))[1]::integer
    WHEN lower(coalesce(d.payload->>'duration', '')) ~ '^[0-9]+[[:space:]]*hour' THEN (regexp_match(lower(d.payload->>'duration'), '([0-9]+)'))[1]::integer * 60
    WHEN lower(coalesce(d.payload->>'duration', '')) ~ '^[0-9]+[[:space:]]*day' THEN (regexp_match(lower(d.payload->>'duration'), '([0-9]+)'))[1]::integer * 1440
    ELSE 60
  END,
  1,
  greatest(coalesce(nullif(d.payload->>'maxGroupSize', '')::integer, 1), 1),
  d.payload,
  'published'
FROM document_records d
WHERE d.domain = 'creatorexperiences'
  AND d.owner_id IS NOT NULL
  AND coalesce(d.payload->>'title', '') <> ''
  AND coalesce(d.payload->>'description', '') <> ''
  AND coalesce(d.payload->>'category', '') <> ''
  AND coalesce(nullif(d.payload->>'city', ''), d.payload->>'location', '') <> ''
ON CONFLICT (id) DO UPDATE SET status = 'published', updated_at = now();
