# Mikrozia RouterOS API Integration Reference

Status: dashboard health, resource telemetry, bounded RouterOS Container log
reads, opt-in single-container restart, and opt-in Managed RouterOS NAT
reconciliation implemented; RouterOS VETH telemetry remains deferred.

## Purpose and scope

Mikrozia is a RouterOS Container product. Its panel can observe processes and
interfaces visible inside its Linux container, but that is not the same as
observing RouterOS. The planned RouterOS integration supplies an explicit,
operator-configured connection from Mikrozia to its hosting router.

The chosen integration is the **native RouterOS API**. Mikrozia will not add a
separate controller, agent, proxy protocol, cloud service, or RouterOS script
as a requirement.

This document is a maintained engineering reference, not a copied replacement
for MikroTik's documentation. RouterOS versions and API behavior remain
authoritative in the official documentation linked below.

## Accepted decisions

- The client speaks the native RouterOS API, whose commands map closely to
  RouterOS CLI commands.
- The integration uses direct internal plain native `api` on TCP/8728 only for
  the isolated container-router network. It does not use TLS-wrapped API, REST,
  or an extra agent. Resource and log queries are read-only; container restart
  is a separate, explicitly configured operation.
- The current dashboard integration performs one read-only authenticated
  `/system/resource/print` query to verify API health and collect RouterOS
  resource telemetry (`uptime`, `cpu-load`, `cpu-count`, `cpu-frequency`,
  `total-memory`, `free-memory`). It does not modify router configuration.
- The resource connection configuration source is `MIKROZIA_ROUTEROS_API_HOST`,
  `MIKROZIA_ROUTEROS_API_USERNAME`, and `MIKROZIA_ROUTEROS_API_PASSWORD` in the
  container environment. All three must be non-empty (or all three unset). The
  host must be a validated literal private IPv4 or IPv6 address (no hostnames,
  zones, loopback, or link-local addresses); the client uses only TCP/8728.
- RouterOS firewall, NAT, routing, access-control, users, services and logging
  remain operator-owned by default. When the administrator explicitly enables
  **Managed RouterOS NAT Integration** in Panel Settings, Mikrozia reconciles
  only its own marked `/ip/firewall/nat` (`dst-nat`) rules
  (`comment="mikrozia:managed:inbound:<id>:<proto>"`) using a dedicated
  operator-supplied RouterOS API account (`read,write,policy,api`), with conflict
  detection against foreign rules and compensating rollback on partial failure.
- Container restart is disabled until the operator provides an exact unique
  container comment and a separate operational API account through
  `MIKROZIA_ROUTEROS_RESTART_USERNAME` and
  `MIKROZIA_ROUTEROS_RESTART_PASSWORD`. The panel never uses the read-only
  resource account for a write command.
- Missing, invalid, unreachable, or unauthorized configuration produces a
  truthful status; it never falls back to guessed RouterOS metrics.

## Internal native API transport

The owner has accepted direct native `api` on TCP/8728 for the isolated internal
path between the Mikrozia container and its router. The API login carries the
password in the login request on current RouterOS versions, so the RouterOS
administrator must restrict the service to the container-router network and
accept this credential-transport risk. It must never be exposed to WAN.

The API protocol is sentence-based: a command and attributes are sent as
length-prefixed words, terminated by an empty word. Replies end in `!done` and
errors use `!trap`. The implementation must use a reviewed Go RouterOS API
client or a small tested native client; dashboard code must never construct
unvalidated sentences directly.

## Operator-side prerequisites

The RouterOS administrator, not Mikrozia, is responsible for all of these:

1. Create one dedicated RouterOS user with the minimum read permissions needed
   for the selected telemetry.
2. Enable `api` and restrict its `address` property to the container subnet or
   exact container IP.
3. Ensure the container can reach the router's internal gateway on TCP/8728.
4. Provide the private router address, username, and password through the three
   supported environment variables. Use placeholders only in deployment notes;
   never put working credentials in documentation or command history.

RouterOS documents service access restriction through `/ip service`'s `address`
property. It denies service access for source addresses outside the configured
prefix; it does not make a public service safe by itself. The router remains
responsible for its own network policy.

## Current dashboard query and telemetry

The status endpoint coalesces viewers into at most one probe per 15 seconds,
with a 4-second connection/query deadline and a 30-second stale threshold. The
client returns only allowlisted state codes, not endpoint details or RouterOS
peer error text. Its only post-login sentence is `/system/resource/print` with
`=.proplist=uptime,cpu-load,cpu-count,cpu-frequency,total-memory,free-memory`;
no arbitrary command interface exists. `connected` is returned only after a
valid single-record reply, valid `cpu-load`, and `!done`.

RouterOS CPU and RAM history keep fresh-only samples for up to 60 samples or 15
minutes, and are cleared on RouterOS reboot (uptime decrease) or when the API
configuration is removed or invalid. On the Dashboard CPU and RAM cards,
RouterOS CPU and RouterOS RAM are the primary metrics; secondary `Container`
lines report container-scoped CPU (unavailable when host-scoped) and cgroup
v1/v2 Container RAM (`used / limit`, `used · No limit`, or `—`), never falling
back to guessed host metrics when unavailable.

The `Container History` CPU and RAM charts use separate `containerCpu` and
`containerMem` series. They accept only cgroup-scoped samples; RAM requires a
finite cgroup limit to calculate a percentage. Earlier host-backed history is
not relabeled as container history. Other tabs retain their documented scopes:
container network sockets, container root filesystem usage, Xray online
clients, and kernel-reported disk I/O with a host-scope warning.

