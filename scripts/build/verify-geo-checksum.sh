#!/bin/sh
set -e

GEO_FILE="$1"
EXPECTED_SHA="$2"

if [ -z "$GEO_FILE" ] || [ -z "$EXPECTED_SHA" ]; then
    echo "verify-geo-checksum: usage: $0 <geo-file> <expected-sha256>" >&2
    exit 1
fi

if [ ! -s "$GEO_FILE" ]; then
    echo "verify-geo-checksum: geo file missing or empty: $GEO_FILE" >&2
    exit 1
fi

EXPECTED_SHA=$(printf '%s' "$EXPECTED_SHA" | tr '[:upper:]' '[:lower:]')
if ! printf '%s\n' "$EXPECTED_SHA" | grep -Eq '^[0-9a-f]{64}$'; then
    echo "verify-geo-checksum: invalid expected SHA-256 for $GEO_FILE (got '$EXPECTED_SHA')" >&2
    exit 1
fi

if command -v sha256sum >/dev/null 2>&1; then
    ACTUAL_SHA=$(sha256sum "$GEO_FILE" | awk '{print $1}' | tr '[:upper:]' '[:lower:]')
elif command -v shasum >/dev/null 2>&1; then
    ACTUAL_SHA=$(shasum -a 256 "$GEO_FILE" | awk '{print $1}' | tr '[:upper:]' '[:lower:]')
else
    echo "verify-geo-checksum: neither sha256sum nor shasum is available in PATH" >&2
    exit 1
fi

if [ "$ACTUAL_SHA" != "$EXPECTED_SHA" ]; then
    echo "verify-geo-checksum: checksum mismatch for $GEO_FILE (expected $EXPECTED_SHA, got $ACTUAL_SHA)" >&2
    exit 1
fi

exit 0
