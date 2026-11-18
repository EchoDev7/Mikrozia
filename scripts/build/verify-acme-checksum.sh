#!/bin/sh
set -e

ACME_FILE="$1"
EXPECTED_SHA="$2"

if [ -z "$ACME_FILE" ] || [ -z "$EXPECTED_SHA" ]; then
    echo "verify-acme-checksum: usage: $0 <acme-file> <expected-sha256>" >&2
    exit 1
fi

if [ ! -s "$ACME_FILE" ]; then
    echo "verify-acme-checksum: acme file missing or empty: $ACME_FILE" >&2
    exit 1
fi

EXPECTED_SHA=$(printf '%s' "$EXPECTED_SHA" | tr '[:upper:]' '[:lower:]')
if ! printf '%s\n' "$EXPECTED_SHA" | grep -Eq '^[0-9a-f]{64}$'; then
    echo "verify-acme-checksum: invalid expected SHA-256 for $ACME_FILE (got '$EXPECTED_SHA')" >&2
    exit 1
fi

if command -v sha256sum >/dev/null 2>&1; then
    ACTUAL_SHA=$(sha256sum "$ACME_FILE" | awk '{print $1}' | tr '[:upper:]' '[:lower:]')
elif command -v shasum >/dev/null 2>&1; then
    ACTUAL_SHA=$(shasum -a 256 "$ACME_FILE" | awk '{print $1}' | tr '[:upper:]' '[:lower:]')
else
    echo "verify-acme-checksum: neither sha256sum nor shasum is available in PATH" >&2
    exit 1
fi

if [ "$ACTUAL_SHA" != "$EXPECTED_SHA" ]; then
    echo "verify-acme-checksum: checksum mismatch for $ACME_FILE (expected $EXPECTED_SHA, got $ACTUAL_SHA)" >&2
    exit 1
fi

exit 0
