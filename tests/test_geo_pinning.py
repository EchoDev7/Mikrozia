#!/usr/bin/env python3
"""
P0-GEO-01 build-contract tests.

The RouterOS image RootFS is immutable. Every Geo database bundled into it must
come from an exact upstream release tag, must match a recorded SHA-256 before
use, and must never be replaced by a runtime downloader (panel API, CLI, or
Xray-native geodata downloader). Geo browsing and routing tokens stay supported
because they only read the bundled files.
"""

import hashlib
import os
import re
import subprocess
import tempfile
import unittest

EXPECTED_FILES = {
    "geoip.dat": "Loyalsoldier/v2ray-rules-dat",
    "geosite.dat": "Loyalsoldier/v2ray-rules-dat",
    "geoip_IR.dat": "Chocolate4U/Iran-v2ray-rules",
    "geosite_IR.dat": "Chocolate4U/Iran-v2ray-rules",
    "geoip_RU.dat": "runetfreedom/russia-v2ray-rules-dat",
    "geosite_RU.dat": "runetfreedom/russia-v2ray-rules-dat",
}

URL_RE = re.compile(
    r"^https://github\.com/(?P<owner>[^/]+)/(?P<repo>[^/]+)/releases/download/"
    r"(?P<tag>[^/]+)/(?P<asset>[^/]+)$"
)


