# Mikrozia — Administrator Credential & RouterOS-Native ACME Specification

سند مرجع Mikrozia برای «اعتبارنامهٔ ادمین از طریق Environment» و «صدور/تمدید
گواهی به‌صورت RouterOS-native و پایدار».

This document is the authoritative corrected contract for task
`P1-AUTH-ACME-03`. It defines two product decisions:

1. **Administrator credentials are owned by the RouterOS Container Environment**
   (`MIKROZIA_ADMIN_USERNAME` / `MIKROZIA_ADMIN_PASSWORD`).
2. **Certificate issuance is a supported Mikrozia feature again**, with exactly two
   official methods: HTTP-01 (manual) and Cloudflare DNS-01 (automatic renewal),
   both writing persistent artifacts under `/root/cert`.

Status: `P1-AUTH-ACME-01` is not accepted. `P1-AUTH-ACME-02` implements the
corrections and remains **awaiting project-manager review**; it is not accepted
or complete from source-level tests alone. Where this document and
`docs/MIKROZIA_PROJECT_LEDGER.md` disagree, the ledger records task status and
this document records the technical contract. Where either conflicts with
`docs/MIKROZIA_PROJECT_CHARTER.md`, the charter wins.

---

## 0. Historical pre-implementation baseline (superseded)

این بخش فقط وضعیت تاریخی کد پیش از `P1-AUTH-ACME-01` را ثبت می‌کند؛ این رفتارها
دیگر قرارداد محصول جاری نیستند.

| Area                      | Historical state before P1-AUTH-ACME-01                                                                                                                               |
| ------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| First admin account       | `internal/database/db.go` → `EnsureFirstUser()` mints `random.Seq(10)` / `random.Seq(16)` and returns them in plaintext.                                              |
| First-login disclosure    | `main.go` → `log.Printf("No panel user existed, so one was generated. Username: %s  Password: %s  (shown once; ...)", username, password)`.                           |
| Panel login check         | `internal/web/service/panel/user.go` → `HasDefaultCredential()` compares against the literal `admin`/`admin`.                                                         |
| CLI credential write      | `main.go` → `x-ui setting -username/-password` → `userService.UpdateFirstUser()`.                                                                                     |
| Panel UI credential write | `internal/web/controller/setting.go` → `updateUser()` → `userService.UpdateUser()`.                                                                                   |
| Shell credential write    | `x-ui.sh` → `reset_user()` prints the generated plaintext username and password.                                                                                      |
| Shell menu                | `x-ui.sh` → `show_menu()` numbers `0`–`16`; entry `14` is `TLS Certificate Paths`, `15` is `RouterOS Network Policy Notice`, `16` is `Database Tools`.                |
| Certificate menu          | The historical `x-ui.sh` → `tls_cert_path_menu()` offered manual certificate paths only.                                                                              |
| Certificate settings      | `internal/web/service/setting.go` → `webCertFile`/`webKeyFile` (panel) and `subCertFile`/`subKeyFile` (subscription).                                                 |
| TLS validation            | `internal/web/entity/entity.go` validates both pairs with `tls.LoadX509KeyPair`; `internal/web/web.go` and `internal/sub/sub.go` load them at listener start.         |
| Certificate mount         | `Dockerfile` declares `VOLUME [ "/etc/x-ui", "/root/cert" ]`.                                                                                                         |
| Pinned build inputs       | `Dockerfile` + `DockerInit.sh` + `GEO_DATA_SOURCES` + `scripts/build/verify-*-checksum.sh` — the model for pinned ACME tooling.                                       |
| Scheduler                 | `internal/web/web.go` uses an in-process `robfig/cron` scheduler (`s.cron.AddJob(...)`); no cron daemon exists in the image.                                          |
| RouterOS env property     | RouterOS uses `key=` (grouping `list=`), **not** `name=`.                                                                                                             |
| ACME today                | None. `tests/test_container_fixes.py::test_28` explicitly forbids ACME and runtime-install strings in `x-ui.sh`; that test must be updated deliberately by this task. |

---

## A. Administrator credential contract

### A.1 خلاصهٔ فارسی

- اعتبارنامهٔ ادمین فقط از طریق Environment Variableهای
  `MIKROZIA_ADMIN_USERNAME` و `MIKROZIA_ADMIN_PASSWORD` در RouterOS Container
  تعیین می‌شود. این دو، منبع حقیقت واحد (single source of truth) هستند.
