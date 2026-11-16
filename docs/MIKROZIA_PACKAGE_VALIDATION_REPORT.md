# MIKROZIA — PHASE 3A: PACKAGE VALIDATION REPORT

> [!CAUTION]
> **HISTORICAL RECORD — SUPERSEDED.** This report documents the Phase 3A offline
> package-validation experiments for an early image. It is retained for evidence
> only. Current installation, updates, and data paths are defined by
> [MIKROZIA_CHR_INSTALL_FA.md](MIKROZIA_CHR_INSTALL_FA.md) and the
> [RouterOS installation guide](content/docs/en/guide/installation.mdx): versioned
> OCI images with persistent mounts, user-managed RouterOS network policy, and
> user-managed certificates.

**Document Version:** 1.0.0
**Date:** November 1, 2026
**Status:** HISTORICAL — SUPERSEDED FOR INSTALLATION
**Upstream Version:** 3x-ui `v3.11.0`
**Upstream Git Commit:** `c621ba9014a6aa21fd7c9ecf45655e44e24ebf13`
**Upstream Image Digest:** `sha256:1628adbd9bed680d25d6f714bef4a20f132105f4612ea294952f6665dad32e5b`

---

## 1. Executive Summary

As part of Phase 3A (Safe Offline Packaging), platform-specific container image archives were generated and validated locally on the development workstation. Each archive was exported using explicit `--platform` filtering, inspected for OCI configuration metadata, scanned for internal binary completeness and executable permissions, and verified via cryptographic checksums.

### Validation Result: ALL THREE PACKAGES VERIFIED & VALIDATED

- `3xui_amd64.tar` (Target A: CHR x86_64) &rarr; **VALIDATED**
- `3xui_armv7.tar` (Target B: RB3011UiAS ARM32) &rarr; **VALIDATED**
- `3xui_arm64.tar` (Target C: Physical ARM64) &rarr; **VALIDATED**

---

## 2. Package Manifest & Cryptographic Verification

| Archive File     | Target Architecture | OCI Image ID             | File Size (Bytes) | File Size (MiB) | SHA-256 Checksum                                                   |
| :--------------- | :------------------ | :----------------------- | :---------------- | :-------------- | :----------------------------------------------------------------- |
| `3xui_amd64.tar` | `linux/amd64`       | `sha256:047a4e1f6119...` | 111,005,696       | 105.86 MiB      | `f8031caf2dc494f0821ccd3fffb04888379687cc219bed172b1053ba35026e76` |
| `3xui_armv7.tar` | `linux/arm/v7`      | `sha256:20c6c9341c4e...` | 107,046,400       | 102.09 MiB      | `50dfc7eff6cadbcf4d435689976d142bfc570caa0847f04895741ef71f17950f` |
| `3xui_arm64.tar` | `linux/arm64`       | `sha256:bd8932079686...` | 107,130,368       | 102.17 MiB      | `8dba2aaac411d253b4b658134905293fa59b66b3e8b3ff7a5f14fc92ebb08c6a` |

---

## 3. OCI Configuration & Architecture Audit

Each package's embedded configuration JSON was parsed directly from the tar archive to verify architecture, entrypoints, and environment variables:

### 3.1 Target A: `3xui_amd64.tar` (CHR x86_64)

- **OS / Architecture:** `linux / amd64` (Variant: `""`)
- **Entrypoint:** `["/app/DockerEntrypoint.sh"]`
- **Default CMD:** `["./x-ui"]`
- **Exposed Ports:** `2053/tcp`
- **Volumes Declared:** `["/etc/x-ui"]`
- **Default Environment:**
  - `XUI_IN_DOCKER=true`
  - `XUI_MAIN_FOLDER=/app`
  - `XUI_ENABLE_FAIL2BAN=true` (historical: removed in P0-F2B-01; the variable no longer exists)
  - `XUI_DB_TYPE=""`
  - `XUI_DB_DSN=""`

### 3.2 Target B: `3xui_armv7.tar` (RB3011UiAS ARM32)

- **OS / Architecture / Variant:** `linux / arm / v7`
- **Entrypoint:** `["/app/DockerEntrypoint.sh"]`
- **Default CMD:** `["./x-ui"]`
- **Exposed Ports:** `2053/tcp`
- **Volumes Declared:** `["/etc/x-ui"]`
- **Default Environment:** Identical to upstream baseline.

### 3.3 Target C: `3xui_arm64.tar` (Physical ARM64)

- **OS / Architecture:** `linux / arm64` (Variant: `""`)
- **Entrypoint:** `["/app/DockerEntrypoint.sh"]`
- **Default CMD:** `["./x-ui"]`
- **Exposed Ports:** `2053/tcp`
- **Volumes Declared:** `["/etc/x-ui"]`
- **Default Environment:** Identical to upstream baseline.

---

## 4. RootFS Layer & Binary Completeness Audit

Every tar archive was decompressed in memory to verify that the required executables, helper scripts, and geodata databases are present and marked executable (`mode 0755`):

```
+---------------------------------+------------------------+-------------------+--------------------+
| Binary / Component File         | 3xui_amd64.tar (x86_64)| 3xui_armv7.tar (v7)| 3xui_arm64.tar     |
+---------------------------------+------------------------+-------------------+--------------------+
| /app/x-ui (Master Panel Binary) | 77,478,528 B (0755)    | 75,037,816 B (0755| 72,993,536 B (0755)|
| /app/bin/xray-linux-<arch>      | 35,045,500 B (0755)    | 34,144,380 B (0755| 32,505,980 B (0755)|
| /app/DockerEntrypoint.sh        | 3,146 B (0755)         | 3,146 B (0755)    | 3,146 B (0755)     |
| /usr/bin/x-ui (CLI Management)  | 139,953 B (0755)       | 139,953 B (0755)  | 139,953 B (0755)   |
| /app/bin/geoip.dat (Routing DB) | 16,570,425 B (0644)    | 16,570,425 B (0644| 16,570,425 B (0644)|
| /app/internal/web/translation   | Present (14 JSON files)| Present           | Present            |
+---------------------------------+------------------------+-------------------+--------------------+
```

---

## 5. Remaining Blockers for First Physical RouterOS Installation

While offline packaging is complete, the following technical prerequisites remain before executing the first deployment on real hardware:

1. **Physical RB3011 Device Access & Mode Confirmation:**
   - RouterOS requires physical button or power-cycle confirmation within 5 minutes of issuing `/system/device-mode/update container=yes`. This cannot be performed headlessly over remote SSH without operator presence.
2. **External USB Storage Pre-Mounting:**
   - The RB3011 operator must manually connect a USB 3.0 drive formatted as `ext4` and verify it is mounted as `usb1`. Under no circumstances should internal NAND be used.
3. **CGO / Musl Runtime Compatibility on ARMv7:**
   - Upstream packages `linux/arm/v7` binaries using musl libc. Physical execution on the Qualcomm IPQ8064 dual-core processor must be validated during acceptance testing to confirm that the CGO SQLite library (`go-sqlite3`) loads without dynamic linker faults on RouterOS v7.25.
4. **Network Verification:**
   - Confirm that RouterOS interface lists (`WAN` and `LAN`) are properly configured before applying DST-NAT forwarding rules.

---

## 6. Phase 3A Sign-Off

The three deployment archives are stored locally at:

- `release/packages/3xui_amd64.tar`
- `release/packages/3xui_armv7.tar`
- `release/packages/3xui_arm64.tar`
- `release/packages/SHA256SUMS`

All local packaging and verification objectives for Phase 3A have been satisfied.
