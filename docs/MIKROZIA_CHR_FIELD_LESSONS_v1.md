# MIKROZIA — CHR FIELD TESTING LESSONS & REAL-WORLD ACCEPTANCE (v1.0)

**Document Status:** HISTORICAL FIELD RECORD — SUPERSEDED FOR INSTALLATION
**Current Policy:** Firewall/NAT examples in this record are not part of Mikrozia installation. Use `MIKROZIA_CHR_INSTALL_FA.md`; network policy is operator-owned. Container-side ACME installation and automatic certificate renewal described below were retired; certificates are user-managed and configured by path only.
**Test Date:** November 1, 2026
**Test Platform:** MikroTik Cloud Hosted Router (CHR) x86_64 / AMD64
**RouterOS Version:** 7.23.2
**Infrastructure Host:** Hetzner Cloud
**Tested Release:** `majlot/mikrozia:v3.11.0-mikrozia.1`
**Upstream Baseline:** `MHSanaei/3x-ui v3.11.0` (commit `c621ba9014a6aa21fd7c9ecf45655e44e24ebf13`)

---

## 1. Executive Summary & Acceptance Results

The first real-world deployment of Mikrozia inside a MikroTik RouterOS container was executed on a Hetzner Cloud Hosted Router (CHR). The test verified the deployment lifecycle end-to-end: pulling the multi-architecture image from Docker Hub, instantiating the container, accessing the web administration interface, provisioning TLS certificates via Let's Encrypt, establishing an HTTPS subscription service, and connecting a mobile client through an Xray VLESS proxy inbound with live internet traffic.

### Confirmed Acceptance Matrix (Hetzner CHR x86_64)

| Test Item   | Description                                        | Status   | Evidence / Observation                                                                            |
| :---------- | :------------------------------------------------- | :------- | :------------------------------------------------------------------------------------------------ |
| **TEST-01** | Pull multi-architecture image from Docker Hub      | **PASS** | Successfully pulled `majlot/mikrozia:v3.11.0-mikrozia.1` via `/container/add remote-image=...`    |
| **TEST-02** | Layer extraction & container startup on ROS 7.23.2 | **PASS** | Extracted to CHR virtual disk; status transitioned from `extracting` to `stopped`, then `running` |
| **TEST-03** | 3x-ui panel & Xray-core daemon initialization      | **PASS** | Web server bound to internal port `:2053`, Xray v26.10.21 process spawned                         |
| **TEST-04** | Remote panel access via RouterOS DST-NAT           | **PASS** | Web UI reached through public port forwarding                                                     |
| **TEST-05** | Let's Encrypt SSL issuance via ACME HTTP-01        | **PASS** | Issued valid certificate for domain (`test.parsiserver.com`) via standalone ACME server           |
| **TEST-06** | Panel TLS 1.3 configuration                        | **PASS** | Panel reconfigured to serve HTTPS over TLS 1.3 using issued certificates                          |
| **TEST-07** | Secure Web UI loading over HTTPS                   | **PASS** | `https://test.parsiserver.com:8443/` returned HTTP 200 with valid TLS cert chain                  |
| **TEST-08** | Subscription endpoint delivery over HTTPS          | **PASS** | Client configuration subscription accessible securely over HTTPS                                  |
| **TEST-09** | Mobile client provisioning & handshake             | **PASS** | Mobile VLESS client connected successfully using imported subscription                            |
| **TEST-10** | Live traffic tunneling & browsing                  | **PASS** | Active multi-protocol traffic passed through VLESS inbound; websites & apps loaded                |

> [!IMPORTANT]
> **Scope Invariant:** These verified results confirm functionality on **MikroTik CHR (x86_64)**. They do NOT represent physical ARM32 / ARM64 hardware validation, which requires on-device testing with external storage.

---

## 2. Test Environment Specifications

