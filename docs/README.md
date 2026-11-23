<p align="center">
  <a href="https://hub.docker.com/r/echodev7/mikrozia">
    <picture>
      <source media="(prefers-color-scheme: dark)" srcset="public/logo-dark.png" />
      <img src="public/logo-light.png" alt="Mikrozia" width="180" />
    </picture>
  </a>
</p>

<h1 align="center">Mikrozia Documentation</h1>

<p align="center">
  Documentation and product site for <b>Mikrozia</b> — the
  <a href="https://github.com/MHSanaei/3x-ui"><b>3x-ui</b></a> panel and Xray-core
  packaged as an immutable MikroTik RouterOS Container image.
</p>

<p align="center">
  <a href="https://hub.docker.com/r/echodev7/mikrozia"><img src="https://img.shields.io/badge/image-echodev7%2Fmikrozia-2496ED?style=flat-square&logo=docker" alt="Container image" /></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-GPL--3.0-blue?style=flat-square" alt="License: GPL-3.0" /></a>
  <img src="https://img.shields.io/badge/Next.js-16-black?style=flat-square&logo=next.js" alt="Next.js 16" />
  <img src="https://img.shields.io/badge/Fumadocs-16-0ea5e9?style=flat-square" alt="Fumadocs 16" />
</p>

---

## Overview

This directory contains the Mikrozia documentation site built with
[Fumadocs](https://fumadocs.dev) on Next.js. It has **no backend, no database, and
no auth**: every page is prerendered and every tool runs entirely in the browser.
Current guides describe RouterOS Container installation and operation only.

## What's inside

The documentation walks you through Mikrozia on RouterOS from first install to
day-to-day operation:

- **Getting Started** — RouterOS Container installation, first login, and replacing the image.
- **Configuration** — the panel, inbounds, REALITY, transports, clients, subscriptions, and share links.
- **Operations** — reverse proxy, multi-node setups, outbounds & routing, backup/restore, Telegram and Discord bots, and security.
- **Reference** — environment variables, the database, ports & firewall, and the HTTP API.
- **Help** — troubleshooting, FAQ, migration, and how to contribute.

## Interactive tools

The site ships with in-browser helpers that generate configuration for you — **no data
ever leaves your browser**:

| Tool                         | What it does                                            |
| ---------------------------- | ------------------------------------------------------- |
| **REALITY Config Generator** | Build a valid REALITY inbound configuration.            |
| **Share Link Inspector**     | Decode and inspect `vless://` / `vmess://` share links. |
| **Reverse Proxy Generator**  | Generate reverse-proxy configs (Nginx / Caddy).         |
| **Protocol Wizard**          | Pick and configure the right protocol for your needs.   |

## Tech stack

| Layer     | Technology                                                  |
| --------- | ----------------------------------------------------------- |
| Framework | [Next.js 16](https://nextjs.org) (App Router) · React 19    |
| Docs      | [Fumadocs](https://fumadocs.dev) (`-ui` / `-core` / `-mdx`) |
| Styling   | [Tailwind CSS v4](https://tailwindcss.com)                  |
| Search    | [Orama](https://orama.com) static index                     |
| Language  | TypeScript (strict)                                         |
| Tests     | [Vitest](https://vitest.dev) for the pure `lib/xray` logic  |
| Tooling   | pnpm · oxlint · oxfmt                                       |

## Quick start

This project uses **[pnpm](https://pnpm.io)** (npm lockfiles are gitignored). Run everything
from the `docs/` directory:

```bash
cd docs
pnpm install
pnpm dev        # http://localhost:3000
```

Useful scripts:

| Script           | Description                                 |
| ---------------- | ------------------------------------------- |
| `pnpm dev`       | Start the dev server                        |
| `pnpm build`     | Production build (also typechecks)          |
| `pnpm typecheck` | Generate MDX/route types and `tsc --noEmit` |
| `pnpm lint`      | Run oxlint (`.oxlintrc.json`)               |
| `pnpm test`      | Run unit tests (Vitest)                     |

See [`CONTRIBUTING.md`](./CONTRIBUTING.md) for the full list and project conventions.

## Project structure

```
app/             # Next.js App Router — layouts, home, docs, OG images, search, llms.txt
components/      # React components — interactive tools, home sections, MDX bindings
content/docs/    # MDX documentation, one folder per locale (en · fa · ru · zh)
lib/             # source config, i18n, GitHub stats, and the unit-tested lib/xray logic
public/          # static assets — logos, favicon, openapi.json, CNAME
scripts/         # build-time scripts (API reference generation)
source.config.ts # Fumadocs MDX schema & collection config
next.config.mjs  # Next.js config (static-export gating)
proxy.ts         # i18n middleware
```

## Internationalization

Documentation is authored in **English**. Persian (`fa`, RTL), Russian (`ru`), and
Chinese (`zh`) locales are wired up; untranslated pages fall back to English so they
never 404. English URLs are unprefixed; other locales live under `/fa`, `/ru`, `/zh`.

## Deployment

The site builds for two targets:

- **Vercel / Node** — `pnpm build` (static search index + prerendered OG images).
- **GitHub Pages (static export)** — `DEPLOY_TARGET=static pnpm build` → `out/`.

## Contributing

Contributions are welcome! Setup, scripts, and project conventions live in
[`CONTRIBUTING.md`](./CONTRIBUTING.md).

## License

Licensed under [GPL-3.0](./LICENSE).
