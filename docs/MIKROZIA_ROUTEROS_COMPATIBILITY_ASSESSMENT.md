# MIKROZIA — PHASE 2: MIKROTIK ROUTEROS CONTAINER COMPATIBILITY ASSESSMENT

**Document Version:** 2.1.0 (Audited & Quality-Gate Validated)
**Date:** November 1, 2026
**Status:** HISTORICAL ASSESSMENT — SUPERSEDED FOR INSTALLATION
**Current Policy:** Use `MIKROZIA_CHR_INSTALL_FA.md`; Mikrozia leaves Firewall/NAT entirely to the operator.
**Repository:** [EchoDev7/Mikrozia](https://github.com/EchoDev7/Mikrozia)
**Upstream Target:** [MHSanaei/3x-ui](https://github.com/MHSanaei/3x-ui)
**Verified Upstream Version:** `v3.11.0`
**Verified Upstream Git Commit:** `c621ba9014a6aa21fd7c9ecf45655e44e24ebf13`

---

## 1. Executive Summary & Final Verdict

This document presents the finalized, quality-gated technical compatibility assessment for deploying the **original, completely unmodified 3x-ui application (v3.11.0)** within MikroTik RouterOS v7 Container environments.

### 1.1 Technical Verdict: FEASIBLE WITH CONFIGURATION ADAPTATIONS ONLY

The genuine 3x-ui application (Go master web panel + supervised Xray-core subprocess + embedded SQLite storage engine) can successfully run inside an unprivileged Linux container under MikroTik RouterOS v7 across all primary proxy protocols (**VLESS REALITY, WebSocket, gRPC, VMess, Trojan, and Shadowsocks**).

- **Source Code Modifications:** **NONE (0%)**. No Go backend source code, React/Ant Design frontend components, translations, SQLite database schemas, or API endpoints need to be altered or patched.
- **Feature Integrity:** All proxy protocols, client traffic accounting, real-time statistics, subscription generation, and web panel management functions remain intact.
- **Operational Adaptations Required:**
  1. **Privileged Capabilities:** The RouterOS container runtime does not grant `CAP_NET_ADMIN` or `CAP_NET_RAW` to containers. Consequently, `iptables`-based Fail2ban must be disabled (`XUI_ENABLE_FAIL2BAN="false"`). Web authentication is protected by 3x-ui's built-in in-memory rate limiter; concurrent client connection limits are enforced by Xray core's internal online stats API; and public port brute-force protection is handled by RouterOS native firewall filters. WireGuard and AmneziaWG operate via their embedded userspace engines (`amneziawg-go` / gVisor).
  2. **Storage Architecture:** Physical routers (such as the reference RB3011UiAS) with 128 MiB internal NAND **must strictly use external USB 3.0 storage**. The internal NAND is mathematically incapable of holding the ~450 MiB root filesystem and must be shielded from SQLite write wear. Cloud Hosted Router (CHR) instances use their allocated virtual disk without requiring a secondary drive.
  3. **Management Access Control:** The web administration panel (`:2053`) **must never be exposed directly to the public WAN**. Management traffic must be restricted to LAN, WireGuard/management VPN, or loopback. Only designated proxy inbound ports (e.g., 443, 10443) are forwarded to WAN.

---

## 2. Locked Target Hardware Scope

The supported hardware scope is formally locked to three specific target categories. The minimum router system RAM across all target platforms is **1024 MiB**. The 128 MiB container memory benchmark is a performance measurement target, not an arbitrary quota to force feature removal.

```
+--------------------------------------------------------------------------------------------------+
|                                    LOCKED TARGET HARDWARE SCOPE                                   |
+-----------------------------------+----------------------------------+---------------------------+
| 1. MikroTik CHR (x86_64)          | 2. Physical ARM 32-bit (v7)      | 3. Physical ARM64         |
+-----------------------------------+----------------------------------+---------------------------+
| Platform: Cloud Hosted Router VM  | Reference: RB3011UiAS-RM         | Reference: RB5009UG+S+IN  |
| CPU: Virtualized x86_64           | CPU: Qualcomm IPQ8064 (2C@1.4GHz)| CPU: Marvell Armada (4C)  |
| Total System RAM: >= 1024 MiB     | Total System RAM: 1024 MiB       | Total System RAM: >= 1024M|
| Storage: Single Virtual Disk      | Storage: USB 3.0 External Drive  | Storage: USB 3.0 / M.2    |
| Primary Role: High-bandwidth VPS  | Primary Role: Edge Gateway       | Primary Role: High-perf   |
+-----------------------------------+----------------------------------+---------------------------+
```

### 2.1 Physical Reference Device Profile: MikroTik RB3011UiAS

- **RouterOS Version:** 7.25+
- **Architecture:** ARM 32-bit (`arm` / ARMv7-A Cortex-A15)
- **CPU:** Qualcomm Atheros IPQ8064 (2 cores @ 1400 MHz)
- **Total System RAM:** 1024 MiB
- **Internal Storage:** 128 MiB NAND (Only ~89.9 MiB free on a clean install)
- **External Storage Interface:** USB 3.0 Type-A
- **Significance:** Required physical acceptance-test hardware for Phase 3.

---

## 3. Storage Architecture & Filesystem Requirements

### 3.1 Cloud Hosted Router (CHR x86_64)

- **Storage Allocation:** Single virtual disk allocated to the CHR virtual machine (typically 10 GiB to 20 GiB on cloud VPS providers such as Hetzner).
- **Storage Placement:** Container rootfs, persistent `/etc/x-ui` data, temporary image extraction layers, and backups live on the existing virtual disk filesystem (e.g., directory `containers/3xui/`).
- **Secondary Virtual Disk:** **Not required**. Adding a second virtual drive is unnecessary unless storage isolation is explicitly mandated by hypervisor policy.

### 3.2 Physical Hardware (RB3011 ARM32 & ARM64 Devices)

- **NAND Constraint Analysis:**
  - Total Internal NAND: 128 MiB
  - Free Space Available: ~89.9 MiB
  - Compressed Image Archive (`3xui_armv7.tar`): ~105 MiB
  - Extracted Container Rootfs: ~450 MiB
  - _Conclusion:_ Attempting to extract or run the container on internal NAND will trigger an immediate out-of-disk failure and induce severe flash memory wear.
- **Strict NAND Exclusion:** Internal NAND must never be used for container rootfs layers (`root-dir`), temporary extraction directories (`tmpdir`), or active SQLite database operations.
- **Mandatory External Storage:** A USB 3.0 SSD or high-end USB 3.0 flash drive (formatted as `ext4` via RouterOS `/disk format-drive`) mounted as `usb1` is strictly required.

### 3.3 Storage Layout Breakdown

```
usb1/ (External ext4 USB on RB3011 / ARM64, or disk on CHR)
├── 3xui/
│   ├── root/           <-- Container root-dir (/container add root-dir=...) [~450 MiB]
│   ├── data/           <-- Bind mount destination for /etc/x-ui (DB + Token Key) [~2-10 MiB]
│   │   ├── x-ui.db
│   │   ├── x-ui.db-wal
│   │   ├── x-ui.db-shm
│   │   ├── node_token_key.json
│   │   └── system_metrics.gob
│   ├── cert/           <-- Bind mount destination for /root/cert [Optional]
│   └── backups/        <-- Database export artifacts
└── tmp/                <-- Global container temporary extraction path (/container/config set tmpdir=...)
```

---

## 4. Empirical Test Results & Resource Benchmarks

All tests were performed in Docker Desktop using the official upstream release image (`ghcr.io/mhsanaei/3x-ui:v3.11.0`) under strict cgroup v2 resource limits.

### 4.1 Resource Measurements Summary

| Metric                          | Unconstrained Idle | 128 MiB Capped Idle   | 128 MiB Capped Load (12 Streams) | ARM64 Emulation (Idle)    |
| :------------------------------ | :----------------- | :-------------------- | :------------------------------- | :------------------------ |
| **Cgroup Memory Usage**         | 122.9 MiB          | **62.89 MiB** (49.1%) | **84.65 MiB** (66.1%)            | 199.2 MiB _(QEMU buffer)_ |
| **Peak Memory (`memory.peak`)** | —                  | —                     | **98.2 MiB** (`102,969,344 B`)   | —                         |
| **Memory Headroom (vs 128m)**   | —                  | 65.1 MiB              | **29.8 MiB**                     | —                         |
| **OOM Invocations / Kills**     | 0 / 0              | 0 / 0                 | **0 / 0**                        | 0 / 0                     |
| **Go `GOMEMLIMIT` Budget**      | Default            | 115 MiB (90% cgroup)  | 115 MiB                          | Default                   |
| **CPU Utilization**             | < 1.0%             | 0.77%                 | ~104% (1 vCPU saturated)         | ~1.5%                     |
| **Active PIDs**                 | 37                 | 18                    | 19                               | 5                         |

### 4.2 Traffic Load Test (12 Parallel Streams)

- **Transports Tested:** VLESS REALITY (TCP Vision), VLESS WebSocket, VLESS gRPC.
- **Workload:** 12 concurrent workers downloading 300 MiB payload files through local proxy listeners continuously for 40 seconds.
- **Results:** All 12 streams completed without transport resets (`HTTP 200`, ~300 MiB transferred per connection).
- **Memory Dynamics:** As memory pressure approached 90 MiB, Go runtime garbage collection (`GOGC=75`) triggered smoothly, stabilizing working set memory at ~84 MiB. CPU returned to `< 0.9%` within 2 seconds of traffic termination.

### 4.3 Container Persistence Test

- Inbounds provisioned via API (`/panel/api/inbounds/add`) were verified.
- The container was destroyed (`docker rm -f`) and recreated against the identical persistent `/etc/x-ui` volume.
- **Result:** All inbounds, client UUIDs, API tokens, and credentials were completely preserved. Proxy connections resumed immediately upon container recreation.

### 4.4 DNS Blackhole Test

- The container was launched with an unreachable nameserver (`--dns 10.255.255.1`).
- **Result:** Web panel, sub-server, and Xray core started without startup hangs. Internal timeout handling functioned as expected.

---

## 5. Security & Kernel Capabilities Analysis

### 5.1 Fail2ban and Linux Capabilities (`CAP_NET_ADMIN` / `CAP_NET_RAW`)

In standard Linux deployments, 3x-ui bundles Fail2ban with a custom jail (`3x-ipl`) that executes `iptables` and `ip6tables` to block client IPs exceeding per-inbound client limits.

- **Behavior with Capabilities (`NET_ADMIN` + `NET_RAW`):** `fail2ban-server` initializes, creates `f2b-3x-ipl` chains in `INPUT`, and enforces IP bans via kernel packet filtering.
- **Behavior without Capabilities (Default RouterOS Container):** `fail2ban-server` starts, but when it attempts to invoke `iptables`, the kernel returns:
  ```text
  iptables v1.8.13 (nf_tables): Could not fetch rule set generation id: Permission denied (you must be root)
  ```
  Bans are logged into `/var/log/x-ui/3xipl.log` but are **never enforced at the kernel firewall level**. Furthermore, running Python3 / Fail2ban consumes ~17 MiB of unnecessary RAM.
- **Required Configuration:** Originally, set `XUI_ENABLE_FAIL2BAN="false"` in the RouterOS container environment list. Superseded by P0-F2B-01: the variable and the Fail2ban slice were retired, so nothing needs to be set.
- **Security Implications & Mitigations:**
  1. _Web Panel Login Protection:_ **Unimpaired**. 3x-ui possesses an internal in-memory rate limiter that locks any IP/username combination after 5 failed login attempts for 15 minutes.
  2. _Active Online Tracking:_ **Unimpaired**. In version 3.11.0, 3x-ui natively queries Xray's internal online stats API to track and disconnect concurrent clients exceeding limits without needing iptables.
  3. _Network-level Brute-Force Protection:_ **Delegated to RouterOS**. On MikroTik, brute-force defense against port scanners or unauthenticated traffic on open proxy ports must be handled via RouterOS firewall filter rules (`/ip/firewall/filter`), which operates in hardware fast-path and is vastly superior to container iptables.

### 5.2 Kernel WireGuard & AmneziaWG

- **AmneziaWG Kernel Mode:** **REMOVED IN PHASE 0 (WAS BLOCKED)**. AmneziaWG Kernel Mode did not use `/dev/net/tun` (`/dev/net/tun` is a separate facility relevant to Userspace/TUN workloads, not AmneziaWG Kernel Mode). Instead, it required the out-of-tree `amneziawg.ko` host-kernel module, which registers the `amneziawg` rtnetlink device type so `ip link add ... type amneziawg` can succeed. While RouterOS rules can replace NAT and forwarding, they cannot create a host kernel module or netlink device type, and RouterOS Container provides no supported mechanism for installing `amneziawg.ko` into the RouterOS host kernel. _(Historical note: kernel-mode backend, `install-awg-kernel` CLI, and UI/API controls were permanently removed in Phase 0.)_
- **AmneziaWG Userspace Netstack:** **SUPPORTED**. 3x-ui embeds `amneziawg-go` compiled directly into the binary over a gVisor userspace network stack. It operates with zero host kernel dependencies.
- **WireGuard Kernel TUN:** **BLOCKED / FALLBACK SUPPORTED**. 3x-ui's code (`internal/xray/kernel_tun_linux.go`) checks for `CAP_NET_ADMIN` and `/dev/net/tun`. If either check fails, Xray core falls back to its built-in userspace WireGuard engine.

### 5.3 Host Kernel BBR (`sysctl` Congestion Control)

- **Status:** **REMOVED IN PHASE 0** (`unavailable host-kernel capability`).
- **Empirical Evidence on Reference CHR x86_64 (`v3.11.0-mikrozia.3`):**
  - Kernel reported inside RouterOS Container: `Linux 5.6.3-64 x86_64`.
  - `/proc/sys/net/ipv4/tcp_congestion_control` reported `cubic`.
  - `/proc/sys/net/ipv4/tcp_available_congestion_control` was not provided (`No such file or directory`).
  - `/proc/sys/net/core/default_qdisc` returned no usable value, `grep -i bbr /proc/modules` found no `tcp_bbr` module, and `/proc/config.gz` exposed no `CONFIG_TCP_CONG_BBR` / `CONFIG_NET_SCH_FQ` configuration.
  - Although `/sbin/sysctl` existed in the image, attempting to enable BBR from the legacy `x-ui` menu failed with `sysctl: error: 'net.core.default_qdisc' is an unknown key`, `sysctl: write error: No such file or directory`, and `Failed to enable BBR. Please check your system configuration.`
- **Conclusion:** Host Kernel BBR is not supported under the RouterOS Container contract. Containers cannot load host kernel modules or replace the RouterOS TCP congestion algorithm, and no RouterOS firewall, NAT, queue, mangle, or routing rule can supply kernel BBR. (Note: physical ARM32/ARM64 hardware was not separately tested for this item; Host Kernel BBR is excluded from Mikrozia's supported contract across all targets.)
- **Preserved Userspace vs. Xray `sockopt.tcpcongestion=bbr` Adaptation:** Userspace `bbr` congestion control in **TUIC**, **QUIC**, and **FinalMask** does not depend on host `sysctl` and remains preserved. New Xray `sockopt` / `echSockopt` configurations default to `OS Default (Unset)` instead of injecting `bbr`; legacy stored `tcpcongestion=bbr` values remain preserved in the database for backup/restore/rollback compatibility, are omitted at RouterOS runtime so subsequent `sockopt` options are not skipped, and can be cleared in the UI by selecting `OS Default`. Note that rolling back to an older Mikrozia release may re-send stored legacy `bbr` to Xray.

---

## 6. Comprehensive Feature Compatibility Matrix

Each original 3x-ui feature is evaluated independently across the three locked hardware targets:

- **VERIFIED IN DOCKER:** Empirically verified under runtime test on x86_64 Linux container.
- **VERIFIED IN ARM EMULATION:** Verified running native binary inside QEMU ARM64 container.
- **SUPPORTED BY DOCUMENTATION ONLY:** Documented upstream and verified by code audit, but awaiting physical hardware verification.
- **REQUIRES ROUTEROS CONFIGURATION:** Works identically once corresponding RouterOS networking (VETH/NAT/routing) is established.
- **DEGRADED OR BLOCKED:** Feature cannot execute due to RouterOS container namespace limitations.
- **NOT TESTED ON TARGET HARDWARE:** Requires physical hardware validation during Phase 3.

```
+------------------------------------+-----------------------+-------------------------+-----------------------+-------------------------------------------------------------+
| Feature / Subsystem                | CHR (x86_64)          | RB3011 (ARM32)          | Physical ARM64        | Technical Evidence / Operational Notes                      |
+------------------------------------+-----------------------+-------------------------+-----------------------+-------------------------------------------------------------+
| Web Administration UI (:2053)      | VERIFIED IN DOCKER    | NOT TESTED ON TARGET HW | VERIFIED IN ARM EMUL. | React/AntD Vite frontend rendered cleanly via Chrome.       |
| REST API & CLI Management          | VERIFIED IN DOCKER    | NOT TESTED ON TARGET HW | VERIFIED IN ARM EMUL. | Tested with Bearer token & `x-ui setting` commands.         |
| SQLite Database Storage            | VERIFIED IN DOCKER    | NOT TESTED ON TARGET HW | VERIFIED IN ARM EMUL. | Verified persistence across container re-creation.          |
| Xray Core Process Supervision      | VERIFIED IN DOCKER    | NOT TESTED ON TARGET HW | VERIFIED IN ARM EMUL. | Child process spawned, reloaded, and restarted by x-ui.     |
| VLESS REALITY (TCP Vision)         | VERIFIED IN DOCKER    | NOT TESTED ON TARGET HW | NOT TESTED ON TARGETHW| Verified with active x25519 handshake and 300MB payload.   |
| VLESS WebSocket Transport          | VERIFIED IN DOCKER    | NOT TESTED ON TARGET HW | NOT TESTED ON TARGETHW| Verified on /ws path with active data transfer.             |
| VLESS gRPC Transport               | VERIFIED IN DOCKER    | NOT TESTED ON TARGET HW | NOT TESTED ON TARGETHW| Verified on custom service name with active data transfer.  |
| VMess / Trojan / Shadowsocks       | SUPP. BY DOCS ONLY    | NOT TESTED ON TARGET HW | NOT TESTED ON TARGETHW| Standard Xray userspace inbounds; zero kernel dependencies. |
| Sub-server (:2096)                 | VERIFIED IN DOCKER    | NOT TESTED ON TARGET HW | VERIFIED IN ARM EMUL. | Embedded sub provider HTTP server starts cleanly.           |
| Web Brute-Force Limiter            | VERIFIED IN DOCKER    | NOT TESTED ON TARGET HW | VERIFIED IN ARM EMUL. | In-memory Go rate limiter (5 failed attempts = 15m block).  |
| Memory Auto-Tuning (GOMEMLIMIT)    | VERIFIED IN DOCKER    | NOT TESTED ON TARGET HW | NOT TESTED ON TARGETHW| Derived at 90% of cgroup RAM; GC throttled smoothly.        |
| Fail2ban (iptables in container)   | DEGRADED OR BLOCKED   | DEGRADED OR BLOCKED     | DEGRADED OR BLOCKED   | Blocked by lack of CAP_NET_ADMIN; must set env flag false.  |
| AmneziaWG Kernel Mode (Removed)    | REMOVED IN PHASE 0    | REMOVED IN PHASE 0      | REMOVED IN PHASE 0    | Removed in Phase 0; required `amneziawg.ko` host-kernel module & rtnetlink link type (not `/dev/net/tun`). |
| Host Kernel BBR (x-ui sysctl)      | REMOVED IN PHASE 0    | REMOVED IN PHASE 0      | REMOVED IN PHASE 0    | Tested on CHR x86_64 (`cubic` only, no `default_qdisc`/`tcp_bbr`); not part of Mikrozia supported contract. |
| Xray sockopt (`tcpcongestion=bbr`) | ADAPTED — STORED BUT OMITTED AT ROUTEROS RUNTIME | ADAPTED — STORED BUT OMITTED AT ROUTEROS RUNTIME | ADAPTED — STORED BUT OMITTED AT ROUTEROS RUNTIME | Stored `bbr` preserved in DB for compatibility; omitted from RouterOS runtime config; new configs default to OS Default; rollback to older image may re-send stored `bbr`. |
| AmneziaWG Userspace Netstack       | SUPP. BY DOCS ONLY    | NOT TESTED ON TARGET HW | NOT TESTED ON TARGETHW| Runs amneziawg-go over gVisor; requires no kernel module.   |
| WireGuard Kernel Mode              | DEGRADED OR BLOCKED   | DEGRADED OR BLOCKED     | DEGRADED OR BLOCKED   | Automatically falls back to Xray userspace WireGuard.       |
| Dynamic Port Listening             | REQ. ROUTEROS CONFIG  | REQ. ROUTEROS CONFIG    | REQ. ROUTEROS CONFIG  | Each new inbound port requires a RouterOS DST-NAT rule.     |
| Transparent Proxying (TProxy)      | REQ. ROUTEROS CONFIG  | REQ. ROUTEROS CONFIG    | REQ. ROUTEROS CONFIG  | Container iptables cannot mangle host; use RouterOS mangle. |
| Host Network Mode (--net=host)     | DEGRADED OR BLOCKED   | DEGRADED OR BLOCKED     | DEGRADED OR BLOCKED   | RouterOS container engine only supports VETH networking.    |
| Automated ACME via 80/443          | REQ. ROUTEROS CONFIG  | REQ. ROUTEROS CONFIG    | REQ. ROUTEROS CONFIG  | Requires port 80 forwarding or external DNS API keys.       |
| Direct WAN Admin Access (:2053)    | POLICY BLOCKED        | POLICY BLOCKED          | POLICY BLOCKED        | Prohibited by security policy; must restrict to LAN/VPN.   |
+------------------------------------+-----------------------+-------------------------+-----------------------+-------------------------------------------------------------+
```

---

## 7. Validated RouterOS Container CLI Syntax & Configuration

The commands below reflect verified MikroTik RouterOS v7 syntax. They account for global container engine memory boundaries, temporary extraction directories, persistent bind mounts, environment variable groups, and strict local-only administration access.

### 7.1 RouterOS Configuration Blueprint

```routeros
# ==============================================================================
# 1. Enable Container Device Mode
# ==============================================================================
# On CHR: Confirmed via system reboot or console
# On RB3011 / Physical: Requires physical button press / power cycle within 5 min
/system/device-mode/update container=yes

# ==============================================================================
# 2. Global Container Engine Configuration
# ==============================================================================
# Direct temporary pull/extraction files to external storage (sparing NAND)
# and configure global RAM limits.
# On CHR: tmpdir=containers/tmp
# On RB3011 / Physical: tmpdir=usb1/tmp
/container/config/set registry-url=https://ghcr.io tmpdir=usb1/tmp memory-high=256M memory-max=512M

# ==============================================================================
# 3. Virtual Ethernet (VETH) & Bridge Configuration
# ==============================================================================
/interface/bridge/add name=dockers-br comment="Mikrozia 3x-ui Bridge"
/ip/address/add address=172.16.10.1/24 interface=dockers-br comment="Container Gateway"
/interface/veth/add name=veth-3xui address=172.16.10.2/24 gateway=172.16.10.1
/interface/bridge/port/add bridge=dockers-br interface=veth-3xui

# Container Internet Egress (Masquerade)
/ip/firewall/nat/add chain=srcnat src-address=172.16.10.0/24 action=masquerade comment="3x-ui Outbound NAT"

# ==============================================================================
# 4. Port Forwarding (DST-NAT)
# ==============================================================================
# Proxy Inbound Traffic (e.g., VLESS REALITY on 10443) - Accessible via WAN
/ip/firewall/nat/add chain=dstnat in-interface-list=WAN dst-port=10443 protocol=tcp action=dst-nat to-addresses=172.16.10.2 to-ports=10443 comment="3x-ui Proxy Inbound"

# Web Admin Panel (2053) - RESTRICTED TO LAN (NEVER FORWARD TO WAN!)
/ip/firewall/nat/add chain=dstnat in-interface-list=LAN dst-port=2053 protocol=tcp action=dst-nat to-addresses=172.16.10.2 to-ports=2053 comment="3x-ui Web Panel (LAN Only)"

# ==============================================================================
# 5. Persistent Volume Mounts (RouterOS v7.21+ mount-list syntax)
# ==============================================================================
# Defined with list=, src=, and dst=
# On CHR: src=containers/3xui/data
# On RB3011 / Physical: src=usb1/3xui/data
/container/mounts/add list=3xui_data src=usb1/3xui/data dst=/etc/x-ui
/container/mounts/add list=3xui_cert src=usb1/3xui/cert dst=/root/cert

# ==============================================================================
# 6. Container Environment Variables
# ==============================================================================
# Defined with list=, key=, and value=
/container/envs/add list=3xui_env key=XRAY_VMESS_AEAD_FORCED value="false"
/container/envs/add list=3xui_env key=XUI_PORT value="2053"

# ==============================================================================
# 7. Container Instantiation & Startup
# ==============================================================================
# Pre-requisite: Transfer pre-built single-arch .tar archive to storage
# Referenced via mountlists= (RouterOS v7.21+) and envlist=
/container/add file=usb1/3xui_armv7.tar interface=veth-3xui root-dir=usb1/3xui/root mountlists=3xui_data,3xui_cert envlist=3xui_env logging=yes start-on-boot=yes

# Start the container
/container/start [find where root-dir~"3xui"]
```

---

## 8. Physical Hardware Gaps & Verification Risks

The following technical items cannot be validated by Docker Desktop tests and represent the empirical verification milestones for Phase 3 on physical hardware:

1. **Physical RB3011 ARMv7 CGO Binary Execution:** Upstream compiles `linux/arm/v7` binaries using musl libc. Physical execution on the Qualcomm IPQ8064 dual-core processor must be validated to confirm that the CGO SQLite library (`go-sqlite3`) loads without dynamic linker or alignment faults on RouterOS v7.25.
2. **USB 3.0 Storage Bus Stability:** On the RB3011, SQLite database transactions, WAL flushing, and Xray geodata lookups reside on external USB 3.0 storage. Bus I/O performance and stability under sustained traffic loads must be verified.
3. **Cortex-A15 Cryptographic Performance:** Unlike modern ARM64 processors (which feature hardware ARMv8-A Cryptographic Extensions), the Cortex-A15 on the RB3011 relies on software computation for TLS handshakes and ChaCha20/Poly1305 ciphers. Maximum throughput per core must be empirically measured during acceptance testing.
4. **Device-Mode Physical Authorization:** Enabling container mode on physical RouterOS hardware requires physical presence (pressing the hardware reset button or cycling power within 5 minutes of issuing `/system/device-mode/update container=yes`). Remote, headless deployments cannot complete this step without on-site operator assistance.

---

## 9. Quality Gate Sign-Off & Transition to Phase 3

The genuine 3x-ui v3.11.0 application is technically sound and ready for deployment engineering on MikroTik RouterOS Containers. It requires no source code forks, no feature stripping, and no UI compromises.

The project is formally cleared to proceed to **Phase 3: Deployment Engineering**, following the specifications in `docs/MIKROZIA_PHASE_3_DEPLOYMENT_PLAN.md`.