- **Hypervisor / Provider:** Hetzner Cloud (KVM)
- **Architecture:** `x86_64` (AMD64)
- **Compute Resources:** 2 vCPU, 4000 MiB RAM
- **Storage Allocation:** Primary virtual disk partition (no secondary disk attached)
- **RouterOS Package:** `routeros 7.23.2`, `container 7.23.2`
- **Device Mode:** `container: yes` (authorized via cold reboot)
- **Networking Topology:**
  - Container Subnet: `172.16.10.0/24`
  - Container Interface: `veth-mikrozia` (`172.16.10.2/24`, gateway `172.16.10.1`)
  - Router Bridge: `br-mikrozia` (`172.16.10.1/24`)
  - Outbound NAT: `masquerade` on `src-address=172.16.10.0/24`

---

## 3. Discovered Defects, Root Causes & Tested Workarounds

During the real deployment, several critical operational differences between generic Linux/Docker hosts and RouterOS Alpine containers were identified.

### DEFECT 1: Broken acme.sh Paths in Alpine Container

- **Symptom:** Selecting the SSL certificate menu option in `x-ui` resulted in:
  ```
  /usr/bin/x-ui: line 1989: /root/.acme.sh/acme.sh: No such file or directory
  ```
- **Root Cause:** When `curl -s https://get.acme.sh | sh` ran during initial installation, the container runtime environment had `HOME=/` or unassigned `$HOME`, causing acme.sh to install into `/.acme.sh/`. The `x-ui.sh` management script and `DockerEntrypoint.sh` expected `/root/.acme.sh/`.
- **Field Workaround:**
  ```sh
  mkdir -p /root
  ln -s /.acme.sh /root/.acme.sh
  ```
- **Permanent Solution:**
  1. Set `ENV HOME=/root` explicitly in `Dockerfile`.
  2. Implement automated bidirectional symlinking in `DockerEntrypoint.sh` linking both `/root/.acme.sh` and `/.acme.sh` to persistent storage.

---

### DEFECT 2: Certificate & ACME State Persistence Across Container Updates

- **Symptom:** Upgrading or recreating the container preserved `/root/cert` (installed public/private keys) and `/etc/x-ui` (database), but wiped out `/.acme.sh/` (ACME account keys, renewal parameters, and domain metadata).
- **Root Cause:** Only `/root/cert` and `/etc/x-ui` were mounted to RouterOS host storage. ACME renewal state remained inside the transient container root filesystem.
- **Permanent Solution:**
  Store `.acme.sh` inside the mounted certificate directory at `/root/cert/.acme.sh`. On startup, `DockerEntrypoint.sh` detects if `/root/cert/.acme.sh` exists, migrates legacy transient data if found, and symlinks `/root/.acme.sh` and `/.acme.sh` directly to `/root/cert/.acme.sh`.

---

### DEFECT 3: Unattended Certificate Renewal

- **Symptom:** Certificates issued by Let's Encrypt expire in 90 days (renewal starts automatically at 60 days). The cron entry generated by `acme.sh` was stored in the container's root crontab, but `crond` was not guaranteed to run, and HTTP-01 validation requires continuing public access to port 80.
- **Permanent Solution:**
  1. `DockerEntrypoint.sh` ensures `crond` is started at boot, and `acme.sh --install-cronjob` is checked and re-registered on every container start so crontab survives container recreation.
  2. Errors during cron startup or crontab registration are logged explicitly without silent suppression (`>/dev/null 2>&1`).
  3. When renewal succeeds, `acme.sh` installs the new certs under `/root/cert` and invokes `x-ui restart` (via `reloadcmd`), sending `SIGHUP` and `SIGUSR1` to reload the panel, subscription listener, and Xray inbounds without stopping the container process.
  4. For HTTP-01, public TCP port 80 DST-NAT forwarding must remain **permanently active** on RouterOS; if disabled after initial issuance, unattended renewal at day 60 will fail.
  5. Cloudflare DNS-01 validation (x-ui option 21) is strongly recommended for fully unattended renewal as it validates via DNS TXT records and requires zero open inbound ports.

---

### DEFECT 4: Alpine Shell Script Incompatibilities