- هیچ حساب پیش‌فرض `admin/admin` و هیچ رمز تصادفی چاپ‌شده در Log وجود ندارد.
- اگر هر دو متغیر موجود باشند، در هر راه‌اندازی کانتینر حساب ادمین دیتابیس به‌طور
  دقیق با همان مقادیر همگام (reconcile) می‌شود.
- اگر فقط یکی از دو متغیر موجود باشد، پنل با خطای روشن و امن شروع نمی‌شود.
- اگر هیچ‌کدام موجود نباشد ولی دیتابیس قبلی حساب معتبر داشته باشد، آن حساب دست‌نخورده
  باقی می‌ماند (سازگاری با نصب‌های موجود) و چیزی حذف یا بازنشانی نمی‌شود.
- اگر هیچ‌کدام موجود نباشد و دیتابیس خالی باشد، پنل اصلاً بالا نمی‌آید تا
  اعتبارنامهٔ معتبر از Environment داده شود؛ هیچ حساب ناشناخته/تصادفی ساخته نمی‌شود.
- تغییر رمز از داخل پنل یا از Shell در حالت RouterOS مجاز نیست و با پیام
  روشن رد می‌شود: «Environment را در RouterOS ویرایش کنید و کانتینر را restart کنید».
- رمز و نام کاربری در هیچ Log، پاسخ API، diagnostic، مثال مستندات، لایهٔ Image یا
  تستی به‌صورت plaintext ظاهر نمی‌شوند.

### A.2 The contract

| #    | Rule                                                                                                                                                                                                                                                                                                                                                                                         |
| ---- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| A-1  | `MIKROZIA_ADMIN_USERNAME` and `MIKROZIA_ADMIN_PASSWORD` are the only authoritative source of the panel administrator account in RouterOS Container mode.                                                                                                                                                                                                                                     |
| A-2  | There is no default `admin/admin` account and no generated random first-login credential printed once to logs. `EnsureFirstUser()` must stop minting random credentials.                                                                                                                                                                                                                     |
| A-3  | Both variables present and valid → startup reconciles the account. Matching values preserve the existing bcrypt hash and cause no database write; a changed password receives a new bcrypt hash.                                                                                                                                                                                             |
| A-4  | Exactly one of the two variables present → the panel refuses to start with an explicit, actionable error. No account is created, changed, or deleted.                                                                                                                                                                                                                                        |
| A-5  | Neither variable present and the database already holds an administrator account → that account is preserved exactly as-is (username, password hash, 2FA, login epoch). The panel starts and logs a non-sensitive notice that the account is managed outside the Environment.                                                                                                                |
| A-6  | Neither variable present and the database holds no account → the panel refuses to start with an explicit error naming both variables. It never creates an unknown, random, or default account.                                                                                                                                                                                               |
| A-7  | A password or username change made from the Panel UI (`/panel/api/panel/settings/updateUser`) or from the legacy shell/CLI path (`x-ui setting -username/-password`, `x-ui.sh` reset) is refused in RouterOS mode with a truthful message that directs the operator to update the RouterOS Environment values and restart the container. The mutation path is retired, not silently ignored. |
| A-8  | The plain username and password are never written to application logs, API responses, diagnostics, documentation examples, Docker image layers, or test fixtures. Reconciliation logs describe the outcome only (e.g. "administrator account reconciled from container environment").                                                                                                        |
| A-9  | The password is stored only as a bcrypt hash. The plaintext exists only in the process environment and in the operator's RouterOS configuration.                                                                                                                                                                                                                                             |
| A-10 | When either environment username or password changes, startup reconciles it and increments `login_epoch` once. Matching values preserve the epoch. Reconciliation preserves 2FA and all unrelated user data.                                                                                                                                                                                 |

### A.3 RouterOS operator flow (exact commands)

**P1-AUTH-ACME-03 clarification of A-3/A-10:** reconciliation is idempotent.
Matching username and password cause no database write and preserve the existing
bcrypt hash, login epoch, and 2FA. A changed password receives a new bcrypt hash.
Changing either username or password increments `login_epoch` once. Container-mode
UI/API and CLI mutations also refuse for preserved legacy accounts without the
environment pair.

> RouterOS stores Environment entries in a named list. The property is `key=`, and
> grouping uses `list=` (since RouterOS 7.20). `name=` does not exist.

