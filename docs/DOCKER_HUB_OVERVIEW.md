# Mikrozia — Xray Control Panel for MikroTik RouterOS Container

[![Docker Hub](https://img.shields.io/badge/Docker%20Hub-echodev7%2Fmikrozia-blue.svg?logo=docker)](https://hub.docker.com/r/echodev7/mikrozia)
[![Release](https://img.shields.io/badge/release-v1.0.9--mikrozia-green.svg)](https://hub.docker.com/r/echodev7/mikrozia/tags)
[![RouterOS](https://img.shields.io/badge/RouterOS-7.25%2B-red.svg)](https://mikrotik.com)
[![Architectures](https://img.shields.io/badge/arch-amd64%20%7C%20arm64%20%7C%20armv7-lightgrey.svg)](https://hub.docker.com/r/echodev7/mikrozia/tags)

**Mikrozia** is a production-hardened, multi-architecture control panel for **Xray-core** engineered specifically to run inside the **MikroTik RouterOS Container** subsystem (CHR, x86_64, and ARM RouterBOARDs).

Standard Docker images cannot run reliably on RouterOS due to non-systemd init, ephemeral root filesystems, absent iptables/fail2ban kernel modules, and missing persistent mounts. Mikrozia solves these hurdles by providing a lightweight Alpine base, zero container-level firewall pollution, persistent volume mounts, a pinned in-image ACME client with persistent TLS certificate storage, and a seamless RouterOS runtime contract.

---

## Published Image & Tags

Docker Hub Repository:

```text
echodev7/mikrozia
```

| Tag                                 | Recommended Use            | Notes                             |
| ----------------------------------- | -------------------------- | --------------------------------- |
| `echodev7/mikrozia:v1.0.9-mikrozia` | **Production Deployments** | Pinned, immutable release tag.    |
| `echodev7/mikrozia:latest`          | Quick testing / Labs       | Tracks the newest stable release. |

### Supported Architectures

- **`linux/amd64`** — MikroTik Cloud Hosted Router (CHR) and x86_64 hardware.
- **`linux/arm64/v8`** — 64-bit ARM RouterBOARDs (CCR2004, CCR2116, CCR2216, RB5009, etc.).
- **`linux/arm/v7`** — 32-bit ARM RouterBOARDs (hAP ax2, hAP ax3, RB4011, etc.).

Every architecture above is built for its own platform, started, and checked against the runtime contract (version labels, bundled binaries, Geo checksums, no firewall tooling) by the release workflow before the multi-architecture manifest is published. CHR x86_64 is the reference environment and beginner path; physical RouterBOARD validation belongs to the Phase 1 acceptance matrix and is not claimed here.

The versioned OCI image is the only release artifact. Mikrozia does not publish binary tarballs, OS packages, or systemd installers for any Mikrozia release.

---

## Runtime Contract & Persistent Storage

Mikrozia enforces strict isolation between application code and persistent user state:

| Property                        | Value                      | Description                                                                  |
| ------------------------------- | -------------------------- | ---------------------------------------------------------------------------- |
| **Entrypoint**                  | `/app/DockerEntrypoint.sh` | Hardened Alpine init without systemd                                         |
| **Working Directory**           | `/app`                     | Mikrozia binary and management scripts                                       |
| **Persistent Database**         | `/etc/x-ui`                | Stores `x-ui.db` (clients, inbounds, settings)                               |
| **Persistent TLS Certificates** | `/root/cert`               | Issued and imported certificates plus ACME state; survives image replacement |
| **User Home**                   | `/root`                    | Preserves SSH/environment consistency                                        |
| **Internal Panel Port**         | `2053/TCP`                 | Default Mikrozia web admin UI                                                |
| **Internal Sub Port**           | `2096/TCP`                 | Default subscription service endpoint                                        |
| **Container Firewall**          | _None_                     | All firewall/NAT is handled exclusively by RouterOS                          |

### Production TLS Certificate Paths

When configuring the Panel or Subscription TLS in Mikrozia, use these exact installed paths:

```text
/root/cert/vpn.yourdomain.com/fullchain.pem
/root/cert/vpn.yourdomain.com/privkey.pem
```

---

## Prerequisites on MikroTik RouterOS

Before installing Mikrozia, verify that your router meets the following requirements:

1. **RouterOS Version**: `7.25.0` or higher (from the **Stable** channel).
2. **Container Package**: Install the extra `container` package matching your exact RouterOS version (`/system/package/print`).
3. **Device Mode Enabled**: Enable container support and perform a hard power-cycle / cold reboot:
   ```routeros
   /system/device-mode/update container=yes
   # Power off and power on the VM/router from hypervisor or power button
   /system/device-mode/print
   ```
4. **Resources**:
   - Minimum **2 GiB RAM** allocated (4 GiB recommended).
   - Minimum **2 GiB free storage** on your target storage path (e.g. `containers/` or `disk1/`).

---

## Step-by-Step Installation Guide

Execute these commands in the RouterOS CLI (Terminal or WinBox).

> These instructions follow the current RouterOS `7.25` container contract.
> Clean-CHR acceptance for P1-CHR-01 has not been completed yet; validate the
> complete flow on a disposable CHR before production. No real-CHR acceptance is
> claimed here.

### Step 1: Create Directories for Persistent Storage

Create dedicated directories for persistent data, SSL certificates, and temporary download cache. RouterOS creates the versioned root directory during extraction. If a directory already exists, skip its `/file/add` line:

```routeros
/file/add name=containers type=directory
/file/add name=containers/mikrozia type=directory
/file/add name=containers/mikrozia/data type=directory
/file/add name=containers/mikrozia/cert type=directory
/file/add name=containers/mikrozia/tmp type=directory
```

### Step 2: Configure Dedicated Bridge & VETH Network

Mikrozia operates within an isolated container network. By default, we use `172.16.10.0/24`:

- **RouterOS Gateway**: `172.16.10.1`
- **Container IP**: `172.16.10.2`

```routeros
# 1. Create container bridge
/interface/bridge/add name=br-mikrozia comment="[Mikrozia:container] Bridge"

# 2. Assign gateway IP to the bridge
/ip/address/add address=172.16.10.1/24 interface=br-mikrozia comment="[Mikrozia:container] Gateway"

# 3. Create VETH interface for the container
/interface/veth/add name=veth-mikrozia address=172.16.10.2/24 gateway=172.16.10.1 comment="[Mikrozia:container] VETH"

# 4. Attach VETH to the container bridge
/interface/bridge/port/add bridge=br-mikrozia interface=veth-mikrozia comment="[Mikrozia:container] Bridge Port"
```

### Step 3: Configure Mounts and Environment Variables

Define the persistent mounts so your database and certificates survive container updates:

```routeros
# Persistent Volume Mounts
/container/mounts/add list=MIKROZIA_DATA src=containers/mikrozia/data dst="/etc/x-ui" comment="[Mikrozia:container] Data Mount"
/container/mounts/add list=MIKROZIA_CERT src=containers/mikrozia/cert dst="/root/cert" comment="[Mikrozia:container] Cert Mount"

# Environment Variables
/container/envs/add list=MIKROZIA_ENV key=XUI_PORT value="2053" comment="[Mikrozia:container] Environment"
/container/envs/add list=MIKROZIA_ENV key=HOME value="/root" comment="[Mikrozia:container] Environment"
/container/envs/add list=MIKROZIA_ENV key=MIKROZIA_ADMIN_USERNAME value="YOUR_ADMIN_USERNAME" comment="[Mikrozia:container] Environment"
/container/envs/add list=MIKROZIA_ENV key=MIKROZIA_ADMIN_PASSWORD value="YOUR_STRONG_PASSWORD" comment="[Mikrozia:container] Environment"
```

> **Sensitive values**: the RouterOS Container Environment is a configuration
> store, not a secret store. Every value in `/container/envs` is visible to
> anyone who can read or export the router configuration. Choose a strong
> administrator password, protect access to the router and its backups, and
> rotate the password if that configuration ever left your control. Mikrozia
> never prints these values to logs, never returns them through an API, and
> never writes them into the image.

`MIKROZIA_ADMIN_USERNAME` and `MIKROZIA_ADMIN_PASSWORD` are the authoritative
source of the panel administrator account: they must be set **together**, and the
panel refuses to start when only one of them is present. An existing account is
reconciled with them on every start and is never silently deleted or reset.

### Step 4: Configure Docker Hub Registry and Add Container

Set the official registry and create the container object using version `v1.0.9-mikrozia`:

```routeros
# Set registry and temporary directory for layer extraction
/container/config/set registry-url="https://registry-1.docker.io" tmpdir=containers/mikrozia/tmp

# Pull image and add container
/container/add remote-image="echodev7/mikrozia:v1.0.9-mikrozia" \
  interface=veth-mikrozia \
  root-dir=containers/mikrozia/root-v1.0.9-mikrozia \
  mountlists="MIKROZIA_DATA,MIKROZIA_CERT" \
  envlist=MIKROZIA_ENV \
  dns="1.1.1.1,8.8.8.8" \
  memory-high=512M \
  memory-max=1024M \
  logging=yes \
  start-on-boot=yes \
  comment="[Mikrozia:container] Container"
```

### Step 5: Monitor Extraction and Start Container

RouterOS will pull and extract image layers. Monitor progress:

```routeros
/container/print detail
/log/print follow-only where topics~"container"
```

When status changes from `extracting` to `stopped`, start the container:

```routeros
/container/start [find where comment="[Mikrozia:container] Container"]
```

Verify that the status is now `running`:

```routeros
/container/print detail where comment="[Mikrozia:container] Container"
```

---

## Required Network Inputs (operator-owned)

> **Important**: Mikrozia never creates, changes, generates, or automates RouterOS
> firewall, NAT, masquerade, port-forwarding, routing-policy, address-list, or
> access-control rules. Mikrozia does not provide RouterOS firewall/NAT commands.
> Every network-policy decision belongs to the RouterOS administrator.

Whether the container needs masquerade, inbound port forwarding, firewall
filtering, routing, or address lists — and how they are implemented — is the
administrator's decision alone. Mikrozia documents only the inputs to evaluate:

- **Panel port**: the web admin panel listens on `2053/TCP` by default.
- **Subscription port**: the subscription service listens on `2096/TCP` by default.
- **User inbound ports**: every port you assign to a user inbound in the panel needs the same evaluation.
- **Container outbound connectivity**: the container needs outbound reachability to resolve DNS and to reach the proxy destinations you configure.
- **Source restrictions and interfaces**: which sources may reach the panel or subscription service, and which interfaces you route through, are administrator-owned choices.

Mikrozia ships no firewall tooling and produces no network policy. Evaluate the
inputs above against your own requirements before exposing any port.

---

## First Login & Credential Management

### The Environment Is the Source of Truth

The panel administrator account is owned by the RouterOS Container Environment.
`MIKROZIA_ADMIN_USERNAME` and `MIKROZIA_ADMIN_PASSWORD` must be set **together**;
on every start they are reconciled onto the stored account.

| Environment state                      | Behaviour                                                                  |
| -------------------------------------- | -------------------------------------------------------------------------- |
| Both values set                        | The stored account is reconciled to exactly these values on every start.   |
| Only one value set                     | The panel refuses to start and names the incomplete variable.              |
| Neither set, an account already exists | The existing account is preserved unchanged; the panel only logs a notice. |
| Neither set, no account exists         | The panel refuses to start and asks for both variables.                    |

There is **no random one-time credential printed to the log** and no fixed
`admin/admin` default. A stored account that still holds the legacy default
credential is kept, but the panel refuses to start until the environment
provides a replacement.

### Reading the Current Values

```routeros
/container/envs/print where list=MIKROZIA_ENV
```

> **Sensitive values**: the RouterOS Container Environment is a configuration
> store, not a secret store. Every value in `/container/envs` is visible to
> anyone who can read or export the router configuration. Choose a strong
> administrator password, protect access to the router and its backups, and
> rotate the password if that configuration ever left your control. Mikrozia
> never prints these values to logs, never returns them through an API, and
> never writes them into the image.

### Changing the Administrator Password

Change the environment value and restart the container:

```routeros
/container/envs/set [find where list=MIKROZIA_ENV and key=MIKROZIA_ADMIN_PASSWORD] value="NEW_STRONG_PASSWORD"
/container/restart [find where comment="[Mikrozia:container] Container"]
```

While the environment owns the account, a password change from the panel UI or
from the legacy shell command is refused so that two sources of truth cannot
compete:

```sh
/app/x-ui setting -username admin -password 'YourNewSecurePassword!'
# Refusing to change the administrator credential: MIKROZIA_ADMIN_USERNAME and
# MIKROZIA_ADMIN_PASSWORD in the container environment own it.
```

> **RouterOS Container Lifecycle Note**: Inside the container, `x-ui restart` reloads the panel web service (`SIGHUP`) and restarts Xray (`SIGUSR1`) without restarting the container itself; `x-ui restart-xray` restarts only Xray. Commands such as `x-ui start`, `x-ui stop`, `x-ui enable`, and `x-ui disable` print the corresponding RouterOS `/container/...` commands (including `start-on-boot=yes|no restart-policy=always|no`) because full container lifecycle is managed by RouterOS.

---

## TLS Certificates

The image ships a pinned, checksum-verified ACME client, so certificates can be issued and renewed inside the container without installing anything at runtime. Two methods are supported:

1. **HTTP-01 (manual)** — needs public TCP/80 to reach the container while the issuance runs. You own that reachability: Mikrozia never creates, changes, or removes RouterOS firewall, NAT, port-forwarding, or routing rules. Mikrozia cannot verify public reachability from inside the container; local diagnostics are not proof that public TCP/80 works. Renewal is manual.
2. **Cloudflare DNS-01 (automatic)** — needs no public port. A least-privilege Cloudflare API token (`Zone → DNS → Edit`, `Zone → Zone → Read`, one zone only) is read from a hidden prompt or deliberately from the `MIKROZIA_CF_API_TOKEN` environment variable, and is stored with mode `0600` on the persistent mount. RouterOS Environment values are visible in the router configuration. The panel checks its DNS-01 domains twice a day.

Both methods store everything on the persistent `/root/cert` mount:

- `/root/cert/<domain>/fullchain.pem`
- `/root/cert/<domain>/privkey.pem`
- `/root/cert/.acme.sh/` (opaque ACME account and renewal state)

Use the shell menu `14` (TLS Certificates) or the CLI:

```bash
x-ui acme status
x-ui acme check --http --domain panel.example.com
x-ui acme issue --http --domain panel.example.com
x-ui acme cloudflare setup --domain panel.example.com
x-ui acme issue --dns --domain panel.example.com
```

For deliberate noninteractive token input, use `MIKROZIA_CF_API_TOKEN` or one token line with explicit `--stdin`; generic inherited standard input is rejected. The token is never a command-line argument.

A changed, validated certificate pair updates the panel and subscription TLS paths and reloads the panel, subscription listener, and Xray exactly once. If installation, validation, settings update, or reload fails, the previous pair and all four previous paths are restored and the error is reported. An unchanged renewal changes nothing.

An existing certificate can still be imported by path with `x-ui cert -webCert … -webCertKey …`, and the panel TLS paths stay editable in Panel Settings. For a manual file replacement outside the ACME flow, run `x-ui restart` to load it.

Mikrozia never creates RouterOS firewall or NAT rules for certificate validation; configure any required port access yourself.

---

## Safe Upgrade Procedure

When upgrading to a new Mikrozia version, never overwrite an active rootfs. Follow this zero-downtime-ready procedure:

1. **Backup SQLite Database and Certificates**:

   ```routeros
   /container/stop [find where comment="[Mikrozia:container] Container"]
   ```

   Copy `containers/mikrozia/data/x-ui.db` and `containers/mikrozia/cert/` to a safe location via WinBox Files or SFTP.

2. **Add New Container**:
   Create a new container with the new version tag and a separate `root-dir` (e.g. `containers/mikrozia/root-v2-mikrozia`), referencing the same `MIKROZIA_MOUNTS`.

3. **Start and Validate**:
   Start the new container. Since the SQLite database is mounted into `/etc/x-ui`, all clients, traffic quotas, and inbounds load immediately.

---

## Troubleshooting & Diagnostics

- **Container status stuck at `extracting`**:
  Verify sufficient free disk space in `tmpdir` and available RAM. RouterOS unpacks layers in memory and temporary storage.
- **DNS resolution fails inside container**:
  Ensure the container definition includes `dns="1.1.1.1,8.8.8.8"` and the RouterOS firewall allows outbound UDP/TCP port 53.
- **Cannot connect to Web Panel**:
  1. Check if container is running: `/container/print detail`.
  2. Verify listening sockets inside container:
     ```routeros
     /container/shell [find where comment="[Mikrozia:container] Container"]
     # Inside shell:
     ss -lntup
     exit
     ```
  3. Verify RouterOS DST-NAT and Forward filter rules are active and not blocked by earlier drop rules.
- **Inspecting Panel & Container Logs**:
  - Panel Application Logs are visible in the Web UI (**Logs**) from the in-memory buffer (`journalctl`/systemd syslog is not used in RouterOS Container).
  - Container `stdout`/`stderr` logs are viewed from RouterOS Terminal or WinBox ([MikroTik Container Docs](https://help.mikrotik.com/docs/spaces/ROS/pages/84901929/Container), [MikroTik Log Docs](https://help.mikrotik.com/docs/spaces/ROS/pages/328094/Log)):
    ```routeros
    /container/set [find where comment="[Mikrozia:container] Container"] logging=yes
    /log/print follow-only where topics~"container"
    ```
  - **Xray Access Logs Privacy**: Access logs may record client source IPs, destinations, and emails. Restrict panel access and review retention settings before enabling or sharing them.

---

## Upstream & License

- **Upstream 3x-ui**: [MHSanaei/3x-ui](https://github.com/MHSanaei/3x-ui)
- **Core Engine**: [XTLS/Xray-core](https://github.com/XTLS/Xray-core)
- **License**: GNU General Public License v3.0 (GPL-3.0)