- **Observed Errors:**
  - `grep: /var/log/messages: No such file or directory`: Triggered when viewing logs via `x-ui` menu on Alpine.
  - `Cannot parse _ssldate2time ...`: BusyBox `date` utility cannot parse RFC/OpenSSL certificate date strings.
  - `sed: write error`: BusyBox `sed` buffer limitations during in-place certificate updates.
  - `ls: cert/*: No such file or directory`: Relative path bug in `x-ui.sh` (lines 1943, 1949).
- **Root Cause:** Standard Alpine images bundle minimal BusyBox core utilities rather than full GNU coreutils.
- **Permanent Solution:**
  1. Add `coreutils` and `socat` to `Dockerfile` APK package list.
  2. Fix relative path in `x-ui.sh`: use `${certPath}/*` instead of `cert/*`.
  3. Guard log viewer in `x-ui.sh` to check for file existence before running `grep`.

---

### DEFECT 5: Public Port Mapping vs. Internal Ports

- **Symptom:** Standard ports were filtered or throttled on the client's network:
  - Public port `2053` (default panel) was unreachable / timed out.
  - Public port `2096` (default subscription) was unreachable / timed out.
- **Tested Resolution:**
  - Remapped Public Panel: RouterOS DST-NAT `dst-port=8443` &rarr; Container `to-ports=2053`.
  - Remapped Public Subscription: RouterOS DST-NAT `dst-port=8444` &rarr; Container `to-ports=2096`.
  - Remapped Proxy Inbound: RouterOS DST-NAT `dst-port=2083` &rarr; Container `to-ports=2083`.
  - Following this mapping, both web panel, subscription delivery, and VLESS mobile traffic worked flawlessly over HTTPS/TLS.
- **Permanent Solution:**
  Upgrade the deployment generator to independently accept separate public vs. internal ports:
  `--admin-public-port`, `--admin-internal-port`, `--sub-public-port`, `--sub-internal-port`, and flexible proxy port syntax (`<public>:<internal>:<proto>`).

---

### DEFECT 6: Initial Credential Initialization & Recovery

- **Symptom:** On a fresh installation, 3x-ui v3.11.0 generates random credentials (`random.Seq(10)` username and `random.Seq(16)` password) and logs them once. If RouterOS container logs roll over, the operator is unable to retrieve them. Furthermore, running `x-ui setting -show` previously invoked the interactive bash menu instead of the CLI helper, and operators mistakenly believed default credentials were `admin/admin`.
- **Root Cause Analysis:**
  1. `/usr/bin/x-ui` is a management bash script wrapper. It lacked CLI subcommand forwarding for `setting` and `cert`.
  2. The compiled binary `/app/x-ui setting -show` displays panel configuration (port, webBasePath, SSL status, `hasDefaultCredential`), but does NOT print plaintext passwords because credentials are stored as bcrypt hashes in SQLite (`/etc/x-ui/x-ui.db`).
  3. Running `/app/x-ui setting -reset` resets general panel settings (webBasePath, subscription URLs), but does NOT reset the administrator user account.
- **Permanent Solution:**
  1. Updated `x-ui.sh` to forward `setting` and `cert` subcommands directly to `/app/x-ui "$@"`, allowing both `x-ui setting ...` and `/app/x-ui setting ...` to work identically.
  2. Documented the RouterOS log command to capture first-boot generated credentials:
     ```routeros
     /log/print where topics~"container"
     ```
  3. Documented direct credential setting and recovery commands via RouterOS container shell:
     ```sh
     # Inspect panel port, base path, and SSL status:
     /app/x-ui setting -show

     # Set explicit administrator username and password (preserves all inbounds, traffic, and settings):
     /app/x-ui setting -username <CHOSEN_USER> -password <CHOSEN_PASS>

     # If two-factor authentication was locked out:
     /app/x-ui setting -username <CHOSEN_USER> -password <CHOSEN_PASS> -resetTwoFactor=true
     ```

---

### DEFECT 7: External URL Advertising Mismatch

