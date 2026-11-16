#!/bin/sh
set -e

SCRIPT_DIR=$(cd "$(dirname "$0")" && pwd)
VERIFY_HELPER="${SCRIPT_DIR}/scripts/build/verify-xray-checksum.sh"
VERIFY_MTG_HELPER="${SCRIPT_DIR}/scripts/build/verify-mtg-checksum.sh"

# Read XRAY_VERSION from parameter 3, or environment, or XRAY_VERSION file
XRAY_VERSION="${3:-${XRAY_VERSION}}"
if [ -z "$XRAY_VERSION" ] && [ -f "XRAY_VERSION" ]; then
    XRAY_VERSION=$(tr -d '[:space:]' < XRAY_VERSION)
elif [ -z "$XRAY_VERSION" ] && [ -f "../XRAY_VERSION" ]; then
    XRAY_VERSION=$(tr -d '[:space:]' < ../XRAY_VERSION)
elif [ -z "$XRAY_VERSION" ] && [ -f "../../XRAY_VERSION" ]; then
    XRAY_VERSION=$(tr -d '[:space:]' < ../../XRAY_VERSION)
fi

# Fail closed: must match ^v[0-9]+\.[0-9]+\.[0-9]+$
if ! echo "$XRAY_VERSION" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+$'; then
    echo "DockerInit: invalid or empty XRAY_VERSION '$XRAY_VERSION' (must match vMAJOR.MINOR.PATCH)" >&2
    exit 1
fi

# Read MTG_VERSION from parameter 4, or environment, or MTG_VERSION file
MTG_VERSION="${4:-${MTG_VERSION}}"
if [ -z "$MTG_VERSION" ] && [ -f "MTG_VERSION" ]; then
    MTG_VERSION=$(tr -d '[:space:]' < MTG_VERSION)
elif [ -z "$MTG_VERSION" ] && [ -f "../MTG_VERSION" ]; then
    MTG_VERSION=$(tr -d '[:space:]' < ../MTG_VERSION)
elif [ -z "$MTG_VERSION" ] && [ -f "../../MTG_VERSION" ]; then
    MTG_VERSION=$(tr -d '[:space:]' < ../../MTG_VERSION)
fi

# Fail closed: must match ^v[0-9]+\.[0-9]+\.[0-9]+$
if ! echo "$MTG_VERSION" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+$'; then
    echo "DockerInit: invalid or empty MTG_VERSION '$MTG_VERSION' (must match vMAJOR.MINOR.PATCH)" >&2
    exit 1
fi

# $1 is Docker's TARGETARCH and $2 its TARGETVARIANT (v6/v7 on arm). The
# platform names match the release archives and the mtg-multi packages.
case $1 in
    amd64 | "")
        PLATFORM="amd64"
        ;;
    arm64 | armv8 | aarch64)
        PLATFORM="arm64"
        ;;
    arm | arm32 | armv7)
        PLATFORM="armv7"
        if [ "$2" = "v6" ]; then
            PLATFORM="armv6"
        fi
        ;;
    armv6)
        PLATFORM="armv6"
        ;;
    386 | i386)
        PLATFORM="386"
        ;;
    *)
        echo "DockerInit: unsupported architecture '$1'" >&2
        exit 1
        ;;
esac
case $PLATFORM in
    amd64) XRAY_ZIP="64" ;;
    arm64) XRAY_ZIP="arm64-v8a" ;;
    armv7) XRAY_ZIP="arm32-v7a" ;;
    armv6) XRAY_ZIP="arm32-v6" ;;
    386) XRAY_ZIP="32" ;;
esac
# The panel looks binaries up by GOARCH; on arm Xray's is "arm32"
# (internal/xray/process.go) and mtg's is plain "arm".
case $PLATFORM in
    armv7 | armv6)
        XRAY_BIN="xray-linux-arm32"
        MTG_BIN="mtg-linux-arm"
        ;;
    *)
        XRAY_BIN="xray-linux-${PLATFORM}"
        MTG_BIN="mtg-linux-${PLATFORM}"
        ;;
esac
mkdir -p build/bin
cd build/bin

# Resilient downloader with automatic retry for transient network/TLS errors
dl() {
    curl --retry 5 --retry-connrefused --retry-delay 3 --retry-all-errors -sfL "$@"
}

XRAY_ARCHIVE="Xray-linux-${XRAY_ZIP}.zip"
XRAY_DGST="${XRAY_ARCHIVE}.dgst"
XRAY_BASE_URL="https://github.com/XTLS/Xray-core/releases/download/${XRAY_VERSION}"

cleanup_xray_tmp() {
    rm -f "${XRAY_ARCHIVE}" "${XRAY_DGST}"
}

dl -RO "${XRAY_BASE_URL}/${XRAY_ARCHIVE}" || { cleanup_xray_tmp; exit 1; }
dl -RO "${XRAY_BASE_URL}/${XRAY_DGST}" || { cleanup_xray_tmp; exit 1; }