```routeros
/container/envs/add list=MIKROZIA_ENV key=MIKROZIA_ADMIN_USERNAME value="YOUR_ADMIN_NAME" comment="[Mikrozia:container] Environment"
/container/envs/add list=MIKROZIA_ENV key=MIKROZIA_ADMIN_PASSWORD value="YOUR_STRONG_PASSWORD" comment="[Mikrozia:container] Environment"
```

Update them later with:

```routeros
/container/envs/set [find where list=MIKROZIA_ENV and key=MIKROZIA_ADMIN_PASSWORD] value="YOUR_NEW_STRONG_PASSWORD"
/container/restart [find where comment="[Mikrozia:container] Container"]
```

### A.4 Security note about RouterOS Environment

RouterOS Environment values are configuration, not a secret store. Anyone who can
read the RouterOS configuration (an operator with read access, a configuration
backup, an exported RouterOS configuration file, or a `print` of the environment
list) can read the administrator password. Mikrozia documents this plainly and
requires operators to:

- use a long, unique password that is not reused anywhere else;
- restrict RouterOS administrative access to trusted addresses;
- treat RouterOS configuration exports and backups as secrets;
- rotate the password by editing the Environment value and restarting the container
  (no manual password change is needed or possible in the panel).

Mikrozia never copies the password into the image, into a log file, or into any
generated Xray/subscription configuration.

---

## B. Persistent certificate contract

### B.1 خلاصهٔ فارسی

- همهٔ artifactهای ACME زیر `/root/cert` (یک Volume پایدار) زندگی می‌کنند و با
  تعویض Image یا rollback از بین نمی‌روند.
- گواهی صادرشده به‌صورت خودکار در مسیرهای استاندارد زیر ذخیره می‌شود:
  `/root/cert/<domain>/fullchain.pem` و `/root/cert/<domain>/privkey.pem`.
- وضعیت حساب و زمان‌بندی تمدید ACME در `/root/cert/.acme.sh/` است.
- پس از صدور/تمدید موفق و تغییر واقعی جفت گواهی، Mikrozia هر چهار مسیر گواهی
  پنل (`webCertFile`/`webKeyFile`) و Subscription (`subCertFile`/`subKeyFile`)
  را در یک تراکنش به‌روزرسانی و پنل، شنوندهٔ Subscription و Xray را دقیقاً
  یک‌بار reload می‌کند. تمدید بدون تغییر هیچ reloadای ندارد.
- جفت جدید پیش از نصب Stage و Validate می‌شود. هر شکست در نصب، Validation،
  ذخیرهٔ تنظیمات یا Reload، بایت‌های دقیق جفت قبلی و هر چهار تنظیم قبلی را
  بازیابی می‌کند و خطا پنهان نمی‌ماند.
- فیلدهای Public Key Path / Private Key Path در پنل باقی می‌مانند؛ هم برای
  استفاده از ACME و هم برای import دستی گواهی پیشرفته.
- Mikrozia هرگز کلید خصوصی RouterOS را به داخل کانتینر export نمی‌کند.
- روش Reverse Proxy / گواهی متعلق به RouterOS دیگر مسیر پشتیبانی‌شدهٔ Mikrozia
  نیست؛ از مستندات فعال حذف می‌شود، ولی نصب‌های واقعی موجود دست‌نخورده
  می‌مانند تا زمانی که با Image جدید یک بار صدور موفق انجام شود.

### B.2 The contract

