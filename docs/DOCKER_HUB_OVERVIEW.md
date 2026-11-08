# Mikrozia Docker Hub Release Policy

Docker Hub repository:

```text
majlot/mikrozia
```

## Tags

Every released source version must have an immutable Docker tag:

```text
v<3x-ui-version>-mikrozia.<revision>
```

Example:

```text
majlot/mikrozia:v3.11.0-mikrozia.4
```

`latest` points to the newest successfully tested release but must not be used for production pinning.

## Architectures

- `linux/amd64` — MikroTik CHR and x86_64;
- `linux/arm64/v8` — supported 64-bit ARM RouterOS devices;
- `linux/arm/v7` — supported ARMv7 RouterOS devices.

## Automated publication

`.github/workflows/docker.yml` is triggered by tags matching:

```text
v*.*.*-mikrozia.*
```

Required repository secrets:

```text
DOCKER_HUB_USERNAME
DOCKER_HUB_TOKEN
```

The pipeline validates the tag, runs compatibility tests, checks shell syntax, rejects RouterOS installers containing firewall commands, builds and smoke-tests `linux/amd64`, then publishes all supported architectures with SBOM, provenance, and a manifest digest.

## Runtime contract

| Property | Value |
|---|---|
| EntryPoint | `/app/DockerEntrypoint.sh` |
| Working directory | `/app` |
| Persistent database | `/etc/x-ui` |
| Persistent certificates/ACME | `/root/cert` |
| HOME | `/root` |
| Internal panel port | `2053/TCP` |
| Internal subscription port | `2096/TCP` |
| Firewall tooling | not included |

Example installed TLS paths:

```text
/root/cert/vpn.yourdomain.com/fullchain.pem
/root/cert/vpn.yourdomain.com/privkey.pem
```

The image does not include Fail2ban, iptables, nftables, or UFW and does not configure RouterOS firewall, NAT, port forwarding, or access policy. Those remain outside the container image and under operator control.

## Publishing a release

```bash
git tag v3.11.0-mikrozia.4
git push origin v3.11.0-mikrozia.4
```

After the workflow succeeds, record the manifest digest in the release notes and update the pinned version used by the RouterOS installer and documentation.
