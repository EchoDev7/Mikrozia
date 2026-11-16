#!/usr/bin/env python3
"""
Unit and Integration Tests for Mikrozia Container Alpine Fixes & Runtime Immutability.

Validates:
- FIX 1: Shell scripts syntax validation (DockerEntrypoint.sh, x-ui.sh).
- FIX 2: Dockerfile Alpine hardening (HOME=/root, no firewall or certificate-issuer tooling).
- FIX 3: Alpine management script bug fixes (log checks).
- FIX 4: Minimal entrypoint with no runtime certificate or scheduler side effects.
- FIX 5: RouterOS lifecycle behavior and menu numbering.
"""

import os
import re
import shutil
import stat
import subprocess
import tempfile
import unittest


class TestContainerFixes(unittest.TestCase):
    def setUp(self):
        self.repo_root = os.path.abspath(os.path.join(os.path.dirname(__file__), ".."))
        self.entrypoint_path = os.path.join(self.repo_root, "DockerEntrypoint.sh")
        self.xui_sh_path = os.path.join(self.repo_root, "x-ui.sh")
        self.dockerfile_path = os.path.join(self.repo_root, "Dockerfile")

    def test_01_shell_scripts_syntax(self):
        """Validates that DockerEntrypoint.sh and x-ui.sh have valid shell syntax."""
        p1 = subprocess.run(["bash", "-n", self.entrypoint_path], capture_output=True, text=True)
        self.assertEqual(p1.returncode, 0, f"DockerEntrypoint.sh syntax error: {p1.stderr}")

        p2 = subprocess.run(["bash", "-n", self.xui_sh_path], capture_output=True, text=True)
        self.assertEqual(p2.returncode, 0, f"x-ui.sh syntax error: {p2.stderr}")

    def test_02_dockerfile_alpine_hardening(self):
        """Verifies Dockerfile hardening and the absence of firewall or certificate-issuer tooling."""
        with open(self.dockerfile_path, "r", encoding="utf-8") as f:
            content = f.read()

        self.assertIn("ENV HOME=/root", content)
        self.assertIn("coreutils", content)
        self.assertIn("ca-certificates", content)
        self.assertIn("openssl", content)
        self.assertNotIn("socat", content)
        self.assertNotIn("fail2ban", content.lower())
        self.assertNotIn("iptables", content.lower())
        self.assertNotIn("nftables", content.lower())
        self.assertNotIn("ufw", content.lower())

    def test_03_xui_script_bug_fixes(self):
        """Verifies x-ui.sh uses a guarded 3xui.log check and no stale relative cert listing."""
        with open(self.xui_sh_path, "r", encoding="utf-8") as f:
            content = f.read()

        # Legacy unguarded /var/log/messages grep must be replaced by guarded 3xui.log check
        self.assertNotIn("grep -F 'x-ui[' /var/log/messages", content)
        self.assertIn('if [[ -f "${app_log}" ]]; then', content)

    def test_15_subscription_and_panel_tls_certificate_paths_consistency(self):
        """Verifies certificate path examples use .pem files and path configuration remains the only cert UI action."""
        overview_path = os.path.join(self.repo_root, "docs", "DOCKER_HUB_OVERVIEW.md")
        readme_path = os.path.join(self.repo_root, "README.md")

        with open(readme_path, "r", encoding="utf-8") as f:
            readme_content = f.read()

        with open(overview_path, "r", encoding="utf-8") as f:
            overview_content = f.read()

        with open(self.xui_sh_path, "r", encoding="utf-8") as f:
            xui_content = f.read()

        # 1. Negative checks: no obsolete .cer / .key paths in installed path examples
        self.assertNotIn("/root/cert/vpn.yourdomain.com/fullchain.cer", readme_content)
        self.assertNotIn("/root/cert/vpn.yourdomain.com/vpn.yourdomain.com.key", readme_content)
        self.assertNotIn("/root/cert/vpn.yourdomain.com/fullchain.cer", overview_content)
        self.assertNotIn("/root/cert/vpn.yourdomain.com/vpn.yourdomain.com.key", overview_content)

        # 2. Positive checks: exact installed .pem paths
        self.assertIn("/root/cert/vpn.yourdomain.com/fullchain.pem", readme_content)
        self.assertIn("/root/cert/vpn.yourdomain.com/privkey.pem", readme_content)
        self.assertIn("/root/cert/vpn.yourdomain.com/fullchain.pem", overview_content)
        self.assertIn("/root/cert/vpn.yourdomain.com/privkey.pem", overview_content)

        # 3. The menu only records user-supplied paths; it never issues or renews
        self.assertIn("TLS Certificate Paths", xui_content)
        self.assertIn('x-ui cert -webCert "$webCertFile" -webCertKey "$webKeyFile"', xui_content)
        self.assertIn('local webCertFile="/root/cert/${domain}/fullchain.pem"', xui_content)
        self.assertIn('local webKeyFile="/root/cert/${domain}/privkey.pem"', xui_content)
        self.assertIn("Mikrozia does not issue or renew TLS certificates", xui_content)
        self.assertNotIn("acme.sh", xui_content)
        self.assertNotIn("--installcert", xui_content)

    def test_16_mikrozia_version_consistency(self):
        """Verifies MIKROZIA_VERSION is the only release input and config.go keeps no hard-coded fallback."""
        version_file = os.path.join(self.repo_root, "MIKROZIA_VERSION")
        with open(version_file, "r", encoding="utf-8", newline="") as f:
            version_expected = f.read()

        self.assertFalse(
            any(char.isspace() for char in version_expected),
            "MIKROZIA_VERSION must contain no whitespace",
        )
        self.assertIsNotNone(
            re.fullmatch(r"v(?:\d+\.\d+\.\d+-mikrozia\.\d+|\d+-mikrozia)", version_expected),
            "MIKROZIA_VERSION must hold a valid release tag",
        )

        config_go = os.path.join(self.repo_root, "internal", "config", "config.go")
        with open(config_go, "r", encoding="utf-8") as f:
            config_content = f.read()

        self.assertNotIn(
            "defaultMikroziaVersion",
            config_content,
            "config.go must not hard-code a production fallback; the release version comes from ldflags",
        )
        self.assertNotIn(
            f'"{version_expected}"',
            config_content,
            "config.go must not embed the canonical release literal",
        )

    def test_17_xui_cli_no_install_uninstall_update(self):
        """Verifies x-ui.sh has removed all dangerous update, install, and uninstall subcommands."""
        import re
        with open(self.xui_sh_path, "r", encoding="utf-8") as f:
            xui_content = f.read()

        # 1. Functions must not exist
        self.assertFalse(re.search(r"^\s*install\(\)\s*\{", xui_content, re.MULTILINE), "install() function must not exist in x-ui.sh")
        self.assertFalse(re.search(r"^\s*uninstall\(\)\s*\{", xui_content, re.MULTILINE), "uninstall() function must not exist in x-ui.sh")
        self.assertFalse(re.search(r"^\s*update\(\)\s*\{", xui_content, re.MULTILINE), "update() function must not exist in x-ui.sh")
        self.assertFalse(re.search(r"^\s*update_dev\(\)\s*\{", xui_content, re.MULTILINE), "update_dev() function must not exist in x-ui.sh")

        # 2. Subcommands in CLI dispatch must not exist
        self.assertFalse(re.search(r'^\s*"install"\)', xui_content, re.MULTILINE), '"install" subcommand must not exist in x-ui.sh')
        self.assertFalse(re.search(r'^\s*"uninstall"\)', xui_content, re.MULTILINE), '"uninstall" subcommand must not exist in x-ui.sh')
        self.assertFalse(re.search(r'^\s*"update"\)', xui_content, re.MULTILINE), '"update" subcommand must not exist in x-ui.sh')
        self.assertFalse(re.search(r'^\s*"update-dev"\)', xui_content, re.MULTILINE), '"update-dev" subcommand must not exist in x-ui.sh')
        self.assertFalse(re.search(r'^\s*"update-menu"\)', xui_content, re.MULTILINE), '"update-menu" subcommand must not exist in x-ui.sh')

    def test_18_xray_version_consistency_and_pinning(self):
        """Verifies Xray is pinned inside the image and XRAY_VERSION is wired cleanly across build and CI files."""
        xray_version_file = os.path.join(self.repo_root, "XRAY_VERSION")
        self.assertTrue(os.path.isfile(xray_version_file), "XRAY_VERSION file must exist")
        with open(xray_version_file, "r", encoding="utf-8") as f:
            xray_ver = f.read().strip()

        self.assertRegex(xray_ver, r"^v\d+\.\d+\.\d+$", "XRAY_VERSION must match vMAJOR.MINOR.PATCH format")

        with open(self.dockerfile_path, "r", encoding="utf-8") as f:
            dockerfile_content = f.read()

        self.assertNotIn(f"ARG XRAY_VERSION={xray_ver}", dockerfile_content, "Dockerfile must not hardcode XRAY_VERSION default")
        self.assertNotIn("ARG XRAY_VERSION=v", dockerfile_content, "Dockerfile must not hardcode any XRAY_VERSION default")
        self.assertIn("ARG XRAY_VERSION", dockerfile_content, "Dockerfile must declare ARG XRAY_VERSION")
        self.assertIn("^v[0-9]+\\.[0-9]+\\.[0-9]+$", dockerfile_content, "Dockerfile must validate XRAY_VERSION format")
        self.assertIn('LABEL org.opencontainers.image.title="Mikrozia"', dockerfile_content)
        self.assertIn('io.mikrozia.xray.version="${XRAY_VERSION}"', dockerfile_content, "Dockerfile must label image with pinned Xray version")
        self.assertIn('RUN ./DockerInit.sh "$TARGETARCH" "$TARGETVARIANT" "$XRAY_VERSION" "$MTG_VERSION"', dockerfile_content)
        final_stage = dockerfile_content[dockerfile_content.index("Final Image"):]
        self.assertNotIn("COPY scripts", final_stage, "Final image must not copy build helper scripts")

        dockerinit_path = os.path.join(self.repo_root, "DockerInit.sh")
        with open(dockerinit_path, "r", encoding="utf-8") as f:
            dockerinit_content = f.read()

        self.assertIn("scripts/build/verify-xray-checksum.sh", dockerinit_content, "DockerInit.sh must delegate checksum verification to helper script")
        self.assertIn("cleanup_xray_tmp", dockerinit_content, "DockerInit.sh must clean temporary files on failure")

        docker_yml_path = os.path.join(self.repo_root, ".github", "workflows", "docker.yml")
        with open(docker_yml_path, "r", encoding="utf-8") as f:
            docker_yml_content = f.read()

        self.assertIn("xray_version:", docker_yml_content, "docker.yml validate job must output xray_version")
        self.assertIn("XRAY_VERSION=${{ env.XRAY_VERSION }}", docker_yml_content, "docker.yml must pass XRAY_VERSION build-arg")
        self.assertIn("io.mikrozia.xray.version=${{ env.XRAY_VERSION }}", docker_yml_content, "docker.yml must set io.mikrozia.xray.version OCI label")
        self.assertIn('--entrypoint "/app/bin/${{ matrix.xray_bin }}"', docker_yml_content, "docker.yml smoke test must verify the per-architecture Xray binary")
        self.assertIn("xray_bin: xray-linux-amd64", docker_yml_content, "docker.yml matrix must name the amd64 Xray binary")
        self.assertIn('grep -Fq "Xray ${xray_no_v}"', docker_yml_content, "docker.yml smoke test must verify pinned Xray version output")
        self.assertIn("/server/getXrayVersion", docker_yml_content, "docker.yml smoke test must verify absence of runtime Xray installer")

    def test_19_verify_xray_checksum_behavior(self):
        """Verifies build-only checksum script passes on valid digests and fails closed on invalid inputs."""
        import hashlib
        import tempfile

        verify_script = os.path.join(self.repo_root, "scripts", "build", "verify-xray-checksum.sh")
        self.assertTrue(os.path.isfile(verify_script), "verify-xray-checksum.sh must exist")

        with tempfile.TemporaryDirectory() as tmpdir:
            archive_path = os.path.join(tmpdir, "Xray-linux-64.zip")
            dgst_path = os.path.join(tmpdir, "Xray-linux-64.zip.dgst")
            payload = b"mikrozia-pinned-xray-binary-test-payload"
            with open(archive_path, "wb") as f:
                f.write(payload)
            digest_hex = hashlib.sha256(payload).hexdigest()

            def run_verify(dgst_content, env_override=None):
                with open(dgst_path, "w", encoding="utf-8") as f:
                    f.write(dgst_content)
                return subprocess.run(
                    ["/bin/sh", verify_script, archive_path, dgst_path],
                    capture_output=True,
                    text=True,
                    env=env_override,
                )

            # 1. Valid lowercase and uppercase SHA2-256 digests pass
            res_lower = run_verify(f"MD5= d41d8cd98f00b204e9800998ecf8427e\nSHA2-256= {digest_hex}\n")
            self.assertEqual(res_lower.returncode, 0, res_lower.stderr)

            res_upper = run_verify(f"SHA2-256={digest_hex.upper()}\n")
            self.assertEqual(res_upper.returncode, 0, res_upper.stderr)

            # 2. Mismatch checksum fails
            wrong_hex = ("0" if digest_hex[0] != "0" else "1") + digest_hex[1:]
            res_mismatch = run_verify(f"SHA2-256= {wrong_hex}\n")
            self.assertNotEqual(res_mismatch.returncode, 0)
            self.assertIn("checksum mismatch", res_mismatch.stderr.lower())

            # 3. Empty digest fails
            res_empty = run_verify("SHA2-256= \n")
            self.assertNotEqual(res_empty.returncode, 0)

            # 4. Short digest (<64 hex) fails
            res_short = run_verify(f"SHA2-256= {digest_hex[:32]}\n")
            self.assertNotEqual(res_short.returncode, 0)

            # 5. 64-char non-hex digest fails
            res_nonhex = run_verify(f"SHA2-256= {'g' * 64}\n")
            self.assertNotEqual(res_nonhex.returncode, 0)

            # 6. Multiple SHA2-256 entries fail
            res_multi = run_verify(f"SHA2-256= {digest_hex}\nSHA2-256= {digest_hex}\n")
            self.assertNotEqual(res_multi.returncode, 0)

            # 7. Missing sha256sum and shasum fails with clear error
            restricted_bin = os.path.join(tmpdir, "restricted_bin")
            os.makedirs(restricted_bin, exist_ok=True)
            for cmd_name in ("grep", "sed", "tr", "awk", "wc"):
                for search_dir in ("/usr/bin", "/bin"):
                    src_bin = os.path.join(search_dir, cmd_name)
                    if os.path.exists(src_bin):
                        os.symlink(src_bin, os.path.join(restricted_bin, cmd_name))
                        break
            env_no_sha = {"PATH": restricted_bin}
            res_no_sha = run_verify(f"SHA2-256= {digest_hex}\n", env_override=env_no_sha)
            self.assertNotEqual(res_no_sha.returncode, 0)
            self.assertIn("neither sha256sum nor shasum", res_no_sha.stderr.lower())

    def test_20_mtg_version_consistency_and_pinning(self):
        """Verifies mtg-multi is pinned in MTG_VERSION and wired cleanly across build, release, CI, and docs."""
        mtg_version_file = os.path.join(self.repo_root, "MTG_VERSION")
        self.assertTrue(os.path.isfile(mtg_version_file), "MTG_VERSION file must exist")
        with open(mtg_version_file, "r", encoding="utf-8") as f:
            mtg_ver = f.read().strip()

        self.assertRegex(mtg_ver, r"^v\d+\.\d+\.\d+$", "MTG_VERSION must match vMAJOR.MINOR.PATCH format")

        with open(self.dockerfile_path, "r", encoding="utf-8") as f:
            dockerfile_content = f.read()

        self.assertNotIn(f"ARG MTG_VERSION={mtg_ver}", dockerfile_content, "Dockerfile must not hardcode MTG_VERSION default")
        self.assertNotIn("ARG MTG_VERSION=v", dockerfile_content, "Dockerfile must not hardcode any MTG_VERSION default")
        self.assertIn("ARG MTG_VERSION", dockerfile_content, "Dockerfile must declare ARG MTG_VERSION")
        self.assertIn('test -n "${MTG_VERSION}"', dockerfile_content, "Dockerfile must validate MTG_VERSION in build/final stages")
        self.assertIn('io.mikrozia.mtg.version="${MTG_VERSION}"', dockerfile_content, "Dockerfile must label image with pinned MTG version")
        self.assertIn('RUN ./DockerInit.sh "$TARGETARCH" "$TARGETVARIANT" "$XRAY_VERSION" "$MTG_VERSION"', dockerfile_content)
        final_stage = dockerfile_content[dockerfile_content.index("Final Image"):]
        self.assertNotIn("COPY scripts", final_stage, "Final image must not copy build helper scripts")

        dockerinit_path = os.path.join(self.repo_root, "DockerInit.sh")
        with open(dockerinit_path, "r", encoding="utf-8") as f:
            dockerinit_content = f.read()

        self.assertNotIn("mhsanaei/mtg-multi/releases/latest", dockerinit_content, "DockerInit.sh must not fetch mtg-multi releases/latest")
        self.assertNotIn(f'"{mtg_ver}"', dockerinit_content, "DockerInit.sh must not hardcode fallback MTG version")
        self.assertNotIn("MTG_MULTI_VER", dockerinit_content, "DockerInit.sh must use MTG_VERSION rather than legacy MTG_MULTI_VER lookup")
        self.assertIn("scripts/build/verify-mtg-checksum.sh", dockerinit_content, "DockerInit.sh must verify mtg-multi archive checksum via helper script")
        self.assertIn("cleanup_mtg_tmp", dockerinit_content, "DockerInit.sh must clean mtg temporary files on failure")
        verify_idx = dockerinit_content.index("VERIFY_MTG_HELPER")
        tar_idx = dockerinit_content.index('tar -xzf "${MTG_ARCHIVE}"')
        helper_call_idx = dockerinit_content.index('sh "${VERIFY_MTG_HELPER}" "${MTG_ARCHIVE}" "${MTG_CHECKSUMS}" "${MTG_ARCHIVE}"')
        self.assertGreater(verify_idx, 0)
        self.assertLess(helper_call_idx, tar_idx, "DockerInit.sh must verify mtg-multi checksum before extracting tarball")
        self.assertIn('MTG_BIN="mtg-linux-arm"', dockerinit_content)
        self.assertIn('MTG_BIN="mtg-linux-${PLATFORM}"', dockerinit_content)
        self.assertNotIn("/releases/latest/", dockerinit_content, "DockerInit.sh must never download from a mutable releases/latest URL")
        self.assertIn("GEO_DATA_SOURCES", dockerinit_content, "DockerInit.sh must read the pinned Geo data manifest")
        self.assertIn("verify-geo-checksum.sh", dockerinit_content, "DockerInit.sh must verify bundled Geo data before use")

        docker_yml_path = os.path.join(self.repo_root, ".github", "workflows", "docker.yml")
        with open(docker_yml_path, "r", encoding="utf-8") as f:
            docker_yml_content = f.read()

        self.assertIn("mtg_version:", docker_yml_content, "docker.yml validate job must output mtg_version")
        self.assertIn("sh -n scripts/build/verify-mtg-checksum.sh", docker_yml_content, "docker.yml must syntax-check verify-mtg-checksum.sh")
        self.assertIn("MTG_VERSION=${{ env.MTG_VERSION }}", docker_yml_content, "docker.yml must pass MTG_VERSION build-arg")
        self.assertIn("io.mikrozia.mtg.version=${{ env.MTG_VERSION }}", docker_yml_content, "docker.yml must set io.mikrozia.mtg.version OCI label")
        self.assertIn('--entrypoint "/app/bin/${{ matrix.mtg_bin }}"', docker_yml_content, "docker.yml smoke test must verify the per-architecture mtg binary")
        self.assertIn("mtg_bin: mtg-linux-amd64", docker_yml_content, "docker.yml matrix must name the amd64 mtg binary")
        self.assertIn('grep -Fq "${mtg_no_v}"', docker_yml_content, "docker.yml smoke test must verify pinned mtg version output")
        self.assertIn("test ! -e /app/scripts/build/verify-mtg-checksum.sh", docker_yml_content, "docker.yml smoke test must verify helper script is excluded from runtime image")

        readme_path = os.path.join(self.repo_root, "README.md")
        with open(readme_path, "r", encoding="utf-8") as f:
            readme_content = f.read()
        self.assertIn('--build-arg MTG_VERSION="$(tr -d \'[:space:]\' < MTG_VERSION)"', readme_content, "README.md local build instructions must include MTG_VERSION build-arg")

    def test_21_verify_mtg_checksum_behavior(self):
        """Verifies verify-mtg-checksum.sh passes on valid manifest entries and fails closed on all invalid cases."""
        import hashlib
        import tempfile

        verify_script = os.path.join(self.repo_root, "scripts", "build", "verify-mtg-checksum.sh")
        self.assertTrue(os.path.isfile(verify_script), "verify-mtg-checksum.sh must exist")

        syntax_res = subprocess.run(["/bin/sh", "-n", verify_script], capture_output=True, text=True)
        self.assertEqual(syntax_res.returncode, 0, f"verify-mtg-checksum.sh syntax error: {syntax_res.stderr}")

        with tempfile.TemporaryDirectory() as tmpdir:
            archive_name = "mtg-multi-1.16.1-linux-amd64.tar.gz"
            archive_path = os.path.join(tmpdir, archive_name)
            checksums_path = os.path.join(tmpdir, "mtg-multi-1.16.1-checksums.txt")
            payload = b"mikrozia-pinned-mtg-multi-archive-test-payload"
            with open(archive_path, "wb") as f:
                f.write(payload)
            digest_hex = hashlib.sha256(payload).hexdigest()
            other_hex = "a" * 64
            other_name = "mtg-multi-1.16.1-linux-arm64.tar.gz"

            def run_verify(manifest_content, expected_name=archive_name, env_override=None):
                with open(checksums_path, "w", encoding="utf-8") as f:
                    f.write(manifest_content)
                return subprocess.run(
                    ["/bin/sh", verify_script, archive_path, checksums_path, expected_name],
                    capture_output=True,
                    text=True,
                    env=env_override,
                )

            # 1. Valid GoReleaser manifest with multiple architectures (lowercase digest)
            res_valid = run_verify(f"{other_hex}  {other_name}\n{digest_hex}  {archive_name}\n")
            self.assertEqual(res_valid.returncode, 0, res_valid.stderr)

            # 2. Valid uppercase digest, asterisk binary marker, and CRLF line endings
            res_upper_crlf = run_verify(f"{other_hex} *{other_name}\r\n{digest_hex.upper()} *{archive_name}\r\n")
            self.assertEqual(res_upper_crlf.returncode, 0, res_upper_crlf.stderr)

            # 3. Mismatched SHA-256 digest fails
            wrong_hex = ("0" if digest_hex[0] != "0" else "1") + digest_hex[1:]
            res_mismatch = run_verify(f"{wrong_hex}  {archive_name}\n")
            self.assertNotEqual(res_mismatch.returncode, 0)
            self.assertIn("checksum mismatch", res_mismatch.stderr.lower())

            # 4. Missing entry (empty manifest or only other arch present) fails
            res_empty = run_verify("")
            self.assertNotEqual(res_empty.returncode, 0)
            self.assertIn("missing checksum entry", res_empty.stderr.lower())

            res_missing_target = run_verify(f"{other_hex}  {other_name}\n")
            self.assertNotEqual(res_missing_target.returncode, 0)
            self.assertIn("missing checksum entry", res_missing_target.stderr.lower())

            # 5. Wrong/substring filename fails (must be exact match)
            res_partial_name = run_verify(f"{digest_hex}  {archive_name}.bak\n")
            self.assertNotEqual(res_partial_name.returncode, 0)

            # 6. Malformed manifest lines (short hash, non-hex char, extra field) fail even when valid target line also exists
            res_short = run_verify(f"{digest_hex[:32]}  {archive_name}\n")
            self.assertNotEqual(res_short.returncode, 0)
            self.assertIn("malformed", res_short.stderr.lower())

            res_nonhex = run_verify(f"{'z' * 64}  {archive_name}\n")
            self.assertNotEqual(res_nonhex.returncode, 0)
            self.assertIn("malformed", res_nonhex.stderr.lower())

            res_extra_field = run_verify(f"{other_hex}  {other_name} extra\n{digest_hex}  {archive_name}\n")
            self.assertNotEqual(res_extra_field.returncode, 0)
            self.assertIn("malformed", res_extra_field.stderr.lower())

            # 7. Duplicate entries (both identical and conflicting) fail
            res_dup_same = run_verify(f"{digest_hex}  {archive_name}\n{digest_hex}  {archive_name}\n")
            self.assertNotEqual(res_dup_same.returncode, 0)
            self.assertIn("duplicate", res_dup_same.stderr.lower())

            res_dup_diff = run_verify(f"{wrong_hex}  {archive_name}\n{digest_hex}  {archive_name}\n")
            self.assertNotEqual(res_dup_diff.returncode, 0)
            self.assertIn("duplicate", res_dup_diff.stderr.lower())

            # 8. Missing sha256sum and shasum fails cleanly
            restricted_bin = os.path.join(tmpdir, "restricted_bin")
            os.makedirs(restricted_bin, exist_ok=True)
            for cmd_name in ("grep", "sed", "tr", "awk", "basename"):
                for search_dir in ("/usr/bin", "/bin"):
                    src_bin = os.path.join(search_dir, cmd_name)
                    if os.path.exists(src_bin):
                        os.symlink(src_bin, os.path.join(restricted_bin, cmd_name))
                        break
            env_no_sha = {"PATH": restricted_bin}
            res_no_sha = run_verify(f"{digest_hex}  {archive_name}\n", env_override=env_no_sha)
            self.assertNotEqual(res_no_sha.returncode, 0)
            self.assertIn("neither sha256sum nor shasum", res_no_sha.stderr.lower())

    def test_22_dockerinit_mtg_fail_closed(self):
        """Verifies DockerInit.sh fails closed on empty/invalid MTG_VERSION or unsupported arch before any download."""
        import shutil
        import tempfile

        src_dockerinit = os.path.join(self.repo_root, "DockerInit.sh")
        with open(os.path.join(self.repo_root, "XRAY_VERSION"), "r", encoding="utf-8") as f:
            xray_ver = f.read().strip()
        with open(os.path.join(self.repo_root, "MTG_VERSION"), "r", encoding="utf-8") as f:
            mtg_ver = f.read().strip()

        with tempfile.TemporaryDirectory() as tmpdir:
            dst_dockerinit = os.path.join(tmpdir, "DockerInit.sh")
            shutil.copy2(src_dockerinit, dst_dockerinit)
            os.makedirs(os.path.join(tmpdir, "scripts", "build"), exist_ok=True)
            shutil.copy2(
                os.path.join(self.repo_root, "scripts", "build", "verify-xray-checksum.sh"),
                os.path.join(tmpdir, "scripts", "build", "verify-xray-checksum.sh"),
            )
            shutil.copy2(
                os.path.join(self.repo_root, "scripts", "build", "verify-mtg-checksum.sh"),
                os.path.join(tmpdir, "scripts", "build", "verify-mtg-checksum.sh"),
            )

            syntax_res = subprocess.run(["/bin/sh", "-n", dst_dockerinit], capture_output=True, text=True)
            self.assertEqual(syntax_res.returncode, 0, syntax_res.stderr)

            # Intentionally exclude MTG_VERSION file in tmpdir to prove empty version fails closed
            clean_env = {"PATH": "/usr/bin:/bin"}

            for bad_mtg in ("", "latest", "1.16.1", "v1.16"):
                res = subprocess.run(
                    ["/bin/sh", dst_dockerinit, "amd64", "", xray_ver, bad_mtg],
                    cwd=tmpdir,
                    env=clean_env,
                    capture_output=True,
                    text=True,
                )
                self.assertNotEqual(res.returncode, 0, f"DockerInit.sh must fail for MTG_VERSION={bad_mtg!r}")
                self.assertIn("MTG_VERSION", res.stderr)

            # Valid XRAY_VERSION + valid MTG_VERSION but unsupported arch must fail immediately
            res_arch = subprocess.run(
                ["/bin/sh", dst_dockerinit, "s390x", "", xray_ver, mtg_ver],
                cwd=tmpdir,
                env=clean_env,
                capture_output=True,
                text=True,
            )
            self.assertNotEqual(res_arch.returncode, 0)
            self.assertIn("unsupported architecture", res_arch.stderr.lower())

    def test_23_entrypoint_bash_shebang_and_no_runtime_side_effects(self):
        """Ensures DockerEntrypoint.sh is a minimal exec wrapper with no ACME/cron/package-manager/filesystem side effects."""
        with open(self.entrypoint_path, "r", encoding="utf-8") as f:
            entrypoint = f.read()

        first_line = entrypoint.splitlines()[0] if entrypoint else ""
        self.assertEqual(first_line, "#!/bin/bash")
        self.assertIn('exec /app/x-ui', entrypoint)
        self.assertNotIn("# shellcheck disable=", entrypoint)
        for forbidden in ("acme", "ACME", "crond", "crontab", "apk", "apt", "curl", "wget"):
            self.assertNotIn(forbidden, entrypoint)

        mutation = re.compile(
            r"(?:^|[;&|()\s])"
            r"(?:mkdir|rmdir|touch|rm|mv|cp|ln|chmod|chown|unlink|mktemp|tee|dd|truncate|install)\b"
            r"|(?:^|[;&|()\s])sed\s+-i\b",
            re.MULTILINE,
        )
        match = mutation.search(entrypoint)
        if match:
            self.fail(f"DockerEntrypoint.sh must not mutate the filesystem, found {match.group(0)!r}")
        self.assertNotIn(">", entrypoint, "DockerEntrypoint.sh must not redirect output")

        with open(self.dockerfile_path, "r", encoding="utf-8") as f:
            dockerfile = f.read()
        self.assertIn("apk add --no-cache", dockerfile)
        final_stage = dockerfile.split("FROM alpine:", 1)[-1]
        self.assertIn("bash", final_stage)
        self.assertIn("ENV HOME=/root", final_stage)
        self.assertNotIn("socat", final_stage)

        docker_wf_path = os.path.join(self.repo_root, ".github", "workflows", "docker.yml")
        with open(docker_wf_path, "r", encoding="utf-8") as f:
            docker_wf = f.read()
        syntax_lines = [line.strip() for line in docker_wf.splitlines() if "DockerEntrypoint.sh" in line and "-n " in line]
        self.assertEqual(syntax_lines, ["bash -n DockerEntrypoint.sh"])

        res = subprocess.run(
            [self.entrypoint_path, "sh", "-c", "printf ENTRYPOINT_PASSTHROUGH"],
            capture_output=True,
            text=True,
        )
        self.assertEqual(res.returncode, 0, res.stderr)
        self.assertEqual(res.stdout, "ENTRYPOINT_PASSTHROUGH")

    def _prepare_xui_harness(self, tmpdir):
        """Creates an executable test harness wrapping the real x-ui.sh with mocked host preamble & ps."""
        import stat as stat_mod

        app_dir = os.path.join(tmpdir, "app")
        log_dir = os.path.join(tmpdir, "logs")
        bin_dir = os.path.join(tmpdir, "bin")
        os.makedirs(app_dir, exist_ok=True)
        os.makedirs(log_dir, exist_ok=True)
        os.makedirs(bin_dir, exist_ok=True)

        fake_bin = os.path.join(app_dir, "x-ui")
        with open(fake_bin, "w", encoding="utf-8") as f:
            f.write("#!/bin/sh\necho fake-x-ui \"$@\"\n")
        os.chmod(fake_bin, os.stat(fake_bin).st_mode | stat_mod.S_IXUSR)

        mock_ps = os.path.join(bin_dir, "ps")
        with open(mock_ps, "w", encoding="utf-8") as f:
            f.write(
                "#!/bin/sh\n"
                "if [ -n \"${MOCK_PANEL_PID}\" ]; then\n"
                "  echo \"${MOCK_PANEL_PID} root 0:00 ${XUI_MAIN_FOLDER}/x-ui\"\n"
                "fi\n"
                "if [ \"${MOCK_XRAY_RUNNING}\" = \"1\" ]; then\n"
                "  echo \"99999 root 0:00 xray-linux-amd64\"\n"
                "fi\n"
            )
        os.chmod(mock_ps, os.stat(mock_ps).st_mode | stat_mod.S_IXUSR)

        mock_curl = os.path.join(bin_dir, "curl")
        with open(mock_curl, "w", encoding="utf-8") as f:
            f.write("#!/bin/sh\nprintf '203.0.113.10\\n200'\n")
        os.chmod(mock_curl, os.stat(mock_curl).st_mode | stat_mod.S_IXUSR)

        with open(self.xui_sh_path, "r", encoding="utf-8") as f:
            script = f.read()

        # Bypass host-only UID=0 and /etc/os-release checks so the real bash functions run on any test host.
        root_check = '[[ $EUID -ne 0 ]] && LOGE "ERROR: You must be root to run this script! \\n" && exit 1'
        self.assertIn(root_check, script)
        script = script.replace(root_check, ": # root check bypassed in test harness", 1)

        os_release_block = (
            'if [[ -f /etc/os-release ]]; then\n'
            '    source /etc/os-release\n'
            '    release=$ID\n'
            'elif [[ -f /usr/lib/os-release ]]; then\n'
            '    source /usr/lib/os-release\n'
            '    release=$ID\n'
            'else\n'
            '    echo "Failed to check the system OS, please contact the author!" >&2\n'
            '    exit 1\n'
            'fi'
        )
        self.assertIn(os_release_block, script)
        script = script.replace(os_release_block, 'release="alpine"', 1)

        version_line = 'os_version=$(grep "^VERSION_ID" /etc/os-release | cut -d \'=\' -f2 | tr -d \'"\' | tr -d \'.\')'
        self.assertIn(version_line, script)
        script = script.replace(version_line, 'os_version="320"', 1)

        harness_path = os.path.join(tmpdir, "x-ui-harness.sh")
        with open(harness_path, "w", encoding="utf-8") as f:
            f.write(script)
        os.chmod(harness_path, os.stat(harness_path).st_mode | stat_mod.S_IXUSR)

        env = os.environ.copy()
        env["PATH"] = f"{bin_dir}:{env.get('PATH', '/usr/bin:/bin')}"
        env["XUI_IN_DOCKER"] = "true"
        env["XUI_MAIN_FOLDER"] = app_dir
        env["XUI_LOG_FOLDER"] = log_dir
        env["MOCK_PANEL_PID"] = ""
        env["MOCK_XRAY_RUNNING"] = "0"
        return harness_path, app_dir, env

    def test_24_routeros_lifecycle_container_guides(self):
        """Verifies start/stop/enable/disable/status under XUI_IN_DOCKER=true emit RouterOS guides and never Docker/Compose."""
        with tempfile.TemporaryDirectory() as tmpdir:
            harness_path, _, base_env = self._prepare_xui_harness(tmpdir)
            selector = '[find where comment="[Mikrozia:container] Container"]'

            # 1. start when stopped vs running
            for panel_pid, expect_fragment in (("", "not running"), ("424242", "x-ui restart")):
                env = base_env.copy()
                env["MOCK_PANEL_PID"] = panel_pid
                res = subprocess.run(["bash", harness_path, "start"], env=env, capture_output=True, text=True)
                self.assertEqual(res.returncode, 0, res.stderr)
                self.assertIn(f"/container/start {selector}", res.stdout)
                self.assertIn("cannot be started from inside itself", res.stdout.lower())
                self.assertIn(expect_fragment.lower(), res.stdout.lower())
                self.assertNotIn("docker restart", res.stdout.lower())
                self.assertNotIn("systemctl", res.stdout.lower())
                self.assertNotIn("rc-service", res.stdout.lower())

            # 2. stop when running & stopped: never kills PID 1, warns about connections, shows RouterOS stop
            for panel_pid in ("424242", ""):
                env = base_env.copy()
                env["MOCK_PANEL_PID"] = panel_pid
                res = subprocess.run(["bash", harness_path, "stop"], env=env, capture_output=True, text=True)
                self.assertEqual(res.returncode, 0, res.stderr)
                self.assertIn(f"/container/stop {selector}", res.stdout)
                self.assertIn("terminates all active", res.stdout.lower())
                self.assertNotIn("docker stop", res.stdout.lower())
                self.assertNotIn("systemctl", res.stdout.lower())

            # 3. enable / disable: RouterOS set commands, zero Compose references
            for subcmd, expected_flags in (
                ("enable", ("start-on-boot=yes", "restart-policy=always")),
                ("disable", ("start-on-boot=no", "restart-policy=no")),
            ):
                res = subprocess.run(["bash", harness_path, subcmd], env=base_env, capture_output=True, text=True)
                self.assertEqual(res.returncode, 0, res.stderr)
                self.assertIn(f"/container/set {selector}", res.stdout)
                for flag in expected_flags:
                    self.assertIn(flag, res.stdout)
                self.assertNotIn("docker-compose", res.stdout.lower())
                self.assertNotIn("unless-stopped", res.stdout.lower())

            # 4. status: Managed by RouterOS + /container/print detail, zero Managed by Docker
            env = base_env.copy()
            env["MOCK_PANEL_PID"] = "424242"
            env["MOCK_XRAY_RUNNING"] = "1"
            res = subprocess.run(["bash", harness_path, "status"], env=env, capture_output=True, text=True)
            self.assertEqual(res.returncode, 0, res.stderr)
            self.assertIn("Managed by RouterOS", res.stdout)
            self.assertIn("/container/print detail", res.stdout)
            self.assertNotIn("Managed by Docker", res.stdout)

    def test_25_routeros_lifecycle_restart_and_restart_xray_signals(self):
        """Verifies restart (HUP+USR1) and restart-xray (USR1 only) signaling and non-zero failure paths."""
        import sys
        import time

        with tempfile.TemporaryDirectory() as tmpdir:
            harness_path, _, base_env = self._prepare_xui_harness(tmpdir)
            sig_log = os.path.join(tmpdir, "signals.log")
            selector = '[find where comment="[Mikrozia:container] Container"]'

            # Failure paths when panel is not running must return non-zero and never claim success
            for subcmd in ("restart", "restart-xray"):
                res_fail = subprocess.run(["bash", harness_path, subcmd], env=base_env, capture_output=True, text=True)
                self.assertNotEqual(res_fail.returncode, 0, f"{subcmd} must fail when panel is not running")
                self.assertNotIn("reloaded and Xray restart signal sent", res_fail.stdout)
                self.assertNotIn("Restarted successfully", res_fail.stdout)

            # Spawn a live dummy target process that logs SIGHUP and SIGUSR1 without exiting
            receiver_code = (
                "import signal, sys, time\n"
                "log_path = sys.argv[1]\n"
                "def record(name):\n"
                "    with open(log_path, 'a', encoding='utf-8') as f:\n"
                "        f.write(name + '\\n')\n"
                "signal.signal(signal.SIGHUP, lambda *_: record('HUP'))\n"
                "signal.signal(signal.SIGUSR1, lambda *_: record('USR1'))\n"
                "with open(log_path + '.ready', 'w', encoding='utf-8') as f:\n"
                "    f.write('ready')\n"
                "while True:\n"
                "    time.sleep(0.2)\n"
            )
            proc = subprocess.Popen([sys.executable, "-c", receiver_code, sig_log])
            try:
                ready_file = sig_log + ".ready"
                for _ in range(50):
                    if os.path.exists(ready_file):
                        break
                    time.sleep(0.05)
                self.assertTrue(os.path.exists(ready_file))

                env = base_env.copy()
                env["MOCK_PANEL_PID"] = str(proc.pid)
                env["MOCK_XRAY_RUNNING"] = "1"

                # 1. restart sends HUP then USR1, leaves process alive, clarifies container not restarted
                res_restart = subprocess.run(["bash", harness_path, "restart"], env=env, capture_output=True, text=True)
                self.assertEqual(res_restart.returncode, 0, res_restart.stderr)
                self.assertIsNone(proc.poll(), "restart must not terminate the dummy panel PID")
                self.assertIn("Panel web service reloaded and Xray restart signal sent.", res_restart.stdout)
                self.assertIn("The RouterOS container itself was not restarted.", res_restart.stdout)
                self.assertIn(f"/container/restart {selector}", res_restart.stdout)
                with open(sig_log, "r", encoding="utf-8") as f:
                    received = [line.strip() for line in f if line.strip()]
                self.assertEqual(received, ["HUP", "USR1"])

                # 2. restart-xray sends USR1 only
                with open(sig_log, "w", encoding="utf-8") as f:
                    f.write("")
                res_xray = subprocess.run(["bash", harness_path, "restart-xray"], env=env, capture_output=True, text=True)
                self.assertEqual(res_xray.returncode, 0, res_xray.stderr)
                self.assertIsNone(proc.poll(), "restart-xray must not terminate the dummy panel PID")
                self.assertIn("SIGUSR1", res_xray.stdout)
                self.assertIn("xray state:", res_xray.stdout)
                with open(sig_log, "r", encoding="utf-8") as f:
                    received_xray = [line.strip() for line in f if line.strip()]
                self.assertEqual(received_xray, ["USR1"])
            finally:
                proc.terminate()
                proc.wait(timeout=5)

    def test_26_routeros_lifecycle_menu_usage_and_baremetal_preservation(self):
        """Verifies menu numbering (6-13), CLI help labels, and intact bare-metal branches in x-ui.sh."""
        with open(self.xui_sh_path, "r", encoding="utf-8") as f:
            content = f.read()

        expected_labels = [
            "6.${plain} Show RouterOS Start Command",
            "7.${plain} Show RouterOS Stop Command",
            "8.${plain} Reload Panel & Restart Xray",
            "9.${plain} Restart Xray Only",
            "12.${plain} Show RouterOS Enable Autostart Command",
            "13.${plain} Show RouterOS Disable Autostart Command",
        ]
        for label in expected_labels:
            self.assertIn(label, content)

        self.assertIn("Reload Panel & Restart Xray", content)
        self.assertIn("Show RouterOS Enable Autostart", content)
        self.assertIn("Show RouterOS Disable Autostart", content)

        # Bare-metal service branches outside running_in_docker must remain intact
        self.assertIn("systemctl start x-ui", content)
        self.assertIn("systemctl stop x-ui", content)
        self.assertIn("systemctl restart x-ui", content)
        self.assertIn("systemctl enable x-ui", content)
        self.assertIn("systemctl disable x-ui", content)
        self.assertIn("rc-service x-ui start", content)

    def test_27_routeros_lifecycle_docs_alignment(self):
        """Verifies active RouterOS lifecycle docs align on container selector and x-ui vs /container semantics."""
        selector = '[find where comment="[Mikrozia:container] Container"]'
        files_to_check = [
            os.path.join(self.repo_root, "docs", "content", "docs", "en", "guide", "update-uninstall.mdx"),
            os.path.join(self.repo_root, "docs", "content", "docs", "fa", "guide", "update-uninstall.mdx"),
            os.path.join(self.repo_root, "docs", "content", "docs", "ru", "guide", "update-uninstall.mdx"),
            os.path.join(self.repo_root, "docs", "content", "docs", "zh", "guide", "update-uninstall.mdx"),
            os.path.join(self.repo_root, "docs", "MIKROZIA_CHR_INSTALL_FA.md"),
            os.path.join(self.repo_root, "docs", "DOCKER_HUB_OVERVIEW.md"),
            os.path.join(self.repo_root, "README.md"),
        ]
        for path in files_to_check:
            with open(path, "r", encoding="utf-8") as f:
                doc = f.read()
            self.assertIn(selector, doc, f"{path} must use standard container selector")
            self.assertIn("/container/restart", doc, f"{path} must document /container/restart")
            self.assertNotIn("docker restart", doc.lower(), f"{path} must not contain docker restart")
            self.assertNotIn("docker stop", doc.lower(), f"{path} must not contain docker stop")
            self.assertNotIn("docker-compose.yml", doc.lower(), f"{path} must not promote docker-compose.yml")

    def test_28_host_kernel_bbr_removed_and_menu_renumbered(self):
        """Verifies host-kernel BBR and runtime software controls are absent and the menu is contiguous 0-16."""
        import re

        with open(self.xui_sh_path, "r", encoding="utf-8") as f:
            xui_content = f.read()

        # A. Host Kernel BBR definitions, callers, and sysctl tuning must be completely absent
        for forbidden in (
            "bbr_menu()",
            "enable_bbr()",
            "disable_bbr()",
            "bbr_menu",
            "99-bbr-x-ui.conf",
            "net.core.default_qdisc",
            "net.ipv4.tcp_congestion_control",
        ):
            self.assertNotIn(forbidden, xui_content, f"x-ui.sh must not contain {forbidden!r}")

        # B. Runtime software installation/update controls are absent.
        for forbidden in (
            "Enable BBR",
            "Update Geo Files",
            "Speedtest",
            "Ookla",
            "Cloudflare SSL Certificate",
            "SSL Certificate Management",
            "run_speedtest",
            "ssl_cert_issue",
            "install_acme",
            "acme.sh",
            "get.acme.sh",
            "snap install",
            "packagecloud",
            "apt-get",
            "apk add",
        ):
            self.assertNotIn(forbidden, xui_content, f"x-ui.sh must not contain {forbidden!r}")

        # C. Contiguous 0-16 menu and case dispatch.
        self.assertIn("14.${plain} TLS Certificate Paths", xui_content)
        self.assertIn("15.${plain} RouterOS Network Policy Notice", xui_content)
        self.assertIn("16.${plain} Database Tools", xui_content)
        self.assertIn("Please enter your selection [0-16]:", xui_content)
        self.assertIn('LOGE "Please enter the correct number [0-16]"', xui_content)
        self.assertNotIn("[0-18]", xui_content)
        self.assertNotIn("[0-19]", xui_content)
        self.assertNotIn("[0-20]", xui_content)
        self.assertIsNotNone(re.search(r"16\)\s*\n\s*database_tools_menu", xui_content))
        self.assertIsNone(re.search(r"^\s*17\)", xui_content, re.MULTILINE))
        self.assertIsNone(re.search(r"^\s*18\)", xui_content, re.MULTILINE))
        for kept in range(0, 17):
            self.assertIn(f"{kept}.${{plain}}", xui_content)

        # D. Runtime check of actual show_menu output via harness
        with tempfile.TemporaryDirectory() as tmpdir:
            harness_path, _, base_env = self._prepare_xui_harness(tmpdir)
            base_env["MOCK_PANEL_PID"] = "424242"
            res = subprocess.run(
                ["bash", harness_path],
                input="0\n",
                env=base_env,
                capture_output=True,
                text=True,
            )
            self.assertEqual(res.returncode, 0, res.stderr)
            plain_out = re.sub(r"\x1b\[[0-9;]*m", "", res.stdout + res.stderr)
            self.assertNotIn("Enable BBR", plain_out)
            self.assertNotIn("Update Geo Files", plain_out)
            self.assertNotIn("Speedtest", plain_out)
            self.assertNotIn("Cloudflare", plain_out)
            self.assertIn("14. TLS Certificate Paths", plain_out)
            self.assertIn("16. Database Tools", plain_out)

            # Invalid choice 17 must report [0-16]
            res_invalid = subprocess.run(
                ["bash", harness_path],
                input="17\n",
                env=base_env,
                capture_output=True,
                text=True,
            )
            self.assertEqual(res_invalid.returncode, 0, res_invalid.stderr)
            self.assertIn("Please enter the correct number [0-16]", res_invalid.stdout)

        # E. Multilingual docs alignment (update-uninstall + security + compatibility + charter)
        for lang in ("en", "fa", "ru", "zh"):
            upd_path = os.path.join(self.repo_root, "docs", "content", "docs", lang, "guide", "update-uninstall.mdx")
            with open(upd_path, "r", encoding="utf-8") as f:
                upd_doc = f.read()
            self.assertNotIn("Enable BBR", upd_doc, f"{upd_path} must not list Enable BBR")
            self.assertNotIn("Update Geo Files", upd_doc, f"{upd_path} must not list the removed Geo update menu")
            self.assertNotIn("Speedtest", upd_doc, f"{upd_path} must not list the removed speedtest")
            self.assertNotIn("Cloudflare SSL Certificate", upd_doc, f"{upd_path} must not list the removed Cloudflare issuer")
            self.assertNotIn("0`–`18", upd_doc, f"{upd_path} must use the 0-16 menu range")
            self.assertNotIn("0` تا `18", upd_doc, f"{upd_path} must use the 0-16 menu range")
            self.assertIn("16", upd_doc)
            self.assertIn("TLS Certificate Paths", upd_doc)

            sec_path = os.path.join(self.repo_root, "docs", "content", "docs", lang, "operations", "security.mdx")
            with open(sec_path, "r", encoding="utf-8") as f:
                sec_doc = f.read()
            self.assertNotIn("Enable BBR", sec_doc, f"{sec_path} must not advertise Enable BBR menu")
            self.assertIn("RouterOS", sec_doc)
            self.assertIn("tcpcongestion=bbr", sec_doc)

        compat_path = os.path.join(self.repo_root, "docs", "MIKROZIA_ROUTEROS_COMPATIBILITY_ASSESSMENT.md")
        with open(compat_path, "r", encoding="utf-8") as f:
            compat_doc = f.read()
        self.assertIn("Host Kernel BBR", compat_doc)
        self.assertIn("REMOVED IN PHASE 0", compat_doc)
        self.assertIn("Linux 5.6.3-64 x86_64", compat_doc)
        self.assertIn("tcpcongestion=bbr", compat_doc)

        charter_path = os.path.join(self.repo_root, "docs", "MIKROZIA_PROJECT_CHARTER.md")
        with open(charter_path, "r", encoding="utf-8") as f:
            charter_doc = f.read()
        self.assertIn("Host Kernel BBR", charter_doc)
        self.assertIn("tcpcongestion=bbr", charter_doc)

    def test_29_protocol_level_userspace_bbr_preserved_and_sockopt_bbr_adapted(self):
        """Verifies TUIC/QUIC/FinalMask userspace BBR remain intact while Xray sockopt defaults to unset and omits bbr at runtime."""
        tuic_types_path = os.path.join(self.repo_root, "internal", "tuic", "types.go")
        with open(tuic_types_path, "r", encoding="utf-8") as f:
            self.assertIn('"bbr"', f.read())

        finalmask_path = os.path.join(self.repo_root, "frontend", "src", "schemas", "protocols", "stream", "finalmask.ts")
        with open(finalmask_path, "r", encoding="utf-8") as f:
            finalmask_doc = f.read()
        self.assertIn("QuicCongestionSchema = z.enum(['reno', 'bbr', 'brutal', 'force-brutal'])", finalmask_doc)

        tuic_doc_path = os.path.join(self.repo_root, "docs", "content", "docs", "en", "config", "tuic.mdx")
        with open(tuic_doc_path, "r", encoding="utf-8") as f:
            self.assertIn("congestion-controller: bbr", f.read())

        transports_doc_path = os.path.join(self.repo_root, "docs", "content", "docs", "en", "config", "transports.mdx")
        with open(transports_doc_path, "r", encoding="utf-8") as f:
            self.assertIn("`bbr`", f.read())

        sockopt_path = os.path.join(self.repo_root, "frontend", "src", "schemas", "protocols", "stream", "sockopt.ts")
        with open(sockopt_path, "r", encoding="utf-8") as f:
            sockopt_doc = f.read()
        self.assertIn("z.enum(['bbr', 'cubic', 'reno']).optional()", sockopt_doc)
        self.assertIn("tcpcongestion: TcpCongestionSchema,", sockopt_doc)
        self.assertNotIn(".default('bbr')", sockopt_doc)

        model_path = os.path.join(self.repo_root, "internal", "database", "model", "model.go")
        with open(model_path, "r", encoding="utf-8") as f:
            self.assertIn("StripUnsupportedRouterOSTcpCongestion", f.read())

    def test_30_logs_management_routeros_container_contract(self):
        """Verifies Logs Management uses application logs, Xray access privacy warning, and RouterOS container guidance without syslog/journalctl."""
        xui_sh_path = os.path.join(self.repo_root, "x-ui.sh")
        with open(xui_sh_path, "r", encoding="utf-8") as f:
            xui_sh = f.read()
        alpine_block = xui_sh.split('show_log() {', 1)[1].split('\n    else\n', 1)[0]
        self.assertIn('"${log_folder}/3xui.log"', alpine_block)
        self.assertNotIn('/var/log/x-ui/*.log', alpine_block)
        self.assertIn('/container/set [find where comment="[Mikrozia:container] Container"] logging=yes', alpine_block)
        self.assertIn('/log/print follow-only where topics~"container"', alpine_block)

        console_other_path = os.path.join(self.repo_root, "internal", "logger", "console_other.go")
        with open(console_other_path, "r", encoding="utf-8") as f:
            console_other = f.read()
        self.assertNotIn("NewSyslogBackend", console_other)
        self.assertNotIn("syslog backend disabled", console_other)

        server_go_path = os.path.join(self.repo_root, "internal", "web", "service", "server.go")
        with open(server_go_path, "r", encoding="utf-8") as f:
            server_go = f.read()
        self.assertNotIn("journalctl", server_go)
        self.assertNotIn("syslogTimeout", server_go)

        log_modal_path = os.path.join(self.repo_root, "frontend", "src", "pages", "index", "LogModal.tsx")
        with open(log_modal_path, "r", encoding="utf-8") as f:
            log_modal = f.read()
        self.assertNotIn("SysLog", log_modal)
        self.assertIn("routerosContainerLogsTitle", log_modal)

        xray_log_modal_path = os.path.join(self.repo_root, "frontend", "src", "pages", "index", "XrayLogModal.tsx")
        with open(xray_log_modal_path, "r", encoding="utf-8") as f:
            xray_log_modal = f.read()
        self.assertIn("xrayAccessLogPrivacyNotice", xray_log_modal)

    def test_31_xui_log_runtime_behavior(self):
        """Executes x-ui.sh log (option 1) with a custom XUI_LOG_FOLDER and verifies 3xui.log is read without access.log."""
        with open(self.xui_sh_path, "r", encoding="utf-8") as f:
            xui_src = f.read()

        # Adapt only root/os-release host probes for non-root macOS/Linux test runner.
        xui_src = xui_src.replace(
            '[[ $EUID -ne 0 ]] && LOGE "ERROR: You must be root to run this script! \\n" && exit 1',
            ":",
        )
        os_probe_start = "# Check OS and set release variable"
        os_probe_end = 'running_in_docker="false"'
        before, rest = xui_src.split(os_probe_start, 1)
        _, after = rest.split(os_probe_end, 1)
        patched_xui = before + 'release="alpine"\nos_version=""\n' + os_probe_end + after

        with tempfile.TemporaryDirectory() as tmpdir:
            app_dir = os.path.join(tmpdir, "app")
            log_dir = os.path.join(tmpdir, "custom-logs")
            svc_dir = os.path.join(tmpdir, "systemd")
            os.makedirs(app_dir)
            os.makedirs(log_dir)
            os.makedirs(svc_dir)

            dummy_bin = os.path.join(app_dir, "x-ui")
            with open(dummy_bin, "w", encoding="utf-8") as f:
                f.write("#!/bin/sh\nexit 0\n")
            os.chmod(dummy_bin, 0o755)

            app_marker = "MIKROZIA_APP_LOG_MARKER_31"
            secret_marker = "MIKROZIA_SECRET_ACCESS_MARKER_31"
            with open(os.path.join(log_dir, "3xui.log"), "w", encoding="utf-8") as f:
                f.write(f"2026/11/12 12:00:00 INFO - {app_marker}\n")
            with open(os.path.join(log_dir, "access.log"), "w", encoding="utf-8") as f:
                f.write(f"2026/11/12 12:00:01 from 1.2.3.4 {secret_marker}\n")

            script_path = os.path.join(tmpdir, "x-ui-test.sh")
            with open(script_path, "w", encoding="utf-8") as f:
                f.write(patched_xui)
            os.chmod(script_path, 0o755)

            env = os.environ.copy()
            env["XUI_IN_DOCKER"] = "true"
            env["XUI_MAIN_FOLDER"] = app_dir
            env["XUI_LOG_FOLDER"] = log_dir
            env["XUI_SERVICE"] = svc_dir

            res = subprocess.run(
                ["bash", script_path, "log"],
                input="1\n",
                env=env,
                capture_output=True,
                text=True,
                timeout=15,
            )

            self.assertEqual(res.returncode, 0, f"x-ui log failed: stderr={res.stderr}\nstdout={res.stdout}")
            self.assertIn(app_marker, res.stdout)
            self.assertIn(os.path.join(log_dir, "3xui.log"), res.stdout)
            self.assertNotIn(secret_marker, res.stdout)
            self.assertIn('/container/set [find where comment="[Mikrozia:container] Container"] logging=yes', res.stdout)
            self.assertIn('/log/print follow-only where topics~"container"', res.stdout)

    def test_32_postgresql_routeros_contract_and_immutable_client(self):
        """Verifies pinned Alpine 3.23.8 + postgresql18-client=18.7-r0 in Dockerfile, removal of local PG/pgclient from x-ui.sh, and updated restore error message."""
        with open(self.dockerfile_path, "r", encoding="utf-8") as f:
            dockerfile = f.read()

        self.assertIn(
            "FROM alpine:3.23.8@sha256:469c9ea410b9215eaee7976ba6c0639039b444be8e5d638f7abbada189971eb2",
            dockerfile,
            "Final Dockerfile stage must pin alpine:3.23.8 to its official multi-arch SHA-256 digest",
        )
        self.assertNotRegex(dockerfile, r"(?m)^FROM\s+alpine(?::latest)?\s*$")
        final_stage = dockerfile.split("FROM alpine:3.23.8@", 1)[-1]
        self.assertIn("postgresql18-client=18.7-r0", final_stage)
        self.assertNotIn("apk add --no-cache --update", final_stage)
        self.assertIn("apk add --no-cache", final_stage)
        self.assertNotIn("postgresql18-contrib", final_stage)
        self.assertNotRegex(final_stage, r"\bpostgresql(?:18)?(?:\s|\\|$)")

        with open(self.xui_sh_path, "r", encoding="utf-8") as f:
            xui_sh = f.read()

        forbidden_xui_tokens = [
            "pg_install_local",
            "pg_upgrade_client",
            "pg_ensure_client",
            "postgresql_menu",
            "migrate_to_postgres",
            "purge_postgresql",
            "postgresql_status",
            "postgresql_start",
            "postgresql_stop",
            "postgresql_restart",
            "postgresql_enable",
            "postgresql_log",
            "pg_lsclusters",
            "pg_ctlcluster",
            "pg_write_env",
            "x-ui pgclient",
            '"pgclient")',
            "PostgreSQL Management",
        ]
        for token in forbidden_xui_tokens:
            self.assertNotIn(token, xui_sh, f"x-ui.sh must not retain legacy local PG/pgclient token: {token}")

        self.assertIn("Database Tools", xui_sh)
        self.assertIn("database_tools_menu", xui_sh)
        self.assertIn("XUI_DB_TYPE=postgres", xui_sh)
        self.assertIn("/container/envs", xui_sh)

        server_go_path = os.path.join(self.repo_root, "internal", "web", "service", "server.go")
        with open(server_go_path, "r", encoding="utf-8") as f:
            server_go = f.read()
        self.assertNotIn("x-ui pgclient", server_go)
        self.assertNotIn("upgrade the postgresql-client package", server_go)

        compose_path = os.path.join(self.repo_root, "docker-compose.yml")
        with open(compose_path, "r", encoding="utf-8") as f:
            compose_yml = f.read()
        self.assertIn("Local Development", compose_yml)

    def test_compose_has_no_fail2ban_assumptions(self):
        """The dev/CI Compose file must not promise Fail2ban or grant its capabilities."""
        compose_path = os.path.join(self.repo_root, "docker-compose.yml")
        with open(compose_path, "r", encoding="utf-8") as f:
            compose_yml = f.read()

        lowered = compose_yml.lower()
        self.assertNotIn("fail2ban", lowered)
        self.assertNotIn("xui_enable_fail2ban", lowered)
        self.assertNotIn("net_admin", lowered)
        self.assertNotIn("net_raw", lowered)
        self.assertNotIn("cap_add", lowered)

    def _prepare_patched_xui_script(self, tmpdir):
        with open(self.xui_sh_path, "r", encoding="utf-8") as f:
            xui_src = f.read()

        xui_src = xui_src.replace(
            '[[ $EUID -ne 0 ]] && LOGE "ERROR: You must be root to run this script! \\n" && exit 1',
            ":",
        )
        os_probe_start = "# Check OS and set release variable"
        os_probe_end = 'running_in_docker="false"'
        before, rest = xui_src.split(os_probe_start, 1)
        _, after = rest.split(os_probe_end, 1)
        patched_xui = before + 'release="alpine"\nos_version=""\n' + os_probe_end + after

        script_path = os.path.join(tmpdir, "x-ui-test.sh")
        with open(script_path, "w", encoding="utf-8") as f:
            f.write(patched_xui)
        os.chmod(script_path, 0o755)
        return script_path

    def test_33_xui_settings_never_leaks_dsn_or_secrets(self):
        """Executes x-ui.sh settings with URI and keyword/value DSNs containing complex passwords and proves zero secret/host/DSN leakage."""
        with tempfile.TemporaryDirectory() as tmpdir:
            app_dir = os.path.join(tmpdir, "app")
            log_dir = os.path.join(tmpdir, "logs")
            svc_dir = os.path.join(tmpdir, "systemd")
            os.makedirs(app_dir)
            os.makedirs(log_dir)
            os.makedirs(svc_dir)

            dummy_bin = os.path.join(app_dir, "x-ui")
            with open(dummy_bin, "w", encoding="utf-8") as f:
                f.write("#!/bin/sh\necho 'webBasePath: /panel'\necho 'port: 2053'\necho 'cert: '\nexit 0\n")
            os.chmod(dummy_bin, 0o755)

            bin_dir = os.path.join(tmpdir, "bin")
            os.makedirs(bin_dir)
            dummy_curl = os.path.join(bin_dir, "curl")
            with open(dummy_curl, "w", encoding="utf-8") as f:
                f.write("#!/bin/sh\nprintf '203.0.113.10\\n200\\n'\nexit 0\n")
            os.chmod(dummy_curl, 0o755)

            script_path = self._prepare_patched_xui_script(tmpdir)

            cases = [
                (
                    "postgres://pguser99:super-secret@value:123@db.internal.example:5432/mikrodb?sslmode=verify-full",
                    ["pguser99", "super-secret@value:123", "super-secret", "db.internal.example", "mikrodb", "verify-full"],
                    "Database: External PostgreSQL (configured through RouterOS container environment)",
                ),
                (
                    "host=pg-host.internal port=5432 user=kwuser password='super secret value' dbname=kwdb sslmode=require",
                    ["pg-host.internal", "kwuser", "super secret value", "kwdb", "sslmode=require"],
                    "Database: External PostgreSQL (configured through RouterOS container environment)",
                ),
                (
                    "",
                    [],
                    "Database: External PostgreSQL (XUI_DB_DSN is not configured)",
                ),
            ]

            for dsn, forbidden_fragments, expected_line in cases:
                env = os.environ.copy()
                env["PATH"] = f"{bin_dir}:{env.get('PATH', '')}"
                env["XUI_IN_DOCKER"] = "true"
                env["XUI_MAIN_FOLDER"] = app_dir
                env["XUI_LOG_FOLDER"] = log_dir
                env["XUI_SERVICE"] = svc_dir
                env["XUI_DB_TYPE"] = "postgres"
                if dsn:
                    env["XUI_DB_DSN"] = dsn
                else:
                    env.pop("XUI_DB_DSN", None)

                res = subprocess.run(
                    ["bash", script_path, "settings"],
                    input="203.0.113.10\nn\n",
                    env=env,
                    capture_output=True,
                    text=True,
                    timeout=15,
                )
                self.assertEqual(res.returncode, 0, f"settings failed: {res.stderr}")
                combined = res.stdout + "\n" + res.stderr
                self.assertIn(expected_line, combined)
                for fragment in forbidden_fragments:
                    self.assertNotIn(fragment, combined, "Sensitive DSN fragment leaked in shell output")

    def test_34_xui_migrate_db_delegates_args_verbatim(self):
        """Executes x-ui.sh migrate-db and verifies all arguments (--src, --dsn, etc.) are forwarded verbatim to the Go binary."""
        with tempfile.TemporaryDirectory() as tmpdir:
            app_dir = os.path.join(tmpdir, "app")
            log_dir = os.path.join(tmpdir, "logs")
            svc_dir = os.path.join(tmpdir, "systemd")
            os.makedirs(app_dir)
            os.makedirs(log_dir)
            os.makedirs(svc_dir)

            args_file = os.path.join(tmpdir, "recorded_args.txt")
            dummy_bin = os.path.join(app_dir, "x-ui")
            with open(dummy_bin, "w", encoding="utf-8") as f:
                f.write(
                    f"#!/bin/sh\n"
                    f"printf '%s\\n' \"$@\" > {args_file!r}\n"
                    f"exit 0\n"
                )
            os.chmod(dummy_bin, 0o755)

            script_path = self._prepare_patched_xui_script(tmpdir)
            sample_src = os.path.join(tmpdir, "snapshot.db")
            sample_dsn = "postgres://u:super-secret@value:123@192.0.2.10:5432/xui?sslmode=require"

            env = os.environ.copy()
            env["XUI_IN_DOCKER"] = "true"
            env["XUI_MAIN_FOLDER"] = app_dir
            env["XUI_LOG_FOLDER"] = log_dir
            env["XUI_SERVICE"] = svc_dir

            res = subprocess.run(
                ["bash", script_path, "migrate-db", "--src", sample_src, "--dsn", sample_dsn],
                env=env,
                capture_output=True,
                text=True,
                timeout=15,
            )
            self.assertEqual(res.returncode, 0, f"migrate-db delegation failed: {res.stderr}")
            self.assertTrue(os.path.exists(args_file), "Dummy Go binary was not invoked by x-ui.sh migrate-db")
            with open(args_file, "r", encoding="utf-8") as f:
                recorded = [line.rstrip("\n") for line in f]
            self.assertEqual(recorded, ["migrate-db", "--src", sample_src, "--dsn", sample_dsn])


    def _prepare_migrate_db_shell(self, tmpdir, content, exit_code):
        app_dir = os.path.join(tmpdir, "app")
        log_dir = os.path.join(tmpdir, "logs")
        svc_dir = os.path.join(tmpdir, "systemd")
        os.makedirs(app_dir)
        os.makedirs(log_dir)
        os.makedirs(svc_dir)

        dummy_bin = os.path.join(app_dir, "x-ui")
        with open(dummy_bin, "w", encoding="utf-8") as f:
            f.write(
                "#!/bin/sh\n"
                'if [ "$1" = "migrate-db" ] && [ "$2" = "-h" ]; then printf -- "-dump\\n"; exit 0; fi\n'
                'if [ "$1" = "migrate-db" ]; then printf "%s" "$MIGRATE_CONTENT" > "$5"; exit "$MIGRATE_EXIT"; fi\n'
                "exit 2\n"
            )
        os.chmod(dummy_bin, 0o755)

        env = os.environ.copy()
        env["XUI_IN_DOCKER"] = "true"
        env["XUI_MAIN_FOLDER"] = app_dir
        env["XUI_LOG_FOLDER"] = log_dir
        env["XUI_SERVICE"] = svc_dir
        env["MIGRATE_CONTENT"] = content
        env["MIGRATE_EXIT"] = str(exit_code)
        return self._prepare_patched_xui_script(tmpdir), env

    def test_35_xui_migrate_db_restore_failure_preserves_existing_output(self):
        """A failed restore never deletes or partially overwrites the selected output database."""
        with tempfile.TemporaryDirectory() as tmpdir:
            script_path, env = self._prepare_migrate_db_shell(tmpdir, "partial-restore", 1)
            input_path = os.path.join(tmpdir, "backup.dump")
            output_path = os.path.join(tmpdir, "target.db")
            with open(input_path, "w", encoding="utf-8") as f:
                f.write("PRAGMA foreign_keys=OFF;\n")
            with open(output_path, "w", encoding="utf-8") as f:
                f.write("original-database")
            for sidecar in ("-wal", "-shm", "-journal"):
                with open(output_path + sidecar, "w", encoding="utf-8") as f:
                    f.write("original-sidecar")

            res = subprocess.run(
                ["bash", script_path, "migrateDB", input_path, output_path],
                input="y\n",
                env=env,
                capture_output=True,
                text=True,
                timeout=15,
            )
            self.assertNotEqual(res.returncode, 0, "failed restore must not report success")
            with open(output_path, "r", encoding="utf-8") as f:
                self.assertEqual(f.read(), "original-database")
            for sidecar in ("-wal", "-shm", "-journal"):
                self.assertTrue(os.path.exists(output_path + sidecar), f"failed restore removed {sidecar}")
            self.assertIn("was not changed", res.stdout + res.stderr)
            self.assertFalse(
                any(name.startswith("target.db.mikrozia-tmp.") for name in os.listdir(tmpdir)),
                "failed restore left a temporary directory behind",
            )

    def test_36_xui_migrate_db_restore_replaces_output_only_after_success(self):
        """A successful restore atomically replaces the output and clears stale SQLite sidecars."""
        with tempfile.TemporaryDirectory() as tmpdir:
            script_path, env = self._prepare_migrate_db_shell(tmpdir, "restored-database", 0)
            input_path = os.path.join(tmpdir, "backup.dump")
            output_path = os.path.join(tmpdir, "target.db")
            with open(input_path, "w", encoding="utf-8") as f:
                f.write("PRAGMA foreign_keys=OFF;\n")
            with open(output_path, "w", encoding="utf-8") as f:
                f.write("original-database")
            for sidecar in ("-wal", "-shm", "-journal"):
                with open(output_path + sidecar, "w", encoding="utf-8") as f:
                    f.write("stale-sidecar")

            res = subprocess.run(
                ["bash", script_path, "migrateDB", input_path, output_path],
                input="y\n",
                env=env,
                capture_output=True,
                text=True,
                timeout=15,
            )
            self.assertEqual(res.returncode, 0, f"restore failed: {res.stderr}")
            with open(output_path, "r", encoding="utf-8") as f:
                self.assertEqual(f.read(), "restored-database")
            for sidecar in ("-wal", "-shm", "-journal"):
                self.assertFalse(os.path.exists(output_path + sidecar), f"stale {sidecar} survived replacement")
            self.assertFalse(
                any(name.startswith("target.db.mikrozia-tmp.") for name in os.listdir(tmpdir)),
                "successful restore left a temporary directory behind",
            )

    def test_37_xui_migrate_db_dump_is_atomic_and_preserves_old_output_on_failure(self):
        """Dump overwrite is atomic; a failed dump leaves the previous file intact."""
        with tempfile.TemporaryDirectory() as tmpdir:
            script_path, env = self._prepare_migrate_db_shell(tmpdir, "partial-dump", 1)
            input_path = os.path.join(tmpdir, "live.db")
            output_path = os.path.join(tmpdir, "live.dump")
            with open(input_path, "wb") as f:
                f.write(b"SQLite format 3\x00")
            with open(output_path, "w", encoding="utf-8") as f:
                f.write("previous-dump")

            res = subprocess.run(
                ["bash", script_path, "migrateDB", input_path, output_path],
                input="y\n",
                env=env,
                capture_output=True,
                text=True,
                timeout=15,
            )
            self.assertNotEqual(res.returncode, 0, "failed dump must not report success")
            with open(output_path, "r", encoding="utf-8") as f:
                self.assertEqual(f.read(), "previous-dump")
            self.assertIn("was not changed", res.stdout + res.stderr)
            self.assertFalse(
                any(name.startswith("live.dump.mikrozia-tmp.") for name in os.listdir(tmpdir)),
                "failed dump left a temporary directory behind",
            )

            env["MIGRATE_CONTENT"] = "fresh-dump"
            env["MIGRATE_EXIT"] = "0"
            res = subprocess.run(
                ["bash", script_path, "migrateDB", input_path, output_path],
                input="y\n",
                env=env,
                capture_output=True,
                text=True,
                timeout=15,
            )
            self.assertEqual(res.returncode, 0, f"dump failed: {res.stderr}")
            with open(output_path, "r", encoding="utf-8") as f:
                self.assertEqual(f.read(), "fresh-dump")
            self.assertFalse(
                any(name.startswith("live.dump.mikrozia-tmp.") for name in os.listdir(tmpdir)),
                "successful dump left a temporary directory behind",
            )


if __name__ == "__main__":
    unittest.main()