class TestGeoPinning(unittest.TestCase):
    def setUp(self):
        self.repo_root = os.path.abspath(os.path.join(os.path.dirname(__file__), ".."))
        self.manifest_path = os.path.join(self.repo_root, "GEO_DATA_SOURCES")
        self.dockerinit_path = os.path.join(self.repo_root, "DockerInit.sh")
        self.dockerfile_path = os.path.join(self.repo_root, "Dockerfile")
        self.xui_path = os.path.join(self.repo_root, "x-ui.sh")
        self.verify_helper = os.path.join(self.repo_root, "scripts", "build", "verify-geo-checksum.sh")

    def read(self, path):
        with open(path, "r", encoding="utf-8") as f:
            return f.read()

    def manifest_entries(self):
        self.assertTrue(os.path.isfile(self.manifest_path), "GEO_DATA_SOURCES must exist")
        entries = []
        for lineno, raw in enumerate(self.read(self.manifest_path).splitlines(), 1):
            line = raw.strip()
            if not line or line.startswith("#"):
                continue
            fields = line.split()
            self.assertEqual(len(fields), 3, f"GEO_DATA_SOURCES line {lineno} must be '<file> <url> <sha256>'")
            entries.append((lineno, fields[0], fields[1], fields[2]))
        return entries

    def test_manifest_pins_exact_tagged_releases_with_checksums(self):
        entries = self.manifest_entries()
        self.assertEqual(len(entries), len(EXPECTED_FILES), "manifest must list exactly the six bundled databases")

        seen_files, seen_urls = set(), set()
        for lineno, local_file, url, digest in entries:
            self.assertIn(local_file, EXPECTED_FILES, f"unexpected bundled database on line {lineno}")
            self.assertNotIn(local_file, seen_files, f"duplicate database '{local_file}'")
            seen_files.add(local_file)

            match = URL_RE.match(url)
            self.assertIsNotNone(match, f"line {lineno} must use a tagged GitHub release URL, got {url}")
            repo = f"{match.group('owner')}/{match.group('repo')}"
            self.assertEqual(repo, EXPECTED_FILES[local_file], f"wrong upstream for {local_file}")
            self.assertNotEqual(match.group("tag"), "latest", f"{local_file} must not track a mutable latest release")
            self.assertRegex(match.group("tag"), r"^[0-9]{8,}$", f"{local_file} must pin a dated release tag")
            self.assertEqual(
                match.group("asset"),
                "geoip.dat" if local_file.startswith("geoip") else "geosite.dat",
                f"wrong asset name for {local_file}",
            )
            self.assertRegex(digest, r"^[0-9a-f]{64}$", f"{local_file} must record a lowercase SHA-256")
            self.assertNotIn(url, seen_urls, f"duplicate URL for {local_file}")
            seen_urls.add(url)

        self.assertEqual(seen_files, set(EXPECTED_FILES))

    def test_docker_init_builds_geo_from_manifest_and_verifies_before_use(self):
        content = self.read(self.dockerinit_path)
        self.assertNotIn("releases/latest/download", content, "production image build must not use a mutable latest URL")
        self.assertIn('GEO_MANIFEST="${SCRIPT_DIR}/GEO_DATA_SOURCES"', content)
        self.assertIn('GEO_VERIFY_HELPER="${SCRIPT_DIR}/scripts/build/verify-geo-checksum.sh"', content)
        for repo in EXPECTED_FILES.values():
            self.assertNotIn(repo, content, "Geo URLs must live in GEO_DATA_SOURCES, not in DockerInit.sh")

        download_idx = content.index('dl -o "${geo_file}" "${geo_url}"')
        verify_idx = content.index('sh "${GEO_VERIFY_HELPER}" "${geo_file}" "${geo_sha}"')
        self.assertLess(download_idx, verify_idx, "each artifact must be downloaded then verified")
        self.assertLess(verify_idx, content.index("cd ../../"), "verification must happen during the build, before packaging")

    def test_runtime_cli_has_no_geo_updater(self):
        content = self.read(self.xui_path)
        for token in ("update-all-geofiles", "update_geofiles", "update_geo", "releases/latest"):
            self.assertNotIn(token, content, f"x-ui.sh must not expose a runtime Geo updater ({token})")
        for name in ("geoip.dat", "geosite.dat", "geoip_IR.dat", "geosite_IR.dat", "geoip_RU.dat", "geosite_RU.dat"):
            self.assertNotIn(name, content, f"x-ui.sh must not download or replace bundled {name} at runtime")

    def test_panel_api_has_no_runtime_geo_updater(self):
        controller = self.read(os.path.join(self.repo_root, "internal", "web", "controller", "server.go"))
        self.assertNotIn("updateGeofile", controller, "the panel API must not expose a Geo updater route")
        service = self.read(os.path.join(self.repo_root, "internal", "web", "service", "server.go"))
        self.assertNotIn("UpdateGeofile", service, "the panel service must not install Geo files at runtime")
        self.assertNotIn("StandardGeodataSources", service, "the panel must not recommend mutable download sources")

    def test_runtime_xray_config_omits_stored_geodata(self):
        service = self.read(os.path.join(self.repo_root, "internal", "web", "service", "xray.go"))
        self.assertIn("xrayConfig.Geodata = nil", service, "the runtime config must omit the Xray-native geodata downloader")

    def test_frontend_has_no_runtime_geo_update_controls(self):
        src_root = os.path.join(self.repo_root, "frontend", "src")
        offenders = []
        for root, _dirs, files in os.walk(src_root):
            if "generated" in root.split(os.sep):
                continue
            for name in files:
                if not name.endswith((".ts", ".tsx")):
                    continue
                path = os.path.join(root, name)
                content = self.read(path)
                if "updateGeofile" in content or "geodataSources" in content:
                    offenders.append(os.path.relpath(path, self.repo_root))
        self.assertEqual(offenders, [], f"Geo update controls must be removed from the UI: {offenders}")

    def test_docs_and_translations_do_not_advertise_runtime_geo_updates(self):
        docs_root = os.path.join(self.repo_root, "docs", "content")
        offenders = []
        for root, _dirs, files in os.walk(docs_root):
            for name in files:
                if not name.endswith((".mdx", ".md")):
                    continue
                path = os.path.join(root, name)
                content = self.read(path)
                if "update-all-geofiles" in content or "updateGeofile" in content:
                    offenders.append(os.path.relpath(path, self.repo_root))
        self.assertEqual(offenders, [], f"docs must not advertise removed Geo updaters: {offenders}")

        translation_dir = os.path.join(self.repo_root, "internal", "web", "translation")
        removed_keys = (
            "geofileUpdateDialog",
            "geofilesUpdateAll",
            "geofileUpdatePopover",
            "geodataTitle",
            "geodataSaveRestart",
            "geodataManagement",
        )
        locales = [f for f in os.listdir(translation_dir) if f.endswith(".json")]
        self.assertEqual(len(locales), 14, "all 14 locales must be present")
        for name in locales:
            content = self.read(os.path.join(translation_dir, name))
            for key in removed_keys:
                self.assertNotIn(f'"{key}"', content, f"{name} still carries removed key {key}")
            self.assertNotIn("Geodata Auto-Update", content, f"{name} still advertises Xray-native geodata updates")

    def test_image_ships_manifest_and_smoke_test_verifies_checksums(self):
        dockerfile = self.read(self.dockerfile_path)
        self.assertIn("COPY --from=builder /app/GEO_DATA_SOURCES /app/GEO_DATA_SOURCES", dockerfile)
        self.assertIn('./DockerInit.sh "$TARGETARCH" "$TARGETVARIANT" "$XRAY_VERSION" "$MTG_VERSION"', dockerfile)

        workflow = self.read(os.path.join(self.repo_root, ".github", "workflows", "docker.yml"))
        self.assertIn("sh -n scripts/build/verify-geo-checksum.sh", workflow)
        for name in EXPECTED_FILES:
            self.assertIn(f"/app/bin/{name}", workflow, f"smoke test must assert bundled {name}")
        self.assertIn("sha256sum -c -", workflow, "smoke test must verify bundled Geo checksums")

    def test_verify_helper_fails_closed(self):
        with tempfile.TemporaryDirectory() as tmpdir:
            good = os.path.join(tmpdir, "geoip.dat")
            with open(good, "wb") as f:
                f.write(b"mikrozia-geo-fixture")
            digest = hashlib.sha256(b"mikrozia-geo-fixture").hexdigest()

            def run(*args):
                return subprocess.run(["sh", self.verify_helper, *args], capture_output=True, text=True)

            self.assertEqual(run(good, digest).returncode, 0, "matching checksum must pass")
            self.assertEqual(run(good, digest.upper()).returncode, 0, "uppercase checksum must pass")
            self.assertNotEqual(run(good, "0" * 64).returncode, 0, "mismatched checksum must fail")
            self.assertNotEqual(run(good, "abc").returncode, 0, "short checksum must fail")
            self.assertNotEqual(run(good, "z" * 64).returncode, 0, "non-hex checksum must fail")

            empty = os.path.join(tmpdir, "empty.dat")
            open(empty, "wb").close()
            self.assertNotEqual(run(empty, digest).returncode, 0, "empty artifact must fail")
            self.assertNotEqual(run(os.path.join(tmpdir, "missing.dat"), digest).returncode, 0)
            self.assertNotEqual(run(good).returncode, 0, "missing checksum argument must fail")


if __name__ == "__main__":
    unittest.main()