The top bar distinguishes `not_configured`, `invalid_configuration`,
`connecting`, `connected`, `authentication_failed`, `permission_denied`, and
unreachable/query/reply failure states (`unreachable`, `query_failed`,
`invalid_reply`). A previous result is marked stale when its age exceeds the 30s
threshold. Password, username and endpoint are excluded from the response.

RouterOS Container logs use a separate, optional read path. The operator sets
`MIKROZIA_ROUTEROS_CONTAINER_COMMENT` to the exact unique comment of the
Mikrozia container (the installation guide uses
`[Mikrozia:container] Container`). The panel queries `/container/print` for
that comment and accepts exactly one record with a nonempty `name` and `.id`.
It then reads `/container/log/print` with the verified name and checks the
`container` field on every returned record. The endpoint returns at most 100
recent records, and the RouterOS request has a four-second deadline. If the
identity is missing or ambiguous, logging is disabled, or permissions are
insufficient, the UI shows a specific unavailable state. It never enables
logging or changes RouterOS settings. WinBox on RouterOS 7.25.1 confirmed
`/container/log/print` records carry `time`, `container`, and `message`.

RouterOS 7.25.1 does not pass the 35-character canonical environment-variable
name into the container process. On that release, use the equivalent shorter
`MIKROZIA_ROS_CONTAINER_COMMENT`; the canonical name remains first priority
where it is available.

The optional Restart Container credentials also require the shorter
`MIKROZIA_ROS_RESTART_USERNAME` and `MIKROZIA_ROS_RESTART_PASSWORD` aliases
on RouterOS 7.25.1. Canonical credentials take priority when their complete
pair is available; partial canonical configuration fails closed.

Restart Container checks the unique comment with the read-only account, then
rechecks the same `.id`, `name`, comment, and running status with the separate
operational account immediately before issuing exactly one
`/container/restart =.id=<verified-id>` sentence. There is no automatic retry.
The panel runs inside that container, so the API connection may close before
RouterOS acknowledges the command. In that case the response is `uncertain`,
and the UI says so and reloads later. A confirmed `!done` is `accepted`.
Missing credentials, duplicate or changed identity, a stopped container, or a
permission error fail closed. No other RouterOS object is touched.

RouterOS native API does not provide a per-command container-restart-only user
policy. The operator must restrict the operational account and API service to
the internal container IP and handle its credentials as sensitive; the panel
does not create that account or widen RouterOS service access.

## Explicit non-goals for this task

- No TLS-wrapped API, REST API, managed RouterOS integration, panel credential
  store, or firewall/NAT/routing/address-list/access-control mutation.
- No RouterOS VETH counters, automatic logging configuration, or automatic
  RouterOS API user/service changes.
- No automatic API service enablement, RouterOS user creation, or logging change.

## Explicit non-goals for the first dashboard implementation

- No automatic creation of API users, certificates, or `/ip service` settings.
- No exposure of the RouterOS API on WAN as a Mikrozia installation step.
- No credentials in environment examples, screenshots, Docker layers, logs,
  database exports, or telemetry payloads.
- No use of the existing multi-node panel abstraction as a substitute for a
  RouterOS connection. A RouterOS router is not an Xray child node.

## Opt-in Managed RouterOS NAT integration

Managed RouterOS NAT is off by default (`routerosNatEnable=false`) and
configured under **Panel Settings → RouterOS NAT** (`/panel/settings#routeros-nat`).

- **Scoped ownership**: Every managed rule is tagged with
  `comment="mikrozia:managed:inbound:<inboundID>:<proto>"` (`tcp` or `udp`).
  Unmarked or foreign rules are never adopted, modified, or deleted.
- **Lifecycle**: Adding or editing a local inbound reconciles its required
  `dst-nat` rule(s); disabling an inbound sets `disabled=yes` on its owned
  rule(s) without deleting them; deleting an inbound (or moving it to
  loopback/remote node) removes only its owned rule(s).
- **Safety**: Only validated private IPv4/IPv6 hosts (`172.16.10.1` example)
  and private IPv4 container targets (`172.16.10.2` example) are accepted.
  Enabled foreign `dstnat` rules on the same protocol and port fail closed with
  a conflict error; multi-rule write failures roll back newly created rules and
  surface the error truthfully to the operator.
- **API endpoints**: `POST /panel/api/setting/testRouterosNat`,
  `POST /panel/api/setting/syncRouterosNat`, and
  `GET /panel/api/setting/detectContainerIp`.

## Remaining future design and verification gates

1. Live CHR acceptance must confirm that the dedicated read-only API account
   can query `/container/print` and `/container/log/print` for the configured
   container comment, and that UI records match WinBox Container Log.
2. Live CHR health-query acceptance requires a separately authorized test with
   the exact environment variables configured by the owner.

## Official references

- [RouterOS API](https://help.mikrotik.com/docs/spaces/ROS/pages/47579160/API)
- [RouterOS Services](https://help.mikrotik.com/docs/spaces/ROS/pages/103841820/Services)
- [RouterOS VETH](https://help.mikrotik.com/docs/spaces/ROS/pages/350584877/VETH)
- [RouterOS Container](https://help.mikrotik.com/docs/spaces/ROS/pages/84901929/Container)

The linked documentation site notes that it has been frozen and points readers
to MikroTik's current manual. Before implementation, use the current manual
for the deployed RouterOS release and record the exact version tested.