| #    | Rule                                                                                                                                                                                                                                                                                              |
| ---- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| B-1  | Every ACME artifact lives under the persistent `/root/cert` mount. Nothing ACME-related is written to the immutable image RootFS.                                                                                                                                                                 |
| B-2  | ACME account, config, and renewal state live under `/root/cert/.acme.sh/` (the account home).                                                                                                                                                                                                     |
| B-3  | An issued certificate is installed at `/root/cert/<domain>/fullchain.pem` and `/root/cert/<domain>/privkey.pem`.                                                                                                                                                                                  |
| B-4  | After a successful issuance or renewal the panel TLS paths (`webCertFile`/`webKeyFile`) and the subscription TLS paths (`subCertFile`/`subKeyFile`) are updated to those standard paths automatically.                                                                                            |
| B-5  | Both new files are staged inside the target domain directory with restrictive permissions and validated with `tls.LoadX509KeyPair` **before** active files or settings change. Final modes are certificate `0644` and private key `0600`.                                                         |
| B-6  | Installation, validation, four-setting update, and listener reload form one recoverable transaction. Any failure restores the exact prior certificate/key bytes and all four prior settings. A failed run never leaves a mixed pair or a panel pointed at a broken certificate.                   |
| B-7  | The Public Key Path / Private Key Path fields stay in the UI and API. ACME fills them automatically; manual import of an existing certificate remains supported.                                                                                                                                  |
| B-8  | TLS inbounds can reference the same standard paths, so an ACME-issued certificate is usable by an inbound without copying files.                                                                                                                                                                  |
| B-9  | Mikrozia never exports a RouterOS certificate or private key into the container. RouterOS-owned certificates stay in RouterOS.                                                                                                                                                                    |
| B-10 | The RouterOS reverse-proxy / RouterOS-owned-certificate workflow is retired from current product guidance. Existing live installations are not modified by this task; the cutover happens only after a successful issuance with the new image and is a separate owner-authorized acceptance task. |
| B-11 | Certificates, private keys, ACME account files, and Cloudflare tokens survive image replacement and rollback because they live on the `/root/cert` mount.                                                                                                                                         |
| B-12 | Mikrozia uses official acme.sh `--issue`, `--install-cert`, `--fullchain-file`, and `--key-file` behavior. It never reads guessed `<domain>.cer`, `<domain>.key`, `<domain>`, or `<domain>_ecc` source paths. Renewal uses acme.sh's saved installation destinations.                             |
| B-13 | Initial CLI issuance and changed manual/scheduled renewals share one apply/reload flow. A changed pair reloads panel, subscription listener, and Xray exactly once; an unchanged renewal performs no settings write or reload. Reload failure is visible and recoverable.                         |

### B.3 Storage layout

```text
/root/cert/
├── .acme.sh/                      # opaque acme.sh account + renewal state
│   └── mikrozia/                  # panel-owned non-secret domain metadata
├── <domain>/
│   ├── fullchain.pem              # official --fullchain-file destination (0644)
│   └── privkey.pem                # official --key-file destination (0600)
└── <other-domain>/…               # certificates imported by the operator
```

The account home may contain acme.sh-version-specific RSA or ECC directories.
Mikrozia does not infer active certificate paths or enumerate domains from that
internal layout; official installation destinations and Mikrozia metadata are
the stable interface.

---

## C. Method A — HTTP-01, manual issuance and manual renewal

### C.1 خلاصهٔ فارسی

HTTP-01 مسیر «کاملاً دستی» است. اپراتور RouterOS خودش یک Rule موقت برای
رسیدنی پورت ۸۰ می‌سازد، صدور را از پنل/CLI درخواست می‌کند، و بعد از صدور
Rule خودش را می‌بندد. تمدید هم دستی و با همان رویه است. Mikrozia هرگز
Firewall، NAT، Port Forward یا هر Rule دیگری در RouterOS ایجاد، تغییر یا
حذف نمی‌کند.

### C.2 Prerequisites (all operator-owned)

1. A DNS `A`/`AAAA` record for the domain pointing at the public IP that reaches
   the Mikrozia container.
2. Operator-owned RouterOS reachability for TCP port 80 from the internet to the
   container's port 80, valid for the duration of the issuance only.
   Mikrozia publishes the requirement and diagnostics; it never writes the rule.
3. The domain resolves to the same public IP the operator opened.

Mikrozia cannot verify public HTTP-01 reachability from inside the container.
The RouterOS operator must temporarily make TCP 80 reachable from the public
Internet. Local DNS resolution is diagnostic only; neither an outgoing port-80
connection nor RouterOS policy inspection is evidence of public reachability.

### C.3 Procedure

```bash
# 1. Check what the panel can observe (diagnostics only, no policy changes).
x-ui acme check --http --domain panel.example.com

# 2. Ask the panel to issue over HTTP-01.
x-ui acme issue --http --domain panel.example.com

# 3. Inspect the result and the installed paths.
x-ui acme status --domain panel.example.com
```

Equivalent interactive path: `x-ui` → **14. TLS Certificates** →
**3. Issue or renew a certificate with HTTP-01 (manual)**.

### C.4 Cleanup and renewal

- After a successful issuance the operator removes or disables their own port-80
  reachability. Mikrozia does not touch it.
- Renewal is **manual**: temporarily restore the operator-owned port-80
  reachability and repeat the issuance in §C.3. The product never claims
  automatic renewal for HTTP-01, and the
  renewal scheduler must not report HTTP-01 domains as automatically renewed.

