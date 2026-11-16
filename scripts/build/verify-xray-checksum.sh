#!/bin/sh
set -e

ARCHIVE_FILE="$1"
DGST_FILE="$2"

if [ -z "$ARCHIVE_FILE" ] || [ -z "$DGST_FILE" ]; then
    echo "verify-xray-checksum: usage: $0 <archive-file> <dgst-file>" >&2
    exit 1
fi

if [ ! -f "$ARCHIVE_FILE" ]; then
    echo "verify-xray-checksum: archive file not found: $ARCHIVE_FILE" >&2
    exit 1
fi

if [ ! -f "$DGST_FILE" ]; then
    echo "verify-xray-checksum: digest file not found: $DGST_FILE" >&2
    exit 1
fi

# Require strictly one SHA2-256= entry line
MATCH_COUNT=$(grep -Ec '^[[:space:]]*SHA2-256=' "$DGST_FILE" || true)
if [ "$MATCH_COUNT" -ne 1 ]; then
    echo "verify-xray-checksum: expected exactly 1 SHA2-256 entry in $DGST_FILE, found $MATCH_COUNT" >&2
    exit 1
fi

EXPECTED_SHA=$(sed -n 's/^[[:space:]]*SHA2-256=[[:space:]]*//p' "$DGST_FILE" | tr -d '\r' | sed 's/[[:space:]]*$//' | tr '[:upper:]' '[:lower:]')

if ! printf '%s\n' "$EXPECTED_SHA" | grep -Eq '^[0-9a-f]{64}$'; then
    echo "verify-xray-checksum: invalid SHA2-256 digest format in $DGST_FILE (expected 64 lowercase hex chars, got '$EXPECTED_SHA')" >&2
    exit 1
fi

if command -v sha256sum >/dev/null 2>&1; then
    ACTUAL_SHA=$(sha256sum "$ARCHIVE_FILE" | awk '{print $1}' | tr '[:upper:]' '[:lower:]')
elif command -v shasum >/dev/null 2>&1; then
    ACTUAL_SHA=$(shasum -a 256 "$ARCHIVE_FILE" | awk '{print $1}' | tr '[:upper:]' '[:lower:]')
else
    echo "verify-xray-checksum: neither sha256sum nor shasum is available in PATH" >&2
    exit 1
fi

if ! printf '%s\n' "$ACTUAL_SHA" | grep -Eq '^[0-9a-f]{64}$'; then
    echo "verify-xray-checksum: failed to compute valid SHA-256 for $ARCHIVE_FILE (got '$ACTUAL_SHA')" >&2
    exit 1
fi

if [ "$ACTUAL_SHA" != "$EXPECTED_SHA" ]; then
    echo "verify-xray-checksum: checksum mismatch for $ARCHIVE_FILE (expected $EXPECTED_SHA, got $ACTUAL_SHA)" >&2
    exit 1
fi

exit 0
