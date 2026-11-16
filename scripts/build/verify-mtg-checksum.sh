#!/bin/sh
set -e

ARCHIVE_FILE="$1"
CHECKSUMS_FILE="$2"
EXPECTED_FILENAME="${3:-$(basename "$ARCHIVE_FILE")}"

if [ -z "$ARCHIVE_FILE" ] || [ -z "$CHECKSUMS_FILE" ] || [ -z "$EXPECTED_FILENAME" ]; then
    echo "verify-mtg-checksum: usage: $0 <archive-file> <checksums-file> [expected-filename]" >&2
    exit 1
fi

case "$EXPECTED_FILENAME" in
    */* | *[[:space:]]*)
        echo "verify-mtg-checksum: invalid expected filename '$EXPECTED_FILENAME'" >&2
        exit 1
        ;;
esac

if [ ! -f "$ARCHIVE_FILE" ]; then
    echo "verify-mtg-checksum: archive file not found: $ARCHIVE_FILE" >&2
    exit 1
fi

if [ ! -f "$CHECKSUMS_FILE" ]; then
    echo "verify-mtg-checksum: checksums manifest not found: $CHECKSUMS_FILE" >&2
    exit 1
fi

AWK_STATUS=0
AWK_OUT=$(awk -v target="$EXPECTED_FILENAME" '
{
    sub(/\r$/, "")
    if ($0 ~ /^[[:space:]]*$/) {
        next
    }
    if ($0 !~ /^[0-9a-fA-F]+[[:space:]]+\*?[^[:space:]]+$/) {
        malformed = 1
        bad_line = NR
        exit 2
    }
    h = $1
    f = $2
    sub(/^\*/, "", f)
    if (length(h) != 64 || h !~ /^[0-9a-fA-F]+$/ || f == "") {
        malformed = 1
        bad_line = NR
        exit 2
    }
    if (f == target) {
        count++
        matched = tolower(h)
    }
}
END {
    if (malformed) {
        printf "MALFORMED:%d\n", bad_line
        exit 2
    }
    if (count == 0) {
        print "MISSING"
        exit 3
    }
    if (count > 1) {
        printf "DUPLICATE:%d\n", count
        exit 4
    }
    printf "OK:%s\n", matched
}' "$CHECKSUMS_FILE") || AWK_STATUS=$?

case "$AWK_STATUS" in
    0)
        EXPECTED_SHA=${AWK_OUT#OK:}
        ;;
    2)
        echo "verify-mtg-checksum: malformed manifest entry in $CHECKSUMS_FILE (line ${AWK_OUT#MALFORMED:})" >&2
        exit 1
        ;;
    3)
        echo "verify-mtg-checksum: missing checksum entry for '$EXPECTED_FILENAME' in $CHECKSUMS_FILE" >&2
        exit 1
        ;;
    4)
        echo "verify-mtg-checksum: duplicate checksum entries (${AWK_OUT#DUPLICATE:}) for '$EXPECTED_FILENAME' in $CHECKSUMS_FILE" >&2
        exit 1
        ;;
    *)
        echo "verify-mtg-checksum: unexpected manifest parse error in $CHECKSUMS_FILE" >&2
        exit 1
        ;;
esac

if ! printf '%s\n' "$EXPECTED_SHA" | grep -Eq '^[0-9a-f]{64}$'; then
    echo "verify-mtg-checksum: invalid SHA-256 digest format for '$EXPECTED_FILENAME' (got '$EXPECTED_SHA')" >&2
    exit 1
fi

if command -v sha256sum >/dev/null 2>&1; then
    ACTUAL_SHA=$(sha256sum "$ARCHIVE_FILE" | awk '{print $1}' | tr '[:upper:]' '[:lower:]')
elif command -v shasum >/dev/null 2>&1; then
    ACTUAL_SHA=$(shasum -a 256 "$ARCHIVE_FILE" | awk '{print $1}' | tr '[:upper:]' '[:lower:]')
else
    echo "verify-mtg-checksum: neither sha256sum nor shasum is available in PATH" >&2
    exit 1
fi

if ! printf '%s\n' "$ACTUAL_SHA" | grep -Eq '^[0-9a-f]{64}$'; then
    echo "verify-mtg-checksum: failed to compute valid SHA-256 for $ARCHIVE_FILE (got '$ACTUAL_SHA')" >&2
    exit 1
fi

if [ "$ACTUAL_SHA" != "$EXPECTED_SHA" ]; then
    echo "verify-mtg-checksum: checksum mismatch for $ARCHIVE_FILE (expected $EXPECTED_SHA, got $ACTUAL_SHA)" >&2
    exit 1
fi

exit 0