### C.5 Exact RouterOS placeholders (operator-owned, illustrative)

> These are placeholders for the RouterOS administrator. Mikrozia ships no
> policy automation, and this document does not prescribe a policy design.

```routeros
# Temporary HTTP-01 reachability — owned and operated by the RouterOS admin.
# Adjust interface/address-list/port to your own network design.
# REMEMBER TO REMOVE OR DISABLE THIS AFTER ISSUANCE.
```

---

## D. Method B — Cloudflare DNS-01, automatic renewal

### D.1 خلاصهٔ فارسی

Cloudflare DNS-01 مسیر «خودکار» است و به پورت ۸۰ عمومی نیاز ندارد. صدور و تمدید
با یک Cloudflare API Token کم‌اختیار (least-privilege) انجام می‌شود. Token در
هیچ Log، پاسخ API، config تولیدشده یا لایهٔ Image نوشته نمی‌شود؛ فقط در حساب
ACME روی Mount پایدار نگه‌داری می‌شود. تمدید خودکار توسط Scheduler داخلی پنل
انجام می‌شود (بدون cron daemon) و تنها در صورت تغییر واقعی گواهی، سرویس‌های
پنل/Subscription reload می‌شوند.

### D.2 Cloudflare token scope (least privilege)

Use an **API Token** (never the Global API Key). Grant, for the single zone that
holds the domain:

| Permission               | Access | Purpose                                             |
| ------------------------ | ------ | --------------------------------------------------- |
| `Zone` → `Zone` → `Read` | Zone   | Look up the zone id for the domain.                 |
| `Zone` → `DNS` → `Edit`  | Zone   | Create and remove the `_acme-challenge` TXT record. |

- Scope the token to **one zone**. Do not use "All zones".
- Do not add `Zone → SSL/TLS → Edit`; it is not needed for DNS-01.
- Keep the token out of shell history, tickets, chat, and command-line
  arguments. Paste it into the hidden interactive prompt, or deliberately use
  `MIKROZIA_CF_API_TOKEN` or explicit one-line `--stdin` for noninteractive
  input. RouterOS Environment values are visible to anyone who can read or
  export the RouterOS configuration; they are not a secret store.

### D.3 Setup, issue, status, rotation

```bash
# 1. Register the token through the hidden interactive prompt (no echo).
x-ui acme cloudflare setup --domain panel.example.com
x-ui acme issue --dns --domain panel.example.com

# 2. Inspect state, expiry, and renewal mode.
x-ui acme status --domain panel.example.com
```

For deliberate noninteractive setup, set `MIKROZIA_CF_API_TOKEN` in the
container environment or pass exactly one token line with explicit `--stdin`.
Generic inherited standard input is rejected rather than described as hidden.
The token is never accepted as a process argument.

Equivalent interactive path: `x-ui` → **14. TLS Certificates** →
**4. Cloudflare DNS-01 setup and automatic renewal**.

### D.4 Secret handling rules

| #   | Rule                                                                                                                                                                                                                                                                                                       |
| --- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| D-1 | The Cloudflare API token is never written to application logs, API responses, diagnostics, generated Xray/subscription configuration, or the immutable image RootFS.                                                                                                                                       |
| D-2 | The token is stored only in the ACME account home on the persistent `/root/cert/.acme.sh` mount, with restrictive file permissions, owned by the container root.                                                                                                                                           |
| D-3 | `x-ui acme status` and every API endpoint return only non-sensitive metadata: mode (DNS-01/HTTP-01), domain, expiry, last renewal result, and whether a token is configured — never the token value.                                                                                                       |
| D-4 | Every log line produced by the ACME subsystem passes through redaction; any occurrence of the token value or of a `CF_Token`-like assignment is replaced with a redaction marker.                                                                                                                          |
| D-5 | Rotation: revoke/rotate the token in Cloudflare, run the setup command again with the new token, then run a status check. The new token replaces the old one on the persistent mount.                                                                                                                      |
| D-6 | Interactive TTY input disables echo and restores terminal state on success, read error, interruption, and panic-safe cleanup. Noninteractive input is deliberate through `MIKROZIA_CF_API_TOKEN` or explicit `--stdin`; generic inherited standard input is rejected and is never called silent or hidden. |

### D.5 Automatic renewal contract

