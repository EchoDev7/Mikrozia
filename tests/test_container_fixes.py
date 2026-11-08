#!/usr/bin/env python3
"""
Unit and Integration Tests for Mikrozia Container Alpine Fixes & ACME Persistence.

Validates:
- FIX 1: Shell scripts syntax validation (DockerEntrypoint.sh, x-ui.sh).
- FIX 2: Dockerfile Alpine hardening (HOME=/root, no firewall tooling, coreutils, socat).
- FIX 3: Alpine management script bug fixes (ls relative path, log check).
- FIX 4: Fresh install persistent ACME initialization via actual DockerEntrypoint.sh.
- FIX 5: Non-destructive migration of existing legacy ACME directories with preservation.
- FIX 6: Clean recovery from broken or dead symlinks.
- FIX 7: Safe detection of conflicting ACME files with fail-safe abort (no data loss).
- FIX 8: Idempotent repeated entrypoint execution.
- FIX 9: Seamless recovery from interrupted migrations.
- FIX 10: Strict preservation of hidden files, nested hierarchies, and permissions.
"""

import os
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

    def _build_test_env(self, tmpdir):
        """Constructs a clean test environment pointing DockerEntrypoint.sh paths to tmpdir."""
        root_dir = os.path.join(tmpdir, "root")
        cert_dir = os.path.join(root_dir, "cert")
        persist_dir = os.path.join(cert_dir, ".acme.sh")
        root_acme = os.path.join(root_dir, ".acme.sh")
        slash_acme = os.path.join(tmpdir, "slash_acme", ".acme.sh")

        env = os.environ.copy()
        env["HOME"] = root_dir
        env["ACME_CERT_DIR"] = cert_dir
        env["ACME_PERSIST_DIR"] = persist_dir
        env["ACME_ROOT_DIR"] = root_acme
        env["ACME_SLASH_DIR"] = slash_acme
        env["ACME_MIGRATE_ONLY"] = "true"

        return env, root_dir, cert_dir, persist_dir, root_acme, slash_acme

    def test_01_shell_scripts_syntax(self):
        """Validates that DockerEntrypoint.sh and x-ui.sh have valid shell syntax."""
        p1 = subprocess.run(["sh", "-n", self.entrypoint_path], capture_output=True, text=True)
        self.assertEqual(p1.returncode, 0, f"DockerEntrypoint.sh syntax error: {p1.stderr}")

        p2 = subprocess.run(["bash", "-n", self.xui_sh_path], capture_output=True, text=True)
        self.assertEqual(p2.returncode, 0, f"x-ui.sh syntax error: {p2.stderr}")

    def test_02_dockerfile_alpine_hardening(self):
        """Verifies Dockerfile hardening and the absence of firewall tooling."""
        with open(self.dockerfile_path, "r", encoding="utf-8") as f:
            content = f.read()

        self.assertIn("ENV HOME=/root", content)
        self.assertIn("coreutils", content)
        self.assertIn("socat", content)
        self.assertIn("ca-certificates", content)
        self.assertIn("openssl", content)
        self.assertNotIn("fail2ban", content.lower())
        self.assertNotIn("iptables", content.lower())
        self.assertNotIn("nftables", content.lower())
        self.assertNotIn("ufw", content.lower())

    def test_03_xui_script_bug_fixes(self):
        """Verifies x-ui.sh relative path bug and /var/log/messages check are fixed."""
        with open(self.xui_sh_path, "r", encoding="utf-8") as f:
            content = f.read()

        # Relative 'cert/*' bug must not exist in auto-renew info
        self.assertNotIn("ls -lah cert/*", content)
        self.assertIn("ls -lah ${certPath}/*", content)

        # /var/log/messages must be guarded by existence check
        self.assertIn("if [[ -f /var/log/messages ]]; then", content)

    def test_04_acme_migration_fresh_install(self):
        """Tests that a fresh install executes DockerEntrypoint.sh and sets up persistent storage."""
        with tempfile.TemporaryDirectory() as tmpdir:
            env, root_dir, cert_dir, persist_dir, root_acme, slash_acme = self._build_test_env(tmpdir)
            os.makedirs(cert_dir)
            os.makedirs(os.path.dirname(slash_acme))

            # Run the ACTUAL DockerEntrypoint.sh
            res = subprocess.run(
                ["sh", self.entrypoint_path, "--migrate-acme-only"],
                env=env,
                capture_output=True,
                text=True
            )
            self.assertEqual(res.returncode, 0, f"Entrypoint failed on fresh install: {res.stderr}")

            # Verify persistent destination was created and symlinks point to it
            self.assertTrue(os.path.isdir(persist_dir), "Persistent destination directory must exist")
            self.assertTrue(os.path.islink(root_acme), "root_acme must be a symlink")
            self.assertTrue(os.path.islink(slash_acme), "slash_acme must be a symlink")
            self.assertEqual(os.path.realpath(root_acme), os.path.realpath(persist_dir))
            self.assertEqual(os.path.realpath(slash_acme), os.path.realpath(persist_dir))

    def test_05_acme_migration_existing_legacy_root(self):
        """Tests that legacy certificates and account keys are migrated non-destructively and preserved."""
        with tempfile.TemporaryDirectory() as tmpdir:
            env, root_dir, cert_dir, persist_dir, root_acme, slash_acme = self._build_test_env(tmpdir)

            os.makedirs(cert_dir)
            os.makedirs(root_acme)
            os.makedirs(os.path.dirname(slash_acme))

            # Create existing legacy account config and cert files in root_acme
            account_conf = os.path.join(root_acme, "account.conf")
            with open(account_conf, "w") as f:
                f.write("ACCOUNT_KEY=secret_key_12345")

            domain_dir = os.path.join(root_acme, "example.com_ecc")
            os.makedirs(domain_dir)
            with open(os.path.join(domain_dir, "fullchain.cer"), "w") as f:
                f.write("CERT_DATA_ECC")
            with open(os.path.join(domain_dir, "example.com.key"), "w") as f:
                f.write("KEY_DATA_ECC")

            # Run the ACTUAL DockerEntrypoint.sh
            res = subprocess.run(
                ["sh", self.entrypoint_path, "--migrate-acme-only"],
                env=env,
                capture_output=True,
                text=True
            )
            self.assertEqual(res.returncode, 0, f"Entrypoint failed on legacy migration: {res.stderr}")

            # Verify files are present and identical in persistent destination
            dst_account = os.path.join(persist_dir, "account.conf")
            dst_cert = os.path.join(persist_dir, "example.com_ecc", "fullchain.cer")
            dst_key = os.path.join(persist_dir, "example.com_ecc", "example.com.key")

            self.assertTrue(os.path.isfile(dst_account), "account.conf must exist in destination")
            self.assertTrue(os.path.isfile(dst_cert), "fullchain.cer must exist in destination")
            self.assertTrue(os.path.isfile(dst_key), "key must exist in destination")

            with open(dst_account) as f:
                self.assertEqual(f.read(), "ACCOUNT_KEY=secret_key_12345")
            with open(dst_cert) as f:
                self.assertEqual(f.read(), "CERT_DATA_ECC")
            with open(dst_key) as f:
                self.assertEqual(f.read(), "KEY_DATA_ECC")

            # Verify legacy source was preserved (NOT deleted)
            migrated_backup = root_acme + ".migrated"
            self.assertTrue(os.path.isdir(migrated_backup), "Legacy source directory must be preserved in .migrated backup")
            self.assertTrue(os.path.isfile(os.path.join(migrated_backup, "account.conf")), "Original files must remain in preserved backup")

            # Verify symlinks point to persistent destination
            self.assertTrue(os.path.islink(root_acme))
            self.assertEqual(os.path.realpath(root_acme), os.path.realpath(persist_dir))
            # Test transparency via symlink
            with open(os.path.join(root_acme, "account.conf")) as f:
                self.assertEqual(f.read(), "ACCOUNT_KEY=secret_key_12345")

    def test_06_acme_migration_broken_symlink_recovery(self):
        """Tests that broken or dead symlinks are cleanly replaced without crashing."""
        with tempfile.TemporaryDirectory() as tmpdir:
            env, root_dir, cert_dir, persist_dir, root_acme, slash_acme = self._build_test_env(tmpdir)

            os.makedirs(cert_dir, exist_ok=True)
            os.makedirs(root_dir, exist_ok=True)
            os.makedirs(os.path.dirname(slash_acme), exist_ok=True)

            # Create broken symlinks pointing to dead paths
            os.symlink("/nonexistent/dead/target1", root_acme)
            os.symlink("/nonexistent/dead/target2", slash_acme)

            # Run the ACTUAL DockerEntrypoint.sh
            res = subprocess.run(
                ["sh", self.entrypoint_path, "--migrate-acme-only"],
                env=env,
                capture_output=True,
                text=True
            )
            self.assertEqual(res.returncode, 0, f"Entrypoint failed on broken symlink recovery: {res.stderr}")

            # Verify symlinks are now valid and point to persistent destination
            self.assertTrue(os.path.islink(root_acme))
            self.assertTrue(os.path.islink(slash_acme))
            self.assertEqual(os.path.realpath(root_acme), os.path.realpath(persist_dir))
            self.assertEqual(os.path.realpath(slash_acme), os.path.realpath(persist_dir))

    def test_07_acme_migration_conflicting_files_fail_safe(self):
        """Tests that conflicting ACME files cause safe abort without deleting or corrupting any data."""
        with tempfile.TemporaryDirectory() as tmpdir:
            env, root_dir, cert_dir, persist_dir, root_acme, slash_acme = self._build_test_env(tmpdir)

            os.makedirs(persist_dir)
            os.makedirs(root_acme)
            os.makedirs(os.path.dirname(slash_acme))

            # Create conflicting file in destination and source
            src_file = os.path.join(root_acme, "account.conf")
            dst_file = os.path.join(persist_dir, "account.conf")
            with open(src_file, "w") as f:
                f.write("SOURCE_KEY_ORIGINAL")
            with open(dst_file, "w") as f:
                f.write("DESTINATION_KEY_DIFFERENT")

            # Run the ACTUAL DockerEntrypoint.sh
            res = subprocess.run(
                ["sh", self.entrypoint_path, "--migrate-acme-only"],
                env=env,
                capture_output=True,
                text=True
            )

            # Must fail with non-zero exit code due to conflict
            self.assertNotEqual(res.returncode, 0, "Entrypoint must fail closed when conflicting files exist")
            self.assertIn("Conflict", res.stderr + res.stdout)

            # Verify neither source nor destination data was modified or deleted
            self.assertTrue(os.path.isdir(root_acme), "Source directory must not be deleted or renamed on conflict")
            with open(src_file) as f:
                self.assertEqual(f.read(), "SOURCE_KEY_ORIGINAL", "Source file content must be preserved unchanged")
            with open(dst_file) as f:
                self.assertEqual(f.read(), "DESTINATION_KEY_DIFFERENT", "Destination file content must be preserved unchanged")

    def test_08_acme_migration_idempotent_repeated_runs(self):
        """Tests that repeated entrypoint execution is completely idempotent."""
        with tempfile.TemporaryDirectory() as tmpdir:
            env, root_dir, cert_dir, persist_dir, root_acme, slash_acme = self._build_test_env(tmpdir)

            os.makedirs(cert_dir)
            os.makedirs(root_acme)
            os.makedirs(os.path.dirname(slash_acme))

            with open(os.path.join(root_acme, "account.conf"), "w") as f:
                f.write("ACCOUNT_KEY=idempotent_test")

            # Run 1
            res1 = subprocess.run(
                ["sh", self.entrypoint_path, "--migrate-acme-only"],
                env=env,
                capture_output=True,
                text=True
            )
            self.assertEqual(res1.returncode, 0, f"Run 1 failed: {res1.stderr}")

            # Run 2
            res2 = subprocess.run(
                ["sh", self.entrypoint_path, "--migrate-acme-only"],
                env=env,
                capture_output=True,
                text=True
            )
            self.assertEqual(res2.returncode, 0, f"Run 2 failed: {res2.stderr}")

            # Run 3
            res3 = subprocess.run(
                ["sh", self.entrypoint_path, "--migrate-acme-only"],
                env=env,
                capture_output=True,
                text=True
            )
            self.assertEqual(res3.returncode, 0, f"Run 3 failed: {res3.stderr}")

            # Destination must be preserved and symlinks intact
            self.assertTrue(os.path.islink(root_acme))
            self.assertEqual(os.path.realpath(root_acme), os.path.realpath(persist_dir))
            with open(os.path.join(persist_dir, "account.conf")) as f:
                self.assertEqual(f.read(), "ACCOUNT_KEY=idempotent_test")

    def test_09_acme_migration_interrupted_run_recovery(self):
        """Tests that an interrupted migration with matching partial files resumes and completes cleanly."""
        with tempfile.TemporaryDirectory() as tmpdir:
            env, root_dir, cert_dir, persist_dir, root_acme, slash_acme = self._build_test_env(tmpdir)

            os.makedirs(persist_dir)
            os.makedirs(root_acme)
            os.makedirs(os.path.dirname(slash_acme))

            # Simulate an interrupted run: part of the source was already copied to destination
            with open(os.path.join(persist_dir, "existing_file.txt"), "w") as f:
                f.write("ALREADY_COPIED_MATCHING_DATA")
            with open(os.path.join(root_acme, "existing_file.txt"), "w") as f:
                f.write("ALREADY_COPIED_MATCHING_DATA")

            # And a remaining file yet to be copied
            with open(os.path.join(root_acme, "remaining_file.txt"), "w") as f:
                f.write("REMAINING_FILE_DATA")

            res = subprocess.run(
                ["sh", self.entrypoint_path, "--migrate-acme-only"],
                env=env,
                capture_output=True,
                text=True
            )
            self.assertEqual(res.returncode, 0, f"Interrupted run recovery failed: {res.stderr}")

            # Both files must exist in destination
            self.assertTrue(os.path.isfile(os.path.join(persist_dir, "existing_file.txt")))
            self.assertTrue(os.path.isfile(os.path.join(persist_dir, "remaining_file.txt")))
            with open(os.path.join(persist_dir, "remaining_file.txt")) as f:
                self.assertEqual(f.read(), "REMAINING_FILE_DATA")

            # Symlink created and legacy preserved
            self.assertTrue(os.path.islink(root_acme))
            self.assertTrue(os.path.isdir(root_acme + ".migrated"))

    def test_10_acme_migration_preserves_hidden_files_and_modes(self):
        """Tests that dotfiles, spaces in filenames, and restrictive file permissions are preserved."""
        with tempfile.TemporaryDirectory() as tmpdir:
            env, root_dir, cert_dir, persist_dir, root_acme, slash_acme = self._build_test_env(tmpdir)

            os.makedirs(cert_dir)
            os.makedirs(root_acme)
            os.makedirs(os.path.dirname(slash_acme))

            # Hidden config file
            dotfile = os.path.join(root_acme, ".account.conf")
            with open(dotfile, "w") as f:
                f.write("DOTFILE_CONFIG")

            # File with spaces
            space_file = os.path.join(root_acme, "my test cert.cer")
            with open(space_file, "w") as f:
                f.write("CERT_WITH_SPACES")

            # Restrictive 0600 file inside hidden directory
            hidden_sub = os.path.join(root_acme, ".secret_keys")
            os.makedirs(hidden_sub)
            key_file = os.path.join(hidden_sub, "ca.key")
            with open(key_file, "w") as f:
                f.write("CA_PRIVATE_KEY")
            os.chmod(key_file, 0o600)

            # Run entrypoint
            res = subprocess.run(
                ["sh", self.entrypoint_path, "--migrate-acme-only"],
                env=env,
                capture_output=True,
                text=True
            )
            self.assertEqual(res.returncode, 0, f"Entrypoint failed on dotfiles/permissions: {res.stderr}")

            # Verify in destination
            dst_dotfile = os.path.join(persist_dir, ".account.conf")
            dst_space = os.path.join(persist_dir, "my test cert.cer")
            dst_key = os.path.join(persist_dir, ".secret_keys", "ca.key")

            self.assertTrue(os.path.isfile(dst_dotfile), "Hidden dotfile must be copied")
            self.assertTrue(os.path.isfile(dst_space), "File with spaces must be copied")
            self.assertTrue(os.path.isfile(dst_key), "Key inside hidden subdir must be copied")

            with open(dst_dotfile) as f:
                self.assertEqual(f.read(), "DOTFILE_CONFIG")
            with open(dst_space) as f:
                self.assertEqual(f.read(), "CERT_WITH_SPACES")
            with open(dst_key) as f:
                self.assertEqual(f.read(), "CA_PRIVATE_KEY")

            # Verify permission bits (0600)
            key_mode = oct(os.stat(dst_key).st_mode & 0o777)
            self.assertEqual(key_mode, "0o600", "File permissions (0600) must be preserved")

    def test_11_acme_symlink_points_to_populated_legacy_directory(self):
        """Tests that an existing symlink pointing to a populated legacy directory is migrated safely."""
        with tempfile.TemporaryDirectory() as tmpdir:
            env, root_dir, cert_dir, persist_dir, root_acme, slash_acme = self._build_test_env(tmpdir)

            os.makedirs(cert_dir)
            os.makedirs(os.path.dirname(slash_acme), exist_ok=True)

            # Create an external populated legacy directory
            external_legacy = os.path.join(tmpdir, "external_custom_acme")
            os.makedirs(external_legacy)
            with open(os.path.join(external_legacy, "account.conf"), "w") as f:
                f.write("EXTERNAL_LEGACY_KEY=98765")
            domain_dir = os.path.join(external_legacy, "vpn.example.com")
            os.makedirs(domain_dir)
            with open(os.path.join(domain_dir, "fullchain.cer"), "w") as f:
                f.write("EXTERNAL_CERT_PAYLOAD")

            # root_acme starts as a symlink pointing to external_legacy
            os.symlink(external_legacy, root_acme)

            # Run production entrypoint
            res = subprocess.run(
                ["sh", self.entrypoint_path, "--migrate-acme-only"],
                env=env,
                capture_output=True,
                text=True
            )
            self.assertEqual(res.returncode, 0, f"Entrypoint failed on populated symlink migration: {res.stderr}")

            # Verify files are in persistent destination
            self.assertTrue(os.path.isfile(os.path.join(persist_dir, "account.conf")))
            self.assertTrue(os.path.isfile(os.path.join(persist_dir, "vpn.example.com", "fullchain.cer")))
            with open(os.path.join(persist_dir, "account.conf")) as f:
                self.assertEqual(f.read(), "EXTERNAL_LEGACY_KEY=98765")

            # Verify original legacy directory was preserved in .migrated
            self.assertTrue(os.path.isdir(external_legacy + ".migrated"))
            with open(os.path.join(external_legacy + ".migrated", "account.conf")) as f:
                self.assertEqual(f.read(), "EXTERNAL_LEGACY_KEY=98765")

            # Verify root_acme symlink now points to persistent destination
            self.assertTrue(os.path.islink(root_acme))
            self.assertEqual(os.path.realpath(root_acme), os.path.realpath(persist_dir))
            # Transparent access via updated symlink
            with open(os.path.join(root_acme, "account.conf")) as f:
                self.assertEqual(f.read(), "EXTERNAL_LEGACY_KEY=98765")

    def test_12_acme_symlink_populated_conflict_preserves_link_and_data(self):
        """Tests that conflicts in a populated symlink target abort without detaching the symlink or corrupting data."""
        with tempfile.TemporaryDirectory() as tmpdir:
            env, root_dir, cert_dir, persist_dir, root_acme, slash_acme = self._build_test_env(tmpdir)

            os.makedirs(persist_dir)
            os.makedirs(os.path.dirname(slash_acme), exist_ok=True)

            external_legacy = os.path.join(tmpdir, "external_conflict_acme")
            os.makedirs(external_legacy)

            # Conflicting files in external legacy and destination
            with open(os.path.join(external_legacy, "account.conf"), "w") as f:
                f.write("LEGACY_CONFLICT_KEY")
            with open(os.path.join(persist_dir, "account.conf"), "w") as f:
                f.write("DESTINATION_CONFLICT_KEY")

            os.symlink(external_legacy, root_acme)

            res = subprocess.run(
                ["sh", self.entrypoint_path, "--migrate-acme-only"],
                env=env,
                capture_output=True,
                text=True
            )

            # Must fail safely
            self.assertNotEqual(res.returncode, 0, "Entrypoint must fail closed on conflict in symlink target")
            self.assertIn("Conflict", res.stderr + res.stdout)

            # Crucial: symlink must NEVER be silently detached or deleted
            self.assertTrue(os.path.islink(root_acme), "Symlink must not be removed on conflict")
            self.assertEqual(os.readlink(root_acme), external_legacy, "Symlink must still point to external legacy")

            # Neither source nor destination may be modified
            with open(os.path.join(external_legacy, "account.conf")) as f:
                self.assertEqual(f.read(), "LEGACY_CONFLICT_KEY")
            with open(os.path.join(persist_dir, "account.conf")) as f:
                self.assertEqual(f.read(), "DESTINATION_CONFLICT_KEY")

    def test_13_acme_migration_traversal_failure_unreadable_source(self):
        """Tests that an unreadable source traversal failure is detected and preserves source data."""
        with tempfile.TemporaryDirectory() as tmpdir:
            env, root_dir, cert_dir, persist_dir, root_acme, slash_acme = self._build_test_env(tmpdir)

            os.makedirs(cert_dir)
            os.makedirs(root_acme)
            os.makedirs(os.path.dirname(slash_acme), exist_ok=True)

            with open(os.path.join(root_acme, "valid.txt"), "w") as f:
                f.write("VALID_BEFORE_FAIL")

            unreadable_sub = os.path.join(root_acme, "unreadable_dir")
            os.makedirs(unreadable_sub)
            with open(os.path.join(unreadable_sub, "secret.key"), "w") as f:
                f.write("HIDDEN_KEY")
            os.chmod(unreadable_sub, 0)

            try:
                res = subprocess.run(
                    ["sh", self.entrypoint_path, "--migrate-acme-only"],
                    env=env,
                    capture_output=True,
                    text=True
                )

                # Traversal must fail with non-zero return code
                self.assertNotEqual(res.returncode, 0, "Entrypoint must fail closed on traversal error")
                self.assertIn("Traversal failed", res.stderr + res.stdout)

                # Source directory must NOT be renamed or deleted
                self.assertTrue(os.path.isdir(root_acme), "Source directory must be preserved on traversal failure")
                self.assertFalse(os.path.exists(root_acme + ".migrated"), "Source must not be marked as migrated")
                with open(os.path.join(root_acme, "valid.txt")) as f:
                    self.assertEqual(f.read(), "VALID_BEFORE_FAIL")
            finally:
                os.chmod(unreadable_sub, 0o700)

    def test_14_acme_migration_copy_failure_preserves_source(self):
        """Tests that a simulated copy failure (read-only destination) preserves source data untouched."""
        with tempfile.TemporaryDirectory() as tmpdir:
            env, root_dir, cert_dir, persist_dir, root_acme, slash_acme = self._build_test_env(tmpdir)

            os.makedirs(persist_dir)
            os.makedirs(root_acme)
            os.makedirs(os.path.dirname(slash_acme), exist_ok=True)

            with open(os.path.join(root_acme, "account.conf"), "w") as f:
                f.write("IMPORTANT_ACCOUNT_KEY")

            # Make destination read-only (mode 0500) to cause copy failure
            os.chmod(persist_dir, 0o500)

            try:
                res = subprocess.run(
                    ["sh", self.entrypoint_path, "--migrate-acme-only"],
                    env=env,
                    capture_output=True,
                    text=True
                )

                # Copy failure must cause non-zero exit code
                self.assertNotEqual(res.returncode, 0, "Entrypoint must fail closed on copy failure")

                # Source directory must remain intact and NOT renamed
                self.assertTrue(os.path.isdir(root_acme), "Source directory must remain intact on copy failure")
                self.assertFalse(os.path.exists(root_acme + ".migrated"), "Source must not be marked as migrated")
                with open(os.path.join(root_acme, "account.conf")) as f:
                    self.assertEqual(f.read(), "IMPORTANT_ACCOUNT_KEY")
            finally:
                os.chmod(persist_dir, 0o700)

    def test_15_subscription_and_panel_tls_certificate_paths_consistency(self):
        """Verifies that README.md, DOCKER_HUB_OVERVIEW.md, and x-ui.sh consistently use .pem paths."""
        overview_path = os.path.join(self.repo_root, "docs", "DOCKER_HUB_OVERVIEW.md")
        readme_path = os.path.join(self.repo_root, "README.md")

        with open(readme_path, "r", encoding="utf-8") as f:
            readme_content = f.read()

        with open(overview_path, "r", encoding="utf-8") as f:
            overview_content = f.read()

        with open(self.xui_sh_path, "r", encoding="utf-8") as f:
            xui_content = f.read()

        # 1. Negative checks: Must NOT contain obsolete or invalid .cer / .key in installed path examples
        self.assertNotIn("/root/cert/vpn.yourdomain.com/fullchain.cer", readme_content)
        self.assertNotIn("/root/cert/vpn.yourdomain.com/vpn.yourdomain.com.key", readme_content)
        self.assertNotIn("/root/cert/vpn.yourdomain.com/fullchain.cer", overview_content)
        self.assertNotIn("/root/cert/vpn.yourdomain.com/vpn.yourdomain.com.key", overview_content)

        # 2. Positive checks: Must prescribe exact installed .pem paths
        self.assertIn("/root/cert/vpn.yourdomain.com/fullchain.pem", readme_content)
        self.assertIn("/root/cert/vpn.yourdomain.com/privkey.pem", readme_content)
        self.assertIn("/root/cert/vpn.yourdomain.com/fullchain.pem", overview_content)
        self.assertIn("/root/cert/vpn.yourdomain.com/privkey.pem", overview_content)

        # 3. Code checks: x-ui.sh must install fullchain.pem and privkey.pem across all issuance flows
        self.assertIn('--key-file /root/cert/${domain}/privkey.pem', xui_content)
        self.assertIn('--fullchain-file /root/cert/${domain}/fullchain.pem', xui_content)
        self.assertIn('--key-file ${certPath}/privkey.pem', xui_content)
        self.assertIn('--fullchain-file ${certPath}/fullchain.pem', xui_content)
        self.assertIn('local webCertFile="/root/cert/${domain}/fullchain.pem"', xui_content)
        self.assertIn('local webKeyFile="/root/cert/${domain}/privkey.pem"', xui_content)


if __name__ == "__main__":
    unittest.main()