- **Symptom:** ACME script and panel displayed `https://test.parsiserver.com:2053/` instead of the publicly accessible `https://test.parsiserver.com:8443/`.
- **Permanent Solution:**
  Instruct operators to configure the **Public Port** and **Subscription URL** in 3x-ui Panel Settings to match the external RouterOS DST-NAT ports.

---

### DEFECT 8: HTTP-01 Port 80 Forwarding & RouterOS WebFig Conflict

- **Symptom:** ACME HTTP-01 validation failed with `Connection refused` prior to creating a DST-NAT rule for port 80. Furthermore, removing the rule after initial issuance breaks automatic background renewal.
- **Root Cause:** RouterOS WebFig binds to port 80 (`/ip service set www port=...`). If WebFig is listening on port 80 or if port 80 is not forwarded to the container, ACME standalone server cannot answer Let's Encrypt challenges. For unattended renewal to succeed at day 60, port 80 must remain accessible whenever `acme.sh --cron` executes.
- **Permanent Solution:**
  Provide explicit guidance to either:
  a) **HTTP-01 Continuing Inbound Port 80 Requirement:** Change RouterOS WebFig port (`/ip/service/set www port=8080`) and maintain a permanent DST-NAT rule forwarding public TCP port 80 to the container (`/ip/firewall/nat/add chain=dstnat action=dst-nat dst-port=80 protocol=tcp to-addresses=172.16.10.2 to-ports=80 comment="Mikrozia HTTP-01 SSL Renewal"`).
  b) **Cloudflare DNS-01 (Recommended for Firewalled / Port-Restricted Setups):** Use Option 21 in `x-ui`, which stores API credentials securely in `/root/cert/.acme.sh/account.conf` (chmod 0600) and renews silently over Cloudflare DNS TXT records without opening any inbound ports.

---

### DEFECT 9: Modern RouterOS Mount Syntax Guard

- **Requirement:** RouterOS v7.21+ strictly requires `/container/mounts/add list=...` and `/container/add mountlists=...`.
- **Status:** Confirmed verified on RouterOS 7.23.2. All generator scripts and documentation strictly adhere to this syntax.

---

### DEFECT 10: Subscription & Panel TLS Certificate Path Ambiguity

- **Symptom:** Referencing `/root/cert/<domain>/fullchain.cer` and `<domain>.key` in Subscription Settings caused TLS handshake failures or missing file errors because these files did not exist.
- **Root Cause:** Earlier guides confused internal ACME staging files (`.acme.sh/<domain>/fullchain.cer` and `<domain>.key`) with installed production files. In `x-ui.sh`, `acme.sh --installcert` explicitly installs certificates into:
  - Public Fullchain: `/root/cert/<domain>/fullchain.pem`
  - Private Key: `/root/cert/<domain>/privkey.pem`
- **Permanent Solution:**
  1. Standardize all documentation, generator scripts, and setup guides to strictly prescribe `/root/cert/<domain>/fullchain.pem` and `/root/cert/<domain>/privkey.pem` for both Web Panel and Subscription endpoints.
  2. Explicitly separate internal ACME working files (`/root/cert/.acme.sh/`) from production installed certificates (`/root/cert/<domain>/`).
  3. Added regression tests asserting path consistency across all documentation and shell issuance scripts.

---

## 4. Architectural Enhancements for Release Candidate v3.11.0-mikrozia.2

1. **Enhanced Dockerfile:**
   Includes `coreutils`, `socat`, and explicit `ENV HOME=/root`.
2. **Hardened DockerEntrypoint.sh:**
   Automatic ACME state migration into `/root/cert/.acme.sh`, bidirectional symlink creation, cronjob re-registration, and background `crond` activation.
3. **Upgraded Generator:**
   Full decoupled port configuration (`admin_public_port`, `admin_internal_port`, `sub_public_port`, `sub_internal_port`, `proxy_ports`).
4. **Tested Non-Destructive Upgrade Path:**
   Containers can be safely stopped, removed, and replaced with `v3.11.0-mikrozia.2` without data loss or certificate re-issuance.