| #    | Rule                                                                                                                                                                                                                             |
| ---- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| D-7  | DNS-01 domains are renewed automatically by the panel's own in-process scheduler (`robfig/cron`), never by a cron daemon inside the image and never by a `curl \| sh` installer.                                                 |
| D-8  | The scheduler survives container restarts and image replacement: its state is the ACME account on `/root/cert/.acme.sh` plus the panel database, both persistent.                                                                |
| D-9  | A changed renewal uses the shared apply/reload flow to reload the panel, subscription listener, and Xray exactly once **only when the certificate actually changed**. An unchanged renewal performs no settings write or reload. |
| D-10 | HTTP-01 domains are never claimed to be automatically renewed. Their status is reported as "manual renewal required".                                                                                                            |
| D-11 | A failed renewal preserves the previous certificate and the previous panel/subscription settings, and reports the failure without exposing secrets.                                                                              |

---

## E. Non-negotiable implementation constraints

| #    | Constraint                                                                                                                                                                                                                                                                                              |
| ---- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| E-1  | No `curl \| sh`, no dynamic runtime installer, no unpinned installer, no runtime package installation (`apk add`, `apt-get`, …) in the panel or in any script it runs at runtime.                                                                                                                       |
| E-2  | Any bundled ACME tool is a **build-time image input**: an exact version, an immutable download URL, and a recorded SHA-256, verified at image build time with the same fail-closed pattern as `GEO_DATA_SOURCES` + `scripts/build/verify-geo-checksum.sh`. The build fails closed on a digest mismatch. |
| E-3  | No mutable `latest` release URL is allowed for a bundled ACME artifact.                                                                                                                                                                                                                                 |
| E-4  | At runtime, Mikrozia may only invoke software that is bundled in the immutable image, and may only write persistent operational state under `/root/cert` and normal application data under `/etc/x-ui`.                                                                                                 |
| E-5  | No ACME state is stored on an unmounted image path.                                                                                                                                                                                                                                                     |
| E-6  | Mikrozia creates no RouterOS policy rules from code, scripts, UI, or documentation automation. Reachability is presented as a factual prerequisite plus diagnostics.                                                                                                                                    |
| E-7  | No plaintext username, password, or Cloudflare token in logs, API responses, diagnostics, documentation examples, Docker image layers, or tests.                                                                                                                                                        |
| E-8  | No destructive migration: no existing user, database, certificate, client, inbound, traffic record, or setting is deleted, reset, or weakened by this feature. Compatibility migrations are explicit, idempotent, tested, and lossless.                                                                 |
| E-9  | The real CHR Reverse Proxy, the real CHR certificate, and every real RouterOS rule stay untouched during source implementation. The real-device cutover is a separate owner-authorized acceptance task after a successful issuance with the new image.                                                  |
| E-10 | Retain useful manual certificate-path configuration; ACME becomes the recommended RouterOS-native workflow, not the only one.                                                                                                                                                                           |
| E-11 | The image remains the only update unit: replacing the image replaces the panel, Xray, sidecars, and the ACME tool together; persistent mounts keep user data and certificates.                                                                                                                          |

---

## F. Implementation surface (what the code must contain)

### F.1 Credential slice

| Component                            | Expected change                                                                                                                                                                                   |
| ------------------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `internal/config/`                   | Read `MIKROZIA_ADMIN_USERNAME` / `MIKROZIA_ADMIN_PASSWORD` (RouterOS Container mode only) with no logging of values.                                                                              |
| `internal/database/db.go`            | `EnsureFirstUser()` no longer mints random credentials; the random-creation path is removed.                                                                                                      |
| `internal/web/service/panel/`        | New reconciliation service: validate the pair, reconcile the stored administrator account, preserve a legacy account when the variables are absent, and refuse a first install with no variables. |
| `internal/web/controller/setting.go` | `updateUser()` refuses the mutation in RouterOS mode with a truthful, localized message.                                                                                                          |
| `main.go`                            | Startup calls the reconciliation; the one-time plaintext credential log line is removed. `x-ui setting -username/-password` refuses in RouterOS mode.                                             |
| `x-ui.sh`                            | `reset_user()` is retired in RouterOS mode and points to the Environment variables; no plaintext credential is printed.                                                                           |
| Frontend                             | The Panel security form shows the Environment-managed state instead of offering a competing password change.                                                                                      |
| Translations                         | New keys in **every** locale file under `internal/web/translation/` and referenced from `frontend/src` or Go in the same commit.                                                                  |

### F.2 ACME slice

