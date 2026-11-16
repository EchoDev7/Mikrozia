#!/bin/sh
# Validates a Mikrozia release version against the canonical MIKROZIA_VERSION
# file. Comparison is byte-exact: whitespace is rejected, never trimmed.
# With --canonical-only, validates and prints the canonical file value alone.
set -eu
LC_ALL=C
export LC_ALL

canonical_file="MIKROZIA_VERSION"
pattern='^v([0-9]+\.[0-9]+\.[0-9]+-mikrozia\.[0-9]+|[0-9]+-mikrozia)$'
build_arg_hint='--build-arg MIKROZIA_VERSION="$(cat MIKROZIA_VERSION)"'

read_canonical() {
    if [ ! -f "$canonical_file" ]; then
        echo "ERROR: canonical MIKROZIA_VERSION file is missing" >&2
        exit 1
    fi
    # Append a sentinel so command substitution preserves trailing newlines,
    # then strip only the sentinel. The file content is never normalized.
    canonical="$(cat "$canonical_file"; printf 'x')"
    canonical="${canonical%x}"
}

reject_whitespace() {
    label="$1"
    value="$2"
    # grep sees one line at a time, so a newline separator must be detected on
    # the whole string. Otherwise a valid line inside a multiline value or a
    # trailing newline would pass the per-line checks below.
    newline='
'
    case "$value" in
        *"$newline"*)
            echo "ERROR: ${label} must contain no whitespace (leading, trailing, embedded, tab, or newline)" >&2
            exit 1
            ;;
    esac
    if printf '%s' "$value" | grep -q '[[:space:]]'; then
        echo "ERROR: ${label} must contain no whitespace (leading, trailing, embedded, tab, or newline)" >&2
        exit 1
    fi
}

validate_canonical() {
    if [ -z "$canonical" ]; then
        echo "ERROR: canonical MIKROZIA_VERSION file is empty" >&2
        exit 1
    fi
    reject_whitespace "canonical MIKROZIA_VERSION file" "$canonical"
    if ! printf '%s\n' "$canonical" | grep -qxE "$pattern"; then
        echo "ERROR: canonical MIKROZIA_VERSION file is invalid ('$canonical')" >&2
        exit 1
    fi
}

read_canonical
validate_canonical

if [ "${1:-}" = "--canonical-only" ]; then
    printf '%s' "$canonical"
    exit 0
fi

version="${1:-}"

if [ -z "$version" ]; then
    echo "ERROR: MIKROZIA_VERSION build-arg is required (e.g. ${build_arg_hint})" >&2
    exit 1
fi
reject_whitespace "MIKROZIA_VERSION" "$version"
if ! printf '%s\n' "$version" | grep -qxE "$pattern"; then
    echo "ERROR: invalid MIKROZIA_VERSION '$version' (expected vMAJOR-mikrozia or vUPSTREAM.MAJOR.PATCH-mikrozia.REVISION with no whitespace)" >&2
    exit 1
fi
if [ "$version" != "$canonical" ]; then
    echo "ERROR: MIKROZIA_VERSION '$version' does not match canonical MIKROZIA_VERSION '$canonical'" >&2
    exit 1
fi
printf '%s' "$canonical"