if ! sh "${VERIFY_HELPER}" "${XRAY_ARCHIVE}" "${XRAY_DGST}"; then
    cleanup_xray_tmp
    exit 1
fi

unzip -o "${XRAY_ARCHIVE}" || { cleanup_xray_tmp; exit 1; }
rm -f "${XRAY_ARCHIVE}" "${XRAY_DGST}" geoip.dat geosite.dat
mv -f xray "${XRAY_BIN}"
# mtg-multi (MTProto sidecar) ships prebuilt release binaries for every target
# we package, so download, verify its release checksum manifest, and unpack it.
MTG_PKG="mtg-multi-${MTG_VERSION#v}-linux-${PLATFORM}"
MTG_ARCHIVE="${MTG_PKG}.tar.gz"
MTG_CHECKSUMS="mtg-multi-${MTG_VERSION#v}-checksums.txt"
MTG_BASE_URL="https://github.com/mhsanaei/mtg-multi/releases/download/${MTG_VERSION}"

cleanup_mtg_tmp() {
    rm -rf "${MTG_PKG}" "${MTG_ARCHIVE}" "${MTG_CHECKSUMS}"
}

dl -RO "${MTG_BASE_URL}/${MTG_ARCHIVE}" || { cleanup_mtg_tmp; exit 1; }
dl -RO "${MTG_BASE_URL}/${MTG_CHECKSUMS}" || { cleanup_mtg_tmp; exit 1; }

if ! sh "${VERIFY_MTG_HELPER}" "${MTG_ARCHIVE}" "${MTG_CHECKSUMS}" "${MTG_ARCHIVE}"; then
    cleanup_mtg_tmp
    exit 1
fi

tar -xzf "${MTG_ARCHIVE}" || { cleanup_mtg_tmp; exit 1; }
mv -f "${MTG_PKG}/mtg-multi" "${MTG_BIN}" || { cleanup_mtg_tmp; exit 1; }
cleanup_mtg_tmp
chmod +x "${MTG_BIN}"
# Bundled Geo data is image build-time content: download only the exact tagged
# release URLs recorded in GEO_DATA_SOURCES and fail closed unless each
# artifact matches its recorded SHA-256. No mutable release channel and no
# runtime updater may rewrite these files; see the manifest header.
GEO_MANIFEST="${SCRIPT_DIR}/GEO_DATA_SOURCES"
GEO_VERIFY_HELPER="${SCRIPT_DIR}/scripts/build/verify-geo-checksum.sh"
if [ ! -f "${GEO_MANIFEST}" ]; then
    echo "DockerInit: missing Geo data manifest: ${GEO_MANIFEST}" >&2
    exit 1
fi
if [ ! -f "${GEO_VERIFY_HELPER}" ]; then
    echo "DockerInit: missing Geo checksum helper: ${GEO_VERIFY_HELPER}" >&2
    exit 1
fi
while read -r geo_file geo_url geo_sha geo_extra; do
    case "${geo_file}" in
        '' | '#'*) continue ;;
    esac
    case "${geo_file}" in
        geoip.dat | geosite.dat | geoip_IR.dat | geosite_IR.dat | geoip_RU.dat | geosite_RU.dat) ;;
        *)
            echo "DockerInit: unexpected Geo artifact '${geo_file}' in ${GEO_MANIFEST}" >&2
            exit 1
            ;;
    esac
    if [ -n "${geo_extra}" ] || [ -z "${geo_url}" ] || [ -z "${geo_sha}" ]; then
        echo "DockerInit: malformed Geo manifest entry for '${geo_file}'" >&2
        exit 1
    fi
    geo_release_segment="${geo_url#*/releases/}"
    geo_release_segment="${geo_release_segment%%/*}"
    if [ "${geo_release_segment}" = "latest" ]; then
        echo "DockerInit: Geo artifact '${geo_file}' must not use a mutable 'latest' release URL" >&2
        exit 1
    fi
    case "${geo_url}" in
        https://github.com/*/releases/download/*) ;;
        *)
            echo "DockerInit: Geo artifact '${geo_file}' must come from a tagged GitHub release, got '${geo_url}'" >&2
            exit 1
            ;;
    esac
    if ! printf '%s\n' "${geo_sha}" | grep -Eq '^[0-9a-fA-F]{64}$'; then
        echo "DockerInit: invalid SHA-256 for '${geo_file}' in ${GEO_MANIFEST}" >&2
        exit 1
    fi

    dl -o "${geo_file}" "${geo_url}" || { rm -f "${geo_file}"; exit 1; }
    if ! sh "${GEO_VERIFY_HELPER}" "${geo_file}" "${geo_sha}"; then
        rm -f "${geo_file}"
        exit 1
    fi
done < "${GEO_MANIFEST}"
for geo_file in geoip.dat geosite.dat geoip_IR.dat geosite_IR.dat geoip_RU.dat geosite_RU.dat; do
    if [ ! -s "${geo_file}" ]; then
        echo "DockerInit: Geo artifact '${geo_file}' was not produced from ${GEO_MANIFEST}" >&2
        exit 1
    fi
done
cd ../../