| Component                             | Expected change                                                                                                                                                                                                                                                            |
| ------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Build input                           | New pinned manifest (for example `ACME_SOURCES`) plus `scripts/build/verify-acme-checksum.sh`, used by `DockerInit.sh`; the verified tool lands in the final image at a fixed path.                                                                                        |
| `internal/acme/`                      | Account-home management, official issue/install/renew invocation, staged pair validation, exact prior-pair snapshots and rollback, stable destinations, redacted errors, and changed/unchanged renewal results. No dependency on acme.sh's internal RSA/ECC source layout. |
| `internal/web/web.go`                 | Registers the ACME renewal job on the existing in-process `robfig/cron` scheduler.                                                                                                                                                                                         |
| `internal/web/service/setting.go`     | Writes `webCertFile`/`webKeyFile` and `subCertFile`/`subKeyFile` in one database transaction.                                                                                                                                                                              |
| `internal/web/service/certificate.go` | One shared apply/reload transaction for CLI and scheduler: validate, snapshot all four settings, update, reload once, restore pair/settings on failure, and commit only after success.                                                                                     |
| `x-ui.sh`                             | `tls_cert_path_menu()` becomes the TLS Certificates menu with the HTTP-01 and Cloudflare DNS-01 entries; ACME state stays on the persistent mount.                                                                                                                         |
| `tests/test_container_fixes.py`       | `test_28`'s blanket ACME prohibition is replaced by a precise contract: ACME is allowed only through the pinned build input and the persistent mount; dynamic installers and runtime package installs stay forbidden.                                                      |

### F.3 Final menu (the labels below are the implemented menu, not provisional ones)

`x-ui.sh` main menu remains contiguous `0`–`16` and the prompt stays
`Please enter your selection [0-16]:`:

```text
 0. Exit Script
 1. Environment-Managed Administrator Credentials
 2. Reset Web Base Path
 3. Reset Settings
 4. Change Port
 5. View Current Settings
 6. Show RouterOS Start Command
 7. Show RouterOS Stop Command
 8. Reload Panel & Restart Xray
 9. Restart Xray Only
10. Check Status
11. Logs Management
12. Show RouterOS Enable Autostart Command
13. Show RouterOS Disable Autostart Command
14. TLS Certificates
15. RouterOS Network Policy Notice
16. Database Tools
```

Two labels change, and nothing is added or renumbered:

- Entry `1` becomes `Environment-Managed Administrator Credentials`. It no longer
  resets or prints a credential; it explains that the administrator account comes
  from `MIKROZIA_ADMIN_USERNAME` / `MIKROZIA_ADMIN_PASSWORD` in the RouterOS
  Container Environment and shows the Environment commands to change it.
- Entry `14` is renamed from `TLS Certificate Paths` to `TLS Certificates`, and its
  submenu becomes:

```text
 1. Show certificate status (panel, subscription, expiry, renewal mode)
 2. Set certificate paths for the panel (manual import)
 3. Issue or renew a certificate with HTTP-01 (manual)
 4. Cloudflare DNS-01 setup and automatic renewal
 5. Show existing certificates under /root/cert
 0. Back to Main Menu
```

Entry `15` (RouterOS Network Policy Notice) and entry `16` (Database Tools) keep
their current meaning and numbering. Any documentation that names a different
number or label for the certificate menu is stale and must be corrected.

---

## G. Handoff checklist

### P1-AUTH-ACME-03 implementation contract

#### C7 lifecycle serialization

The process lifecycle coordinator admits one synchronous operation at a time.
Certificate IPC acknowledges only after panel TLS, subscription TLS and Xray
restart complete, in that order. A competing reload or monitor restart receives
an immediate conflict; IPC maps this to HTTP 409 with retry guidance. No request
is queued for later execution after returning a conflict.

SIGHUP panel/subscription restart and SIGUSR1 Xray restart wait at most 45 seconds
for admission. Once admitted, they retain ownership until the actual operation
returns. The timeout bounds admission, not execution; work is never detached on
timeout. Shutdown closes admission permanently, waits up to 45 seconds for the
current operation, and then stops the servers synchronously. A wait timeout is
reported; it does not run shutdown concurrently. Late copied reload hooks are
rejected by the terminal coordinator state. IPC maps that state to HTTP 503.

The generic panel restart hook remains an asynchronous, nonblocking signal
enqueue, so request draining and scheduler shutdown cannot wait on their own
caller. Certificate failure still restores files and settings and attempts a
synchronous recovery reload; a second conflict is reported as recovery failure.
Source concurrency tests do not replace RouterOS signal delivery, mount and
real-device acceptance under P1-CHR-01.

