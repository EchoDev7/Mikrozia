#!/usr/bin/env python3
"""Regression tests for the P0-RT-01 RouterOS runtime-immutability contract.

The production image may download and install software only at build time
(DockerInit.sh). No panel, management-script or container-start path may
download, install, upgrade or replace software, issue certificates, or run a
renewal scheduler. Certificates are user-supplied and configured by path.
"""

import os
import re
import unittest


class TestRuntimeImmutability(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.repo_root = os.path.abspath(os.path.join(os.path.dirname(__file__), ".."))

    def _read(self, *parts):
        with open(os.path.join(self.repo_root, *parts), "r", encoding="utf-8") as f:
            return f.read()

    def test_01_entrypoint_is_minimal_and_has_no_runtime_mutation(self):
        entrypoint = self._read("DockerEntrypoint.sh")
        self.assertEqual(entrypoint.splitlines()[0], "#!/bin/bash")
        self.assertIn("exec /app/x-ui", entrypoint)
        for forbidden in (
            "acme",
            "ACME",
            "crond",
            "crontab",
            "curl",
            "wget",
            "apk",
            "apt",
            "dnf",
            "yum",
            "DockerInit",
            "install.sh",
        ):
            self.assertNotIn(forbidden, entrypoint, f"DockerEntrypoint.sh must not contain {forbidden!r}")

        dockerfile = self._read("Dockerfile")
        final_stage = dockerfile.split("FROM alpine:", 1)[-1]
        self.assertIn("ENV HOME=/root", final_stage)
        self.assertNotIn("install.sh", final_stage)
        self.assertNotIn("DockerInit.sh", final_stage)

    def test_02_xui_has_no_software_install_or_certificate_issuer(self):
        xui = self._read("x-ui.sh")
        for forbidden in (
            "get.acme.sh",
            "acme.sh",
            "certbot",
            "install_acme",
            "ssl_cert_issue",
            "run_speedtest",
            "speedtest",
            "apt-get",
            "apk add",
            "dnf ",
            "yum ",
            "zypper ",
            "pacman ",
            "snap install",
            "packagecloud",
            "releases/latest",
            "go install",
            "pip install",
            "npm install",
        ):
            self.assertNotIn(forbidden, xui, f"x-ui.sh must not contain {forbidden!r}")
        self.assertIsNone(
            re.search(r"curl[^\n|]*\|\s*(?:ba)?sh", xui),
            "x-ui.sh must not pipe downloaded content into a shell",
        )

    def test_03_removed_menu_controls_are_absent_and_range_is_contiguous(self):
        xui = self._read("x-ui.sh")
        for removed in (
            "SSL Certificate Management",
            "Cloudflare SSL Certificate",
            "Speedtest",
            "Ookla",
            "run_speedtest",
        ):
            self.assertNotIn(removed, xui)
        self.assertIn("14.${plain} TLS Certificate Paths", xui)
        self.assertIn("15.${plain} RouterOS Network Policy Notice", xui)
        self.assertIn("16.${plain} Database Tools", xui)
        self.assertIn("Please enter your selection [0-16]:", xui)
        self.assertIn('LOGE "Please enter the correct number [0-16]"', xui)
        self.assertIsNone(re.search(r"^\s*17\)", xui, re.MULTILINE))
        self.assertIsNone(re.search(r"^\s*18\)", xui, re.MULTILINE))

    def test_04_certificate_path_configuration_is_retained_and_local(self):
        xui = self._read("x-ui.sh")
        self.assertIn("tls_cert_path_menu()", xui)
        self.assertIn('x-ui cert -webCert "$webCertFile" -webCertKey "$webKeyFile"', xui)
        self.assertIn("local webCertFile=\"/root/cert/${domain}/fullchain.pem\"", xui)
        self.assertIn("local webKeyFile=\"/root/cert/${domain}/privkey.pem\"", xui)
        self.assertIn("x-ui restart", xui)

        main_go = self._read("main.go")
        match = re.search(r"func updateCert\(.*?\n}\n", main_go, re.DOTALL)
        self.assertIsNotNone(match, "main.go must retain updateCert for certificate path configuration")
        update_cert = match.group(0)
        self.assertIn("SetCertFile", update_cert)
        self.assertIn("SetKeyFile", update_cert)
        for forbidden in ("http.", "net/http", "exec.Command", "acme", "certbot"):
            self.assertNotIn(forbidden, update_cert, f"updateCert must not perform {forbidden!r}")

    def test_05_user_certificates_and_database_tools_are_not_removed(self):
        xui = self._read("x-ui.sh")
        self.assertIsNone(
            re.search(r"rm\s+-rf\s+[\"']?/root/cert", xui),
            "x-ui.sh must never delete the persistent certificate directory",
        )
        self.assertIn("database_tools_menu()", xui)
        self.assertIn("migrate_db()", xui)

        entrypoint = self._read("DockerEntrypoint.sh")
        self.assertNotIn("/root/cert", entrypoint)
        self.assertNotIn(".acme.sh", entrypoint)

    def test_06_current_docs_do_not_advertise_container_issuance_or_speedtest(self):
        for lang in ("en", "fa", "ru", "zh"):
            guide = self._read("docs", "content", "docs", lang, "config", "ssl-certificates.mdx")
            for forbidden in ("acme.sh", "get.acme.sh", "Cloudflare DNS-01", "Speedtest", "certbot renew"):
                self.assertNotIn(forbidden, guide, f"{lang} certificate guide must not contain {forbidden!r}")
            self.assertIn("fullchain.pem", guide)
            self.assertIn("privkey.pem", guide)
            self.assertIn("x-ui cert", guide)

            menu = self._read("docs", "content", "docs", lang, "guide", "update-uninstall.mdx")
            for forbidden in ("Speedtest", "Cloudflare SSL Certificate", "SSL Certificate Management", "0`–`18", "0` تا `18"):
                self.assertNotIn(forbidden, menu, f"{lang} menu guide must not contain {forbidden!r}")
            self.assertIn("TLS Certificate Paths", menu)

        overview = self._read("docs", "DOCKER_HUB_OVERVIEW.md")
        for forbidden in ("Automated SSL / ACME", "acme.sh", "Cloudflare DNS API", "issue/renew ACME"):
            self.assertNotIn(forbidden, overview)
        self.assertIn("does not issue, renew, or install certificates", overview)

        readme = self._read("README.md")
        self.assertNotIn("ACME state", readme)
        self.assertNotIn("Cron تمدید", readme)

        install_fa = self._read("docs", "MIKROZIA_CHR_INSTALL_FA.md")
        for forbidden in ("acme.sh", "MIKROZIA-ACME", "--install-cronjob", "get.acme.sh"):
            self.assertNotIn(forbidden, install_fa)
        self.assertIn("/root/cert", install_fa)

    def test_07_legacy_host_installer_is_removed(self):
        self.assertFalse(os.path.exists(os.path.join(self.repo_root, "install.sh")))

        compose = self._read("docker-compose.yml")
        self.assertNotIn(".acme.sh", compose)
        self.assertNotIn("acme", compose.lower())

    def test_08_panel_update_info_is_local_and_read_only(self):
        panel = self._read("internal", "web", "service", "panel", "panel.go")
        match = re.search(r"func \(s \*PanelService\) GetUpdateInfo\(.*?\n}\n", panel, re.DOTALL)
        self.assertIsNotNone(match)
        body = match.group(0)
        self.assertIn("UpdateAvailable: false", body)
        self.assertIn("config.GetMikroziaVersion()", body)
        for forbidden in ("http.", "net/http", "exec.Command", "releases/latest"):
            self.assertNotIn(forbidden, body)

    def test_09_entrypoint_has_no_filesystem_mutation_commands(self):
        """The entrypoint is a pure dispatcher; it must never write the container filesystem."""
        entrypoint = self._read("DockerEntrypoint.sh")

        mutation = re.compile(
            r"(?:^|[;&|()\s])"
            r"(?:mkdir|rmdir|touch|rm|mv|cp|ln|chmod|chown|unlink|mktemp|tee|dd|truncate|install)\b"
            r"|(?:^|[;&|()\s])sed\s+-i\b",
            re.MULTILINE,
        )
        match = mutation.search(entrypoint)
        if match:
            self.fail(f"DockerEntrypoint.sh must not run a filesystem-mutating command, found {match.group(0)!r}")
        self.assertNotIn(">", entrypoint, "DockerEntrypoint.sh must not use output redirection")
        self.assertNotIn("export HOME", entrypoint, "HOME is supplied by the image, not the entrypoint")


if __name__ == "__main__":
    unittest.main()
