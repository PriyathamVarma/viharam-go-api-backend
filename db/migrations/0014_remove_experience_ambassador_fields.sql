-- Ambassadors are assigned by admins to area operations and bookings, not to
-- the experience itself. Remove legacy creator-document fields.
UPDATE document_records
SET payload = payload - 'ambassadorId' - 'isAmbassadorLed' - 'ambassadorFee',
    updated_at = now()
WHERE domain = 'creatorexperiences';