- Domain inputs use one IDNA/DNS-label validator before filesystem operations or
  DNS diagnostics. Traversal, separators, control characters, IP literals,
  wildcard labels, empty labels, and trailing dots are rejected. Existing
  symlinks in managed destinations are rejected before invoking the ACME client.
  The persistent mount must remain writable only by trusted container root;
  hostile concurrent root filesystem mutation is outside this boundary.
- Domain metadata accepts only the two supported modes. DNS renewal persists a
  timestamped `pending`, `renewed`, `unchanged`, or `failed` result on the mount;
  `pending` after process interruption is never presented as success.
- A re-issued unchanged pair reconciles incorrect panel/subscription paths;
  unchanged scheduled renewal writes no TLS settings and reloads no listeners.
- Certificate installation holds a nonblocking process-shared advisory lock
  through apply, reload, and commit/rollback. Concurrent attempts fail with an
  explicit retry message.
- CLI reload uses the container-private `/tmp/.mikrozia-certificate-reload.sock`
  (mode `0600`) and waits for the running panel's response. Signal delivery alone
  is not reload success. Panel/subscription listener replacement validates TLS
  and binds synchronously without stopping scheduler jobs. Failure restores the
  previous pair/settings and attempts listener recovery, reporting recovery errors.

Source validation does not establish real CHR or Cloudflare acceptance. The
ledger remains unchanged pending independent project-manager review.

The checklist items below are the acceptance surface of `P1-AUTH-ACME-01`. Items
that require the real CHR, a published image, or a real Cloudflare account are
never marked complete from static or source-level tests.

- [ ] **C-1 — Environment pair validated.** Exactly one of
      `MIKROZIA_ADMIN_USERNAME` / `MIKROZIA_ADMIN_PASSWORD` present fails closed with
      an explicit error and leaves the database untouched.
- [ ] **C-2 — Clean first boot.** With both variables set, a fresh database is
      reconciled to exactly those credentials and the panel logs in with them.
- [ ] **C-3 — Legacy database, no variables.** An existing administrator account
      survives unchanged: same username, same password, working login, no reset.
- [ ] **C-4 — Existing account with variables.** The stored account is reconciled
      to the environment values without touching any other user data.
- [ ] **C-5 — No plaintext in logs.** A startup log capture contains neither the
      administrator username nor the administrator password.
- [ ] **C-6 — Mutating paths retired.** The panel UI password change and the
      legacy shell/CLI credential write refuse in RouterOS mode with the truthful
      Environment-directed message.
- [ ] **C-7 — ACME persistence contract.** ACME state is confined to
      `/root/cert/.acme.sh` and issued files to `/root/cert/<domain>/`; nothing is
      written outside the persistent mounts.
- [ ] **C-8 — Pinned build input.** The ACME tool's version, immutable URL, and
      SHA-256 are recorded in a project-controlled manifest and verified fail-closed
      at image build time.
- [ ] **C-9 — Validated install.** A successful issuance updates the panel and
      subscription certificate paths only after `tls.LoadX509KeyPair` validation.
- [ ] **C-10 — Failure preserves the previous state.** A failed issuance or an
      invalid certificate leaves the previous certificate and previous settings
      untouched.
- [ ] **C-11 — Renewal semantics.** HTTP-01 is never reported as automatically
      renewed; DNS-01 renewal is scheduled internally and reloads listeners only when
      the certificate actually changed; the Cloudflare token is redacted everywhere.
- [ ] **C-12 — No RouterOS policy automation.** No code, script, UI, or active
      documentation creates, changes, or removes RouterOS firewall/NAT/forward rules.
- [ ] **C-13 — No dynamic installer.** No `curl | sh`, runtime package install, or
      unpinned download remains in any runtime path.
- [ ] **C-14 — No stale reverse-proxy guidance.** The RouterOS reverse-proxy
      certificate workflow appears only in clearly labelled historical or
      superseded context, never in current supported guidance.
- [ ] **C-15 — Real-device acceptance.** Performed on the disposable CHR with the
      published image: HTTP-01 issuance with an operator-owned temporary port-80
      rule, Cloudflare DNS-01 issuance and automatic renewal, image replacement with
      certificate survival, and rollback. **Requires owner authorization and a real
      device; never marked complete from static tests.**
