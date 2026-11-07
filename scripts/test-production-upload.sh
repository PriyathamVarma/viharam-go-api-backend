#!/usr/bin/env bash
set -euo pipefail

: "${VIHARAM_API_URL:?Set VIHARAM_API_URL, for example https://viharam-go-api-backend.onrender.com}"
: "${VIHARAM_AUTH_TOKEN:?Set VIHARAM_AUTH_TOKEN to a real authenticated Viharam JWT}"
: "${VIHARAM_UPLOAD_FILE:?Set VIHARAM_UPLOAD_FILE to a local JPEG/PNG/WebP file}"

response="$(curl --fail-with-body --silent --show-error \
  -X POST "${VIHARAM_API_URL%/}/api/v1/uploads" \
  -H "Authorization: Bearer ${VIHARAM_AUTH_TOKEN}" \
  -H 'Accept: application/json' \
  -F "purpose=listing_photo" \
  -F "file=@${VIHARAM_UPLOAD_FILE}")"

printf '%s\n' "$response"
printf '%s\n' "$response" | grep -Eq '"url"[[:space:]]*:[[:space:]]*"https?://' \
  || { echo 'Upload response did not contain an https URL' >&2; exit 1; }
echo 'Authenticated production upload passed.'
