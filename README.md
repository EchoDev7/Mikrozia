[English](/README.md) | [فارسی](/README.fa_IR.md) | [العربية](/README.ar_EG.md) | [中文](/README.zh_CN.md) | [Español](/README.es_ES.md) | [Русский](/README.ru_RU.md) | [Türkçe](/README.tr_TR.md) | [Upstream history](/README.upstream.md)

<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="media/mikrozia-dark.svg" />
    <source media="(prefers-color-scheme: light)" srcset="media/mikrozia-light.svg" />
    <img src="media/mikrozia-dark.svg" alt="Mikrozia" width="220" />
  </picture>
</p>

# Mikrozia — RouterOS Container Control Panel

[![Docker Hub](https://img.shields.io/badge/Docker%20Hub-echodev7%2Fmikrozia-blue.svg?logo=docker)](https://hub.docker.com/r/echodev7/mikrozia)
[![Release](https://img.shields.io/badge/release-v2.0.2--mikrozia-green.svg)](https://hub.docker.com/r/echodev7/mikrozia/tags)
[![RouterOS](https://img.shields.io/badge/RouterOS-7.25%2B-red.svg)](https://mikrotik.com)
[![License](https://img.shields.io/badge/License-GPLv3-blue.svg)](LICENSE)

Mikrozia is an independent Xray control panel and runtime package built specifically for **MikroTik RouterOS Container**, with **CHR x86_64** as the first-class reference platform.

- **Documentation:** [Mikrozia Docs](https://echodev7.github.io/Mikrozia/)
- **Persian CHR Install & Troubleshooting Guide:** [docs/MIKROZIA_CHR_INSTALL_FA.md](docs/MIKROZIA_CHR_INSTALL_FA.md)

## Highlights

- **Multi-architecture OCI images** published to Docker Hub (`linux/amd64`, `linux/arm64/v8`, `linux/arm/v7`) with pinned, checksum-verified Xray, sidecar, Geo, and ACME binaries — no systemd, no host firewall tools, and no in-place self-updater.
- **Persistent mounts for user data:** Database/settings on `/etc/x-ui` and TLS certificates/ACME state on `/root/cert` survive image replacement and rollback.
- **Environment-owned administrator account:** `MIKROZIA_ADMIN_USERNAME` and `MIKROZIA_ADMIN_PASSWORD` are the sole authoritative source for the panel admin account (no default `admin/admin` and no credentials printed to logs).
- **Live RouterOS telemetry in Overview:** Real-time RouterOS CPU, RAM, container status/logs, SSL certificate expiry, and live **Port 80 open/closed status** inside the SSL Certificate card.
- **Opt-in Managed RouterOS NAT Integration:** Off by default (RouterOS firewall and NAT policy remain operator-owned). When explicitly enabled with a dedicated RouterOS API account:
  - Reconciles only Mikrozia-owned `mikrozia:managed:inbound:*` dst-nat rules with conflict detection and rollback.
  - Displays live per-inbound NAT badges (**NAT Active**, **NAT Disabled**, **Missing / Drift**) with real RouterOS byte/packet counters and one-click **Sync** on the Inbounds page.
  - Supports automatic WAN `in-interface` detection (works across `ether1`, `pppoe-out1`, `lte1`, VLANs, etc.), live managed rule inspection, and orphaned rule cleanup.
  - Includes optional Panel (`2053`) & Subscription (`2096`) port NAT sync (`mikrozia:managed:service:panel` / `sub`) gated by explicit two-step confirmation.
- **Read-only RouterOS Pre-flight Diagnostics:** Inspects RouterOS free disk space, VETH interface status, container gateway IP, active outbound `srcnat` masquerade, and inbound port conflicts with manual `dstnat` rules without modifying router configuration.
- **Built-in ACME TLS issuance & renewal:** Supports **HTTP-01** (manual renewal with operator-owned temporary port 80 reachability) and **Cloudflare DNS-01** (automatic renewal via the panel's internal scheduler).
- **Userspace AmneziaWG & full Xray protocol suite:** Runs `amneziawg-go` entirely in userspace alongside VLESS, VMess, Trojan, Shadowsocks, Hysteria2, TUIC, MASQUE, XHTTP, Reality, and more.

> **Network ownership:** By default, Mikrozia never creates or modifies RouterOS firewall, NAT, port-forward, masquerade, routing, or access-control rules. Managed RouterOS NAT Integration is strictly opt-in, scoped only to Mikrozia-tagged rules, and never touches operator-created rules.

## Current Release

```text
echodev7/mikrozia:v2.0.2-mikrozia
```

Published architectures:

- `linux/amd64` (CHR reference platform)
- `linux/arm64/v8`
- `linux/arm/v7`

All three architectures are built, started, and smoke-tested in CI against the runtime contract (versions, mounts, Geo assets, and absence of host firewall tools). Always pin a versioned tag in production instead of `latest`.

## Requirements (CHR / RouterOS)

- RouterOS `7.25.0` or newer (Stable channel)
- Matching RouterOS `container` extra package (exact same version)
- Device Mode enabled with `container=yes` (requires cold reboot)
- At least 2 GiB RAM and 2 GiB free storage
- CHR `x86_64` for the beginner reference installation path

## Quick Install (RouterOS 7.25+)

1. Install RouterOS and the matching `container` package.
2. Enable container Device Mode and cold-reboot CHR.
3. Run the following commands in order in RouterOS Terminal or WinBox.

> This path follows the current RouterOS `7.25` container contract. Clean-CHR acceptance for `P1-CHR-01` is still tracked in the project ledger; test the full path on a temporary CHR before production use.

If `172.16.10.0/24`, the default bridge/VETH names, or storage paths are already in use, update them consistently across all commands. Mikrozia-owned container objects are tagged with `[Mikrozia:container]`.

Create persistent and temporary directories:

```routeros
/file/add name=containers type=directory
/file/add name=containers/mikrozia type=directory
/file/add name=containers/mikrozia/data type=directory
/file/add name=containers/mikrozia/cert type=directory
/file/add name=containers/mikrozia/tmp type=directory
```

Skip any `/file/add` line for a directory that already exists. RouterOS creates the versioned root directory during extraction.

Create Bridge and VETH:

```routeros
/interface/bridge/add name=br-mikrozia comment="[Mikrozia:container] Bridge"
/ip/address/add address=172.16.10.1/24 interface=br-mikrozia comment="[Mikrozia:container] Gateway"
/interface/veth/add name=veth-mikrozia address=172.16.10.2/24 gateway=172.16.10.1 comment="[Mikrozia:container] VETH"
/interface/bridge/port/add bridge=br-mikrozia interface=veth-mikrozia comment="[Mikrozia:container] Bridge Port"
```

Create persistent mounts and environment variables:

```routeros
/container/mounts/add list=MIKROZIA_DATA src=containers/mikrozia/data dst="/etc/x-ui" comment="[Mikrozia:container] Data Mount"
/container/mounts/add list=MIKROZIA_CERT src=containers/mikrozia/cert dst="/root/cert" comment="[Mikrozia:container] Cert Mount"
/container/envs/add list=MIKROZIA_ENV key=XUI_PORT value="2053" comment="[Mikrozia:container] Environment"
/container/envs/add list=MIKROZIA_ENV key=HOME value="/root" comment="[Mikrozia:container] Environment"
/container/envs/add list=MIKROZIA_ENV key=MIKROZIA_ADMIN_USERNAME value="YOUR_ADMIN_USERNAME" comment="[Mikrozia:container] Environment"
/container/envs/add list=MIKROZIA_ENV key=MIKROZIA_ADMIN_PASSWORD value="YOUR_STRONG_PASSWORD" comment="[Mikrozia:container] Environment"
```

> **Sensitivity notice:** RouterOS container environment variables are configuration, not a secret store. Anyone able to read or export RouterOS configuration can read `/container/envs`. `MIKROZIA_ADMIN_USERNAME` and `MIKROZIA_ADMIN_PASSWORD` are the authoritative admin account settings and must be supplied **together**; supplying only one fails closed at startup. Mikrozia never prints these values to logs or returns them via API.

Configure Docker Hub registry and create the versioned container:

```routeros
/container/config/set registry-url="https://registry-1.docker.io" tmpdir=containers/mikrozia/tmp
/container/add remote-image="echodev7/mikrozia:v2.0.2-mikrozia" interface=veth-mikrozia root-dir=containers/mikrozia/root-v2.0.2-mikrozia mountlists="MIKROZIA_DATA,MIKROZIA_CERT" envlist=MIKROZIA_ENV dns="1.1.1.1,8.8.8.8" memory-high=512M memory-max=1024M logging=yes start-on-boot=yes comment="[Mikrozia:container] Container"
```

Monitor extraction:

```routeros
/container/print detail
/log/print follow-only where topics~"container"
```

Start the container once extraction finishes:

```routeros
/container/start [find where comment="[Mikrozia:container] Container"]
```

These commands create only the Bridge/VETH, mounts, environment, and container. They do not create or modify any RouterOS firewall, NAT, port-forward, or routing rules.

## Runtime Contract

| Item | Value |
|---|---|
| Database & panel settings | `/etc/x-ui` |
| TLS certificates & ACME state | `/root/cert` |
| `HOME` | `/root` |
| Internal panel port | `2053/TCP` |
| Internal subscription port | `2096/TCP` |
| Host firewall tools in image | None |
| Logs | `stdout`/`stderr` → RouterOS log |
| Start on boot | Enabled |
| Memory high / max | `512M / 1024M` |

Example TLS certificate paths for `vpn.yourdomain.com`:

```text
/root/cert/vpn.yourdomain.com/fullchain.pem
/root/cert/vpn.yourdomain.com/privkey.pem
```

### Container Lifecycle & `x-ui` CLI

- `x-ui start`, `x-ui stop`, `x-ui enable`, and `x-ui disable` inside the container do not mutate the container lifecycle; they print the equivalent RouterOS commands targeting `[find where comment="[Mikrozia:container] Container"]`.
- `x-ui restart` reloads the panel web server (`SIGHUP`) and restarts Xray (`SIGUSR1`) inside the container without stopping the container; `x-ui restart-xray` restarts Xray only.
- For a full container restart from Terminal or WinBox, run: `/container/restart [find where comment="[Mikrozia:container] Container"]`.

## Updating & Rollback

Database state and TLS certificates live on persistent mounts, while each version uses its own `root-dir`:

1. Back up `containers/mikrozia/data` and `containers/mikrozia/cert`.
2. Stop the running container.
3. Add a new container with the new versioned tag and a new `root-dir`, reusing `MIKROZIA_DATA`, `MIKROZIA_CERT`, and `MIKROZIA_ENV`.
4. Start and verify the new container.
5. Keep the previous container until verified so rollback is instant.

Never use an in-place updater to overwrite the container RootFS.

## Releases & Local Docker Build

- Release workflow: `.github/workflows/docker.yml`
- Required GitHub Secrets: `DOCKER_HUB_USERNAME`, `DOCKER_HUB_TOKEN`
- Release tag format: `v<major>.<minor>.<patch>-mikrozia` (e.g. `v2.0.2-mikrozia`)
- CI tests and smoke-tests all three published architectures before publishing `amd64`, `arm64`, and `arm/v7` images with SBOM, provenance, and digests to `echodev7/mikrozia`.

To build the Docker image locally, pass the pinned version files as build arguments:

```bash
docker build \
  --build-arg MIKROZIA_VERSION="$(cat MIKROZIA_VERSION)" \
  --build-arg XRAY_VERSION="$(tr -d '[:space:]' < XRAY_VERSION)" \
  --build-arg MTG_VERSION="$(tr -d '[:space:]' < MTG_VERSION)" \
  -t mikrozia:local .
```

## Limitations

- The image contains no Fail2ban, iptables, nftables, or UFW.
- Fail2ban-based IP Limit enforcement is unavailable in RouterOS Container (stored user data is preserved safely). Traffic quotas and expiry work normally.
- Standard `docker compose` does not run directly inside RouterOS `/container`.
- Host-kernel features (kernel BBR sysctl, kernel AmneziaWG, TUN/TProxy) are not assumed; userspace equivalents (`amneziawg-go`, TUIC/QUIC BBR) are used instead.

## Testing

```bash
go test ./...
python3 -m unittest discover -s tests -p 'test_*.py'
bash -n DockerEntrypoint.sh
sh -n DockerInit.sh
bash -n x-ui.sh
```

## Upstream & License

- Repository: [EchoDev7/Mikrozia](https://github.com/EchoDev7/Mikrozia) (originally forked from MHSanaei/3x-ui v3.11.0)
- Xray: [XTLS/Xray-core](https://github.com/XTLS/Xray-core)
- License: [GPL-3.0](LICENSE)
