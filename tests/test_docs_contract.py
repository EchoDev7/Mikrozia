#!/usr/bin/env python3
"""Regression tests for the RouterOS and bundled-ACME documentation contract.

Every current guide must describe the supported product: a versioned OCI image
for MikroTik RouterOS Container with persistent mounts, operator-owned network
policy, bundled ACME issuance/renewal, and immutable runtime content. Historical
records may mention retired behaviour only behind a prominent superseded label.
"""

import json
import os
import re
import unittest

DOCS_ROOT = "docs/content/docs"


class DocsContractTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.repo_root = os.path.abspath(os.path.join(os.path.dirname(__file__), ".."))
        cls.docs_root = os.path.join(cls.repo_root, "docs", "content", "docs")
        cls.locales = ("en", "fa", "ru", "zh")
        cls.readmes = (
            "README.md",
            "README.fa_IR.md",
            "README.ar_EG.md",
            "README.es_ES.md",
            "README.ru_RU.md",
            "README.tr_TR.md",
            "README.zh_CN.md",
        )
        cls.historical = (
            "README.upstream.md",
            "docs/MIKROZIA_CHR_FIELD_LESSONS_v1.md",
            "docs/MIKROZIA_PHASE_3_DEPLOYMENT_PLAN.md",
            "docs/MIKROZIA_ROUTEROS_COMPATIBILITY_ASSESSMENT.md",
            "docs/MIKROZIA_GLOBAL_DEPLOYMENT_ARCHITECTURE.md",
            "docs/MIKROZIA_PACKAGE_VALIDATION_REPORT.md",
        )
        cls.current_install_docs = tuple(
            list(cls.readmes)
            + [
                "docs/DOCKER_HUB_OVERVIEW.md",
                "docs/MIKROZIA_CHR_INSTALL_FA.md",
            ]
            + [
                os.path.join(DOCS_ROOT, locale, "guide", "installation.mdx")
                for locale in cls.locales
            ]
        )
        cls.ordered_install_commands = (
            "/file/add name=containers type=directory",
            "/interface/bridge/add name=br-mikrozia",
            "/ip/address/add address=172.16.10.1/24",
            "/interface/veth/add name=veth-mikrozia",
            "/interface/bridge/port/add bridge=br-mikrozia",
            "/container/mounts/add list=MIKROZIA_DATA",
            "/container/mounts/add list=MIKROZIA_CERT",
            "/container/envs/add list=MIKROZIA_ENV key=XUI_PORT",
            "/container/envs/add list=MIKROZIA_ENV key=HOME",
            "/container/config/set registry-url=",
            '/container/add remote-image="echodev7/mikrozia:',
            "/container/start",
        )
        cls.localized_api_pages = {
            "api-tokens.mdx",
            "authentication.mdx",
            "backup.mdx",
            "clients.mdx",
            "hosts.mdx",
            "inbounds.mdx",
            "index.mdx",
            "nodes.mdx",
            "server.mdx",
            "settings.mdx",
            "subscription-server.mdx",
            "websocket.mdx",
            "xray-settings.mdx",
        }

    def _read(self, *parts):
        with open(os.path.join(self.repo_root, *parts), "r", encoding="utf-8") as f:
            return f.read()

    def _mdx_pages(self):
        for locale in self.locales:
            base = os.path.join(self.docs_root, locale)
            for root, _dirs, files in os.walk(base):
                for name in sorted(files):
                    if name.endswith(".mdx"):
                        yield os.path.join(root, name)

    def _code_lines(self, path):
        """Yield (line_number, line) for fenced code blocks only."""
        in_fence = False
        with open(path, "r", encoding="utf-8") as f:
            for number, line in enumerate(f, 1):
                if line.lstrip().startswith("```"):
                    in_fence = not in_fence
                    continue
                if in_fence:
                    yield number, line

    @staticmethod
    def _normalized(text):
        return re.sub(r"\s+", " ", text)

    def _assert_ordered_commands(self, text, label):
        cursor = -1
        for command in self.ordered_install_commands:
            position = text.find(command)
            self.assertNotEqual(position, -1, f"{label} lacks command: {command}")
            self.assertGreater(
                position,
                cursor,
                f"{label} has command out of order: {command}",
            )
            cursor = position

    def test_01_code_examples_contain_no_unsupported_installers(self):
        forbidden = (
            (r"bash\s*<\(\s*curl", "upstream curl-pipe installer"),
            (r"\./install\.sh", "upstream install.sh execution"),
            (r"chmod\s+\+x\s+install\.sh", "upstream install.sh execution"),
            (r"systemctl\s+(start|stop|restart|enable|disable|status)", "systemd service command"),
            (r"service\s+x-ui\s+(start|stop|restart)", "SysV service command"),
            (r"docker(?: |-)compose\s+(up|down|start|stop)", "compose production command"),
            (r"(?:apt|apt-get|yum|dnf|apk)\s+(?:install|add)\b", "in-container package installation"),
            (r"firewall-cmd\b", "firewalld command"),
            (r"iptables\s+-[AID]\b", "iptables rule mutation"),
            (r"nft\s+add\b", "nftables rule mutation"),
            (r"ufw\s+(allow|enable|deny)\b", "ufw rule mutation"),
            (r"certbot\s+(certonly|renew)\b", "ACME issuance command"),
            (r"\bjournalctl\b", "systemd journal command"),
            (r"sysctl\s+-w\b", "host kernel tuning command"),
            (r"ghcr\.io/mhsanaei", "unsupported upstream image registry"),
            (r"x-ui\s+update\b", "retired in-container updater"),
        )
        violations = []
        # Certificate issuance on a separate reverse-proxy host is operator-owned
        # and outside the container, so certbot examples are valid there.
        cert_exempt = {"operations/reverse-proxy.mdx"}
        paths = list(self._mdx_pages())
        paths += [os.path.join(self.repo_root, name) for name in self.readmes]
        for path in paths:
            rel = os.path.relpath(path, self.repo_root)
            for number, line in self._code_lines(path):
                for pattern, label in forbidden:
                    if label == "ACME issuance command" and rel.replace(os.sep, "/").endswith(
                        tuple(cert_exempt)
                    ):
                        continue
                    if re.search(pattern, line, re.IGNORECASE):
                        violations.append(f"{rel}:{number}: {label}: {line.strip()}")
        self.assertEqual(violations, [], "\n".join(violations))

    def test_02_install_and_update_paths_use_versioned_image_and_mounts(self):
        image = re.compile(r"echodev7/mikrozia:v(?:\d+\.\d+\.\d+-mikrozia(?:\.\d+)?|\d+-mikrozia)")
        import_instruction = re.compile(
            r"(mikrozia-clean-room-deploy|mikrozia-preflight|\.rsc|/import\s+file-name)",
            re.IGNORECASE,
        )
        for locale in self.locales:
            install = self._read(DOCS_ROOT, locale, "guide", "installation.mdx")
            update = self._read(DOCS_ROOT, locale, "guide", "update-uninstall.mdx")
            with self.subTest(locale=locale, page="installation"):
                self.assertRegex(install, image)
                self.assertNotRegex(install, import_instruction)
                for token in (
                    "/container/start",
                    "/etc/x-ui",
                    "/root/cert",
                    "container=yes",
                    "7.25",
                    "172.16.10.2",
                    "P1-CHR-01",
                    'mountlists="MIKROZIA_DATA,MIKROZIA_CERT"',
                ):
                    self.assertIn(token, install)
                self._assert_ordered_commands(
                    install, f"{locale}/guide/installation.mdx"
                )
            with self.subTest(locale=locale, page="update-uninstall"):
                self.assertNotRegex(update, import_instruction)
                self.assertIn("/etc/x-ui", update)
                self.assertIn("/root/cert", update)
                self.assertIn("/container/start", update)

    def test_02b_current_guides_use_direct_commands(self):
        import_instruction = re.compile(
            r"(mikrozia-clean-room-deploy|mikrozia-preflight|mikrozia-clean-room-teardown|\.rsc|/import\s+file-name)",
            re.IGNORECASE,
        )
        image = re.compile(r"echodev7/mikrozia:v(?:\d+\.\d+\.\d+-mikrozia(?:\.\d+)?|\d+-mikrozia)")
        violations = []
        for name in self.current_install_docs:
            text = self._read(name)
            for match in import_instruction.finditer(text):
                line = text.count("\n", 0, match.start()) + 1
                violations.append(f"{name}:{line}: {match.group(0)}")
            with self.subTest(document=name):
                self.assertRegex(text, image)
                self.assertIn("/etc/x-ui", text)
                self.assertIn("/root/cert", text)
                self.assertIn("P1-CHR-01", text)
                self._assert_ordered_commands(text, name)
        self.assertEqual(violations, [], "\n".join(violations))

    def test_03_root_readmes_publish_the_same_contract(self):
        image = re.compile(r"echodev7/mikrozia:v(?:\d+\.\d+\.\d+-mikrozia(?:\.\d+)?|\d+-mikrozia)")
        import_instruction = re.compile(
            r"(mikrozia-clean-room-deploy|mikrozia-preflight|\.rsc|/import\s+file-name)",
            re.IGNORECASE,
        )
        for name in self.readmes:
            text = self._read(name)
            with self.subTest(readme=name):
                self.assertIn("RouterOS", text)
                self.assertRegex(text, image)
                self.assertIn("/etc/x-ui", text)
                self.assertIn("/root/cert", text)
                self.assertIn("P1-CHR-01", text)
                self.assertNotRegex(text, import_instruction)
                self.assertNotRegex(text, r"bash\s*<\(\s*curl")
                self._assert_ordered_commands(text, name)

    def test_04_network_policy_is_operator_owned(self):
        ports = self._normalized(self._read(DOCS_ROOT, "en", "reference", "ports-firewall.mdx"))
        self.assertIn("never creates or changes RouterOS firewall", ports)
        security = self._normalized(self._read(DOCS_ROOT, "en", "operations", "security.mdx"))
        self.assertIn(
            "all filter, NAT, port-forwarding, routing, and access-control policy is configured by the RouterOS administrator",
            security,
        )
        for locale in self.locales:
            text = self._read(DOCS_ROOT, locale, "reference", "ports-firewall.mdx")
            with self.subTest(locale=locale):
                self.assertIn("RouterOS", text)
                self.assertNotIn("FirewallRulesGenerator", text)

    def test_05_certificates_are_issued_by_the_bundled_acme_client(self):
        ssl = self._normalized(self._read(DOCS_ROOT, "en", "config", "ssl-certificates.mdx"))
        self.assertIn("HTTP-01", ssl)
        self.assertIn("DNS-01", ssl)
        self.assertIn("x-ui acme", ssl)
        self.assertIn(
            "never creates, changes, removes, enables, or disables RouterOS firewall",
            ssl,
        )
        self.assertNotIn("does not issue or renew certificates", ssl)
        self.assertNotIn("does not run an ACME client", ssl)
        faq = self._normalized(self._read(DOCS_ROOT, "en", "help", "faq.mdx"))
        self.assertIn("ACME client bundled in the image", faq)
        for locale in self.locales:
            guide = self._normalized(self._read(DOCS_ROOT, locale, "config", "ssl-certificates.mdx"))
            with self.subTest(locale=locale):
                self.assertIn("HTTP-01", guide)
                self.assertIn("DNS-01", guide)
            panel = self._read(DOCS_ROOT, locale, "config", "panel.mdx")
            with self.subTest(locale=locale):
                self.assertNotIn("Let's Encrypt", panel)
                self.assertIn("/root/cert", panel)

    def test_05b_certificate_pages_do_not_retire_the_bundled_acme_client(self):
        retired_claims = {
            "en": "does not issue or renew certificates",
            "fa": "گواهی را داخل کانتینر صادر یا تمدید نمی‌کند",
            "ru": "не выпускает и не продлевает сертификаты",
            "zh": "不在容器内签发或续期证书",
        }
        for locale in self.locales:
            for page in (("config", "panel.mdx"), ("help", "troubleshooting.mdx")):
                text = self._normalized(self._read(DOCS_ROOT, locale, *page))
                with self.subTest(locale=locale, page=page):
                    self.assertNotIn(retired_claims[locale], text)
                    self.assertIn("HTTP-01", text)
                    self.assertIn("DNS-01", text)
                    self.assertIn("ACME", text)
            troubleshooting = self._normalized(
                self._read(DOCS_ROOT, locale, "help", "troubleshooting.mdx")
            )
            with self.subTest(locale=locale, page="troubleshooting"):
                self.assertIn("x-ui acme", troubleshooting)

    def test_05c_every_active_page_rejects_retired_acme_claims(self):
        retired = (
            r"there is no acme client",
            r"not a certificate authority or acme client",
            r"does not issue or renew(?: tls)? certificates",
            r"does not run an acme client",
            r"user-managed certificates",
            r"obtain and renew them outside the container",
            r"publicly trusted certificates obtained outside the container",
            r"whether port 80 answered",
            r"port 80 answered from inside",
            r"هیچ کلاینت acme",
            r"مرجع گواهی یا کلاینت acme نیست",
            r"بیرون از کانتینر تهیه و تمدید",
            r"پورت ۸۰\s+از داخل کانتینر پاسخ داد",
            r"не acme-клиент",
            r"нет acme-клиента",
            r"сертификаты, полученные вне контейнера",
            r"ответил ли порт 80",
            r"没有\s*acme\s*客户端",
            r"不是证书颁发机构或\s*acme\s*客户端",
            r"80\s*端口在容器内部是否",
        )
        paths = list(self._mdx_pages())
        paths += [os.path.join(self.repo_root, name) for name in self.readmes]
        paths += [
            os.path.join(self.repo_root, name)
            for name in (
                "docs/DOCKER_HUB_OVERVIEW.md",
                "docs/MIKROZIA_CHR_INSTALL_FA.md",
                "docs/MIKROZIA_AUTH_ACME_SPEC.md",
            )
        ]
        violations = []
        for path in paths:
            rel = os.path.relpath(path, self.repo_root).replace(os.sep, "/")
            text = self._normalized(self._read(rel)).lower()
            for pattern in retired:
                match = re.search(pattern, text, re.IGNORECASE)
                if match:
                    violations.append(f"{rel}: {match.group(0)}")
        self.assertGreaterEqual(len(list(self._mdx_pages())), 160)
        self.assertEqual(violations, [], "\n".join(violations))

    def test_05d_localized_acme_guides_match_the_shared_transaction(self):
        warnings = {
            "en": "Mikrozia cannot verify public HTTP-01 reachability from inside the container. The RouterOS operator must temporarily make TCP 80 reachable from the public Internet.",
            "fa": "Mikrozia از داخل کانتینر نمی‌تواند دسترس‌پذیری عمومی HTTP-01 را بررسی کند. اپراتور RouterOS باید موقتاً TCP 80 را از اینترنت عمومی در دسترس قرار دهد.",
            "ru": "Mikrozia не может проверить публичную доступность HTTP-01 изнутри контейнера. Оператор RouterOS должен временно сделать TCP-порт 80 доступным из публичного интернета.",
            "zh": "Mikrozia 无法从容器内部验证 HTTP-01 的公网可达性。RouterOS 操作员必须临时使 TCP 80 端口可从公网访问。",
        }
        for locale in self.locales:
            page = self._normalized(
                self._read(DOCS_ROOT, locale, "config", "ssl-certificates.mdx")
            )
            with self.subTest(locale=locale):
                self.assertIn(warnings[locale], page)
                for token in (
                    "/root/cert/<domain>/fullchain.pem",
                    "/root/cert/<domain>/privkey.pem",
                    "/root/cert/.acme.sh/",
                    "x-ui acme issue --http",
                    "x-ui acme issue --dns",
                    "x-ui acme renew",
                    "x-ui acme cloudflare setup",
                    "MIKROZIA_CF_API_TOKEN",
                    "--stdin",
                    "0600",
                    "14 — TLS Certificates",
                    "3 — Issue or renew a certificate with HTTP-01 (manual)",
                    "4 — Cloudflare DNS-01 setup and automatic renewal",
                ):
                    self.assertIn(token, page)
                self.assertLessEqual(page.count("x-ui restart"), 1)
                self.assertNotIn("Serve the new certificate", page)
                self.assertNotIn("export MIKROZIA_CF_API_TOKEN", page)
                self.assertIn("x-ui acme cloudflare setup --domain", page)
                for token in {
                    "en": ("exactly once", "restores"),
                    "fa": ("دقیقاً یک‌بار", "بازیابی می‌شوند"),
                    "ru": ("ровно один раз", "восстанавливаются"),
                    "zh": ("恰好", "恢复原有"),
                }[locale]:
                    with self.subTest(locale=locale, token=token):
                        self.assertIn(token, page)
                env = self._normalized(
                    self._read(DOCS_ROOT, locale, "reference", "env-vars.mdx")
                )
                self.assertIn("MIKROZIA_CF_API_TOKEN", env)
                self.assertIn("RouterOS", env)
                self.assertNotIn("export MIKROZIA_CF_API_TOKEN", env)

        hub = self._normalized(self._read("docs", "DOCKER_HUB_OVERVIEW.md"))
        self.assertIn("cannot verify public reachability from inside the container", hub)
        self.assertIn("explicit `--stdin`", hub)
        self.assertNotIn('printf \'%s\' "<your-token>"', hub)

        spec = self._normalized(self._read("docs", "MIKROZIA_AUTH_ACME_SPEC.md"))
        for token in (
            "--install-cert",
            "--fullchain-file",
            "--key-file",
            "exactly once",
            "generic inherited standard input is rejected",
            "awaiting project-manager review",
        ):
            with self.subTest(document="acme-spec", token=token):
                self.assertIn(token, spec)
        for token in (
            "<domain>/<domain>.conf",
            "<domain>/<domain>.cer",
            "export MIKROZIA_CF_API_TOKEN",
        ):
            with self.subTest(document="acme-spec", retired=token):
                self.assertNotIn(token, spec)

    def test_05e_contributing_does_not_promise_a_generated_first_login_credential(self):
        contributing = self._normalized(self._read("CONTRIBUTING.md"))
        self.assertNotIn("No panel user existed", contributing)
        self.assertIn("MIKROZIA_ADMIN_USERNAME", contributing)
        self.assertIn("MIKROZIA_ADMIN_PASSWORD", contributing)

    def test_06_runtime_is_immutable_and_has_no_updater(self):
        update = self._normalized(self._read(DOCS_ROOT, "en", "guide", "update-uninstall.mdx"))
        self.assertIn("replacing the Container or Container Image", update)
        self.assertIn("never modifies its own RootFS", update)
        guide = self._normalized(self._read(DOCS_ROOT, "en", "guide", "index.mdx"))
        self.assertIn("Not self-updating", guide)
        for locale in self.locales:
            page = self._read(DOCS_ROOT, locale, "guide", "update-uninstall.mdx")
            with self.subTest(locale=locale):
                self.assertNotIn("x-ui update", page)

    def test_06b_rollback_and_sqlite_restore_contract_is_documented(self):
        rollback_markers = {
            "en": "Rolling back an image update",
            "fa": "بازگردانی به‌روزرسانی ایمیج",
            "ru": "Откат обновления образа",
            "zh": "回滚镜像更新",
        }
        for locale in self.locales:
            update = self._read(DOCS_ROOT, locale, "guide", "update-uninstall.mdx")
            with self.subTest(locale=locale, page="update-uninstall"):
                self.assertIn(rollback_markers[locale], update)
                self.assertIn("/etc/x-ui", update)
                self.assertIn("/root/cert", update)
                self.assertIn("RootFS", update)
                backup = self._read(DOCS_ROOT, locale, "operations", "backup-restore.mdx")
                self.assertIn("--restore", backup)
                self.assertIn("--out", backup)

    def test_06c_live_sqlite_backup_guidance_uses_consistent_dump(self):
        for locale in self.locales:
            backup = self._read(DOCS_ROOT, locale, "operations", "backup-restore.mdx")
            with self.subTest(locale=locale):
                self.assertIn("x-ui migrate-db --dump", backup)
                self.assertIn("x-ui migrate-db --restore", backup)
                if locale == "en":
                    self.assertIn(
                        "Do not copy `/etc/x-ui/x-ui.db` directly", backup
                    )
                elif locale == "ru":
                    self.assertIn("Не копируйте `/etc/x-ui/x-ui.db`", backup)
                elif locale == "zh":
                    self.assertIn("请勿直接复制 `/etc/x-ui/x-ui.db`", backup)
                elif locale == "fa":
                    self.assertIn(
                        "فایل `/etc/x-ui/x-ui.db` را مستقیماً کپی\nنکنید",
                        backup,
                    )
                self.assertNotRegex(
                    backup,
                    r"(?m)^\s*cp\s+[^\n]*?/etc/x-ui/x-ui\.db",
                )

        rollout = self._read("docs", "pwa-installability-verification.md")
        self.assertIn("x-ui migrate-db --dump", rollout)
        self.assertIn("do not\ncopy `/etc/x-ui/x-ui.db` directly while it is live", rollout)

    def test_07_retired_api_endpoints_are_absent_everywhere(self):
        retired = ("/server/updatePanel", "/server/installXray", "/server/getXrayVersion")
        for path in self._mdx_pages():
            text = self._read(os.path.relpath(path, self.repo_root))
            for endpoint in retired:
                with self.subTest(page=os.path.relpath(path, self.repo_root), endpoint=endpoint):
                    self.assertNotIn(endpoint, text)
        # internal/web/dist is a gitignored frontend build output.  It does not
        # exist in a clean checkout until the image/frontend build runs, so the
        # release validation must assert the two version-controlled API specs.
        for spec in ("frontend/public/openapi.json", "docs/public/openapi.json"):
            self.assertTrue(os.path.exists(os.path.join(self.repo_root, spec)), spec)
            document = json.loads(self._read(spec))
            for endpoint in retired:
                with self.subTest(spec=spec, endpoint=endpoint):
                    self.assertNotIn(endpoint, document["paths"])

    def test_08_generated_api_reference_locales_are_preserved(self):
        self.assertTrue(os.path.isdir(os.path.join(self.docs_root, "en", "reference", "api")))
        total_localized = 0
        for locale in ("fa", "ru", "zh"):
            api_dir = os.path.join(self.docs_root, locale, "reference", "api")
            with self.subTest(locale=locale):
                self.assertTrue(
                    os.path.isdir(api_dir),
                    "localized API reference directory was removed",
                )
                actual = {
                    name
                    for name in os.listdir(api_dir)
                    if name.endswith(".mdx")
                }
                self.assertEqual(
                    actual,
                    self.localized_api_pages,
                    f"{locale} API reference page set changed",
                )
                self.assertTrue(
                    os.path.isfile(os.path.join(api_dir, "meta.json")),
                    f"{locale} API reference meta.json is missing",
                )
                total_localized += len(actual) + 1
        self.assertEqual(
            total_localized,
            42,
            "localized API reference inventory must remain 39 MDX + 3 meta.json",
        )

    def test_10_historical_documents_are_prominently_labelled(self):
        for name in self.historical:
            with open(os.path.join(self.repo_root, name), "r", encoding="utf-8") as f:
                head = f.read(2500)
            with self.subTest(document=name):
                self.assertIn("HISTORICAL", head.upper())
                self.assertTrue(
                    re.search(r"SUPERSEDED|NON-EXECUTABLE|DO NOT USE", head.upper()),
                    f"{name} lacks a superseded/non-executable label",
                )

    def test_10b_rsc_mentions_are_historical_or_ledger_only(self):
        script_reference = re.compile(
            r"(mikrozia-clean-room-deploy|mikrozia-preflight|/import\s+file-name|\.rsc)",
            re.IGNORECASE,
        )
        allowed = set(self.historical) | {"docs/MIKROZIA_PROJECT_LEDGER.md"}
        violations = []
        for root, dirs, files in os.walk(self.repo_root):
            dirs[:] = [
                name
                for name in dirs
                if name not in {".git", "node_modules", "vendor", "dist"}
            ]
            for name in files:
                if not name.endswith((".md", ".mdx")):
                    continue
                path = os.path.join(root, name)
                rel = os.path.relpath(path, self.repo_root).replace(os.sep, "/")
                with open(path, "r", encoding="utf-8", errors="replace") as f:
                    text = f.read()
                if not script_reference.search(text):
                    continue
                if rel in allowed:
                    continue
                violations.append(rel)
        self.assertEqual(
            sorted(set(violations)),
            [],
            "script import references outside historical/ledger documents",
        )

    def test_09_docs_do_not_reference_retired_tool_components(self):
        for path in self._mdx_pages():
            text = self._read(os.path.relpath(path, self.repo_root))
            with self.subTest(page=os.path.relpath(path, self.repo_root)):
                self.assertNotIn("<InstallCommandBuilder", text)
                self.assertNotIn("<FirewallRulesGenerator", text)

    def test_12_no_active_legacy_installer_paths(self):
        removed = (
            "install.sh",
            "deploy",
            "scripts/generator",
            "scripts/routeros",
            "docs/components/tools/install-command-builder.tsx",
            "docs/components/home/install-command.tsx",
            "docs/lib/xray/install.ts",
            "docs/lib/xray/install.test.ts",
            "tests/test_mikrozia_generator.py",
        )
        for rel in removed:
            with self.subTest(path=rel):
                self.assertFalse(
                    os.path.exists(os.path.join(self.repo_root, rel)),
                    f"retired install artifact still shipped: {rel}",
                )

        legacy = re.compile(
            r"(install\.sh|cloud-init|\.rsc\b|/import\s+file-name"
            r"|bash\s*<\(\s*curl|curl[^\n|]*\|\s*(?:ba)?sh)",
            re.IGNORECASE,
        )
        allowed = set(self.historical) | {"docs/MIKROZIA_PROJECT_LEDGER.md"}
        scopes = [
            os.path.join(self.repo_root, name) for name in self.readmes
        ] + [
            os.path.join(self.repo_root, "docs", "README.md"),
            os.path.join(self.repo_root, "docs", "DOCKER_HUB_OVERVIEW.md"),
        ]
        scopes += list(self._mdx_pages())
        for root in (
            os.path.join(self.repo_root, ".github", "workflows"),
            os.path.join(self.repo_root, "scripts"),
            os.path.join(self.repo_root, "docs", "app"),
            os.path.join(self.repo_root, "docs", "components"),
            os.path.join(self.repo_root, "docs", "lib"),
        ):
            for current, dirs, files in os.walk(root):
                dirs[:] = [d for d in dirs if d not in {"node_modules", "dist"}]
                for name in files:
                    if name.endswith((".md", ".mdx", ".sh", ".ts", ".tsx", ".yml", ".yaml")):
                        scopes.append(os.path.join(current, name))

        violations = []
        for path in scopes:
            if not os.path.isfile(path):
                continue
            rel = os.path.relpath(path, self.repo_root).replace(os.sep, "/")
            if rel in allowed:
                continue
            with open(path, "r", encoding="utf-8", errors="replace") as f:
                for number, line in enumerate(f, 1):
                    if legacy.search(line):
                        violations.append(f"{rel}:{number}: {line.strip()}")
        self.assertEqual(
            violations,
            [],
            "current documentation, workflow, or script still offers a retired install path",
        )

    def test_13_direct_terminal_install_docs_remain_in_all_locales(self):
        for locale in self.locales:
            path = os.path.join(DOCS_ROOT, locale, "guide", "installation.mdx")
            with self.subTest(locale=locale):
                self.assertTrue(os.path.isfile(os.path.join(self.repo_root, path)))
                text = self._read(path)
                self.assertIn("/container/add remote-image=", text)
                self.assertIn('mountlists="MIKROZIA_DATA,MIKROZIA_CERT"', text)
                self.assertIn("/root/cert", text)
                self._assert_ordered_commands(text, path)

    def test_14_firewall_policy_generator_slice_is_retired(self):
        removed = (
            "docs/components/tools/firewall-rules-generator.tsx",
            "docs/lib/xray/firewall.ts",
            "docs/lib/xray/firewall.test.ts",
        )
        for rel in removed:
            with self.subTest(path=rel):
                self.assertFalse(
                    os.path.exists(os.path.join(self.repo_root, rel)),
                    f"retired host-firewall policy generator still shipped: {rel}",
                )

        identifiers = (
            "FirewallRulesGenerator",
            "buildUfwCommands",
            "buildNftablesRuleset",
        )
        # The project ledger records the audit history of this retired slice; it is
        # a task-control record, not shipped documentation or product source.
        allowed = {"docs/MIKROZIA_PROJECT_LEDGER.md"}
        text_extensions = {
            ".ts",
            ".tsx",
            ".js",
            ".jsx",
            ".mjs",
            ".cjs",
            ".json",
            ".md",
            ".mdx",
            ".yml",
            ".yaml",
            ".sh",
            ".css",
        }
        violations = []
        for area in ("docs", "frontend"):
            for current, dirs, files in os.walk(os.path.join(self.repo_root, area)):
                dirs[:] = [
                    d
                    for d in dirs
                    if d not in {"node_modules", ".next", "dist", "coverage", "storybook-static"}
                ]
                for name in files:
                    if os.path.splitext(name)[1] not in text_extensions:
                        continue
                    path = os.path.join(current, name)
                    rel = os.path.relpath(path, self.repo_root).replace(os.sep, "/")
                    if rel in allowed:
                        continue
                    with open(path, "r", encoding="utf-8", errors="replace") as f:
                        text = f.read()
                    for identifier in identifiers:
                        if identifier in text:
                            violations.append(f"{rel}: {identifier}")
        self.assertEqual(
            violations,
            [],
            "host-firewall policy generation is still exposed in docs or frontend source",
        )

        ports = self._normalized(self._read(DOCS_ROOT, "en", "reference", "ports-firewall.mdx"))
        self.assertIn("never creates or changes RouterOS firewall", ports)
        self.assertIn("does not automate any of these steps", ports)
        security = self._normalized(self._read(DOCS_ROOT, "en", "operations", "security.mdx"))
        self.assertIn("Mikrozia has no firewall menu", security)
        self.assertIn(
            "all filter, NAT, port-forwarding, routing, and access-control policy is configured by the RouterOS administrator",
            security,
        )

    def test_15_routeros_policy_examples_are_strictly_operator_owned(self):
        policy_syntax = (
            "/ip/firewall/",
            "action=masquerade",
            "action=dst-nat",
            "action=accept",
            "action=drop",
            "chain=srcnat",
            "chain=dstnat",
            "chain=forward",
            'comment="[Mikrozia]',
        )
        # Network policy is never generated or applied by Mikrozia. The owner
        # explicitly approved these two Persian, operator-run examples; nowhere
        # else may ship RouterOS firewall/NAT commands.
        allowed = set(self.historical) | {
            "docs/MIKROZIA_PROJECT_LEDGER.md",
            "docs/MIKROZIA_PROJECT_CHARTER.md",
            "docs/MIKROZIA_CHR_INSTALL_FA.md",
            "docs/MIKROZIA_ROUTEROS_API_INTEGRATION.md",
            "docs/content/docs/fa/reference/ports-firewall.mdx",
            "docs/content/docs/en/operations/routeros-nat.mdx",
            "docs/content/docs/fa/operations/routeros-nat.mdx",
            "docs/content/docs/ru/operations/routeros-nat.mdx",
            "docs/content/docs/zh/operations/routeros-nat.mdx",
        }
        skipped_dirs = {
            ".git",
            ".next",
            ".source",
            "coverage",
            "dist",
            "node_modules",
            "release",
            "storybook-static",
            "vendor",
        }
        violations = []
        for current, dirs, files in os.walk(self.repo_root):
            dirs[:] = [name for name in dirs if name not in skipped_dirs]
            for name in sorted(files):
                if not name.endswith((".md", ".mdx")):
                    continue
                path = os.path.join(current, name)
                rel = os.path.relpath(path, self.repo_root).replace(os.sep, "/")
                with open(path, "r", encoding="utf-8", errors="replace") as f:
                    for number, line in enumerate(f, 1):
                        for token in policy_syntax:
                            if token in line and rel not in allowed:
                                violations.append(f"{rel}:{number}: {token}")
        self.assertEqual(
            violations,
            [],
            "RouterOS firewall/NAT examples must remain limited to the explicitly operator-owned Persian guides",
        )

        for rel in (
            "docs/MIKROZIA_CHR_INSTALL_FA.md",
            "docs/content/docs/fa/reference/ports-firewall.mdx",
        ):
            text = self._read(rel)
            self.assertIn("[Mikrozia:operator]", text)
            self.assertIn("نمونه", text)
            self.assertIn("دستی", text)
            self.assertIn("/ip/firewall/nat/add chain=dstnat", text)

        for rel in (
            "docs/MIKROZIA_ROUTEROS_API_INTEGRATION.md",
            "docs/content/docs/en/operations/routeros-nat.mdx",
            "docs/content/docs/fa/operations/routeros-nat.mdx",
            "docs/content/docs/ru/operations/routeros-nat.mdx",
            "docs/content/docs/zh/operations/routeros-nat.mdx",
        ):
            text = self._read(rel)
            self.assertIn("mikrozia:managed:inbound:", text)

        overview = self._read("docs", "DOCKER_HUB_OVERVIEW.md")
        heading = "## Required Network Inputs (operator-owned)"
        self.assertIn(heading, overview, "the Docker Hub overview must document operator-owned network inputs")
        section = overview[overview.index(heading):]
        end = section.find("\n## ", len(heading))
        if end != -1:
            section = section[:end]
        normalized = self._normalized(section)
        for token in (
            "Mikrozia does not provide RouterOS firewall/NAT commands",
            "belongs to the RouterOS administrator",
            "Panel port",
            "2053/TCP",
            "Subscription port",
            "2096/TCP",
            "User inbound ports",
            "outbound reachability",
            "Source restrictions and interfaces",
        ):
            self.assertIn(token, normalized, f"the operator-owned section must state: {token}")
        for token in policy_syntax + (
            "172.16.",
            "interface-list",
            "out-interface",
            "in-interface",
            "WAN",
        ):
            self.assertNotIn(token, section, f"the operator-owned section must not contain: {token}")
        for line in section.splitlines():
            self.assertFalse(
                line.strip().startswith("/"),
                f"the operator-owned section must not contain a RouterOS command: {line.strip()}",
            )

        # The supported direct-terminal installation path must stay intact and
        # must not be mistaken for a branded firewall rule.
        self.assertRegex(overview, r"/container/add remote-image=\"echodev7/mikrozia:v(?:\d+\.\d+\.\d+-mikrozia(?:\.\d+)?|\d+-mikrozia)\"")
        self.assertIn('/container/start [find where comment="[Mikrozia:container] Container"]', overview)
        self.assertNotIn('comment="[Mikrozia]', overview)

    def test_11_local_development_compose_is_scoped(self):
        database = self._normalized(self._read(DOCS_ROOT, "en", "reference", "database.mdx"))
        self.assertIn("local development", database)
        for locale in self.locales:
            install = self._read(DOCS_ROOT, locale, "guide", "installation.mdx")
            with self.subTest(locale=locale):
                self.assertNotIn("docker-compose.yml", install)


if __name__ == "__main__":
    unittest.main()
