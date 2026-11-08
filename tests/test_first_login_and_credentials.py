#!/usr/bin/env python3
"""
Unit and Integration Tests for Mikrozia First Login and Credential Recovery (Task 05).

Verifies:
1. Fresh Installation:
   - 3x-ui generates random 10-char username and 16-char password on initial boot.
   - Credentials are saved as bcrypt hashes ($2a$10$...) in /etc/x-ui/x-ui.db.
   - Initial credentials are NOT 'admin'/'admin'; hasDefaultCredential is false.
   - RouterOS-visible logs print: 'No panel user existed, so one was generated...'
2. Credential Inspection via CLI:
   - '/app/x-ui setting -show' outputs port, webBasePath, SSL status, and hasDefaultCredential.
   - Does NOT output plaintext passwords.
3. Credential Reset / Direct Configuration:
   - '/app/x-ui setting -username <user> -password <pass>' updates the administrator account.
   - Hashes password with bcrypt and increments login_epoch.
4. Database Preservation:
   - Setting credentials strictly affects the users table.
   - Inbounds, client traffics, settings, and other tables remain completely intact.
5. Two-Factor Authentication Reset:
   - '-resetTwoFactor=true' resets 2FA flags without corrupting configuration.
6. Setting Reset Behavior:
   - '/app/x-ui setting -reset' resets panel configurations but does NOT delete or alter user credentials.
7. Wrapper Script CLI Forwarding:
   - 'x-ui.sh' forwards 'setting' and 'cert' subcommands directly to '/app/x-ui',
     allowing 'x-ui setting ...' and '/app/x-ui setting ...' to work interchangeably.
8. Persistent Mount Retention:
   - Container recreate retains updated credentials when /etc/x-ui is mounted.
"""

import os
import shutil
import sqlite3
import subprocess
import tempfile
import time
import unittest


class TestFirstLoginAndCredentials(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.repo_root = os.path.abspath(os.path.join(os.path.dirname(__file__), ".."))
        cls.xui_sh_path = os.path.join(cls.repo_root, "x-ui.sh")
        cls.entrypoint_path = os.path.join(cls.repo_root, "DockerEntrypoint.sh")

        # Check docker availability
        try:
            res = subprocess.run(["docker", "info"], capture_output=True, text=True, timeout=10)
            cls.docker_available = (res.returncode == 0)
        except Exception:
            cls.docker_available = False

        # Release CI injects the freshly built local image. Never pull the new
        # release tag before it has passed tests and been published.
        cls.test_image = os.environ.get(
            "MIKROZIA_TEST_IMAGE", "majlot/mikrozia:v3.11.0-mikrozia.4"
        )
        if cls.docker_available:
            images = subprocess.run(["docker", "images", "-q", cls.test_image], capture_output=True, text=True).stdout.strip()
            if not images:
                # Fallback to whatever mikrozia or 3x-ui image exists
                for fallback in ["ghcr.io/mhsanaei/3x-ui:v3.11.0", "majlot/mikrozia:0.1.0-alpha.7"]:
                    chk = subprocess.run(["docker", "images", "-q", fallback], capture_output=True, text=True).stdout.strip()
                    if chk:
                        cls.test_image = fallback
                        images = chk
                        break
            if not images:
                cls.docker_available = False

    def setUp(self):
        self.temp_dirs = []

    def tearDown(self):
        for d in self.temp_dirs:
            if os.path.exists(d):
                shutil.rmtree(d, ignore_errors=True)

    def make_temp_dir(self):
        d = tempfile.mkdtemp()
        self.temp_dirs.append(d)
        return d

    def test_01_first_boot_random_credentials_generation(self):
        """Item 1: On fresh boot, random credentials are generated, logged once, and stored as bcrypt."""
        if not self.docker_available:
            self.skipTest("Docker not available")

        data_dir = self.make_temp_dir()
        cert_dir = self.make_temp_dir()
        container_name = f"test_first_login_{int(time.time())}"

        try:
            # Run container briefly to let it initialize first-time user
            run_cmd = [
                "docker", "run", "-d",
                "--name", container_name,
                "-v", f"{data_dir}:/etc/x-ui",
                "-v", f"{cert_dir}:/root/cert",
                self.test_image
            ]
            run_res = subprocess.run(run_cmd, capture_output=True, text=True, timeout=20)
            self.assertEqual(run_res.returncode, 0, f"Container failed to start: {run_res.stderr}")

            # Wait up to 10 seconds for initialization
            found_log = False
            logs = ""
            for _ in range(10):
                log_res = subprocess.run(["docker", "logs", container_name], capture_output=True, text=True)
                logs = log_res.stdout + log_res.stderr
                if "No panel user existed, so one was generated" in logs:
                    found_log = True
                    break
                time.sleep(1)

            self.assertTrue(found_log, f"Expected initial user generation log not found in:\n{logs}")
            self.assertIn("Username:", logs)
            self.assertIn("Password:", logs)
            self.assertIn("shown once; change it in Panel Settings", logs)

            # Check that credentials are NOT admin/admin
            self.assertNotIn("Username: admin", logs)

            # Inspect created SQLite database
            db_path = os.path.join(data_dir, "x-ui.db")
            self.assertTrue(os.path.exists(db_path), f"Database not found at {db_path}")

            con = sqlite3.connect(db_path)
            cur = con.cursor()
            user_row = cur.execute("SELECT id, username, password, login_epoch FROM users").fetchone()
            con.close()

            self.assertIsNotNone(user_row, "No user record found in SQLite database")
            uid, username, pwd_hash, login_epoch = user_row

            # Username is 10 chars (random.Seq(10))
            self.assertEqual(len(username), 10, f"Expected 10-char username, got: {username}")
            self.assertNotEqual(username, "admin")

            # Password is a 60-character bcrypt hash ($2a$10$...)
            self.assertEqual(len(pwd_hash), 60, f"Expected 60-char bcrypt hash, got length {len(pwd_hash)}")
            self.assertTrue(pwd_hash.startswith("$2a$10$"), f"Expected bcrypt prefix $2a$10$, got: {pwd_hash[:7]}")

        finally:
            subprocess.run(["docker", "rm", "-f", container_name], capture_output=True)

    def test_02_show_setting_cli_behavior(self):
        """Item 2: /app/x-ui setting -show displays panel settings but never plaintext passwords."""
        if not self.docker_available:
            self.skipTest("Docker not available")

        container_name = f"test_show_setting_{int(time.time())}"
        data_dir = self.make_temp_dir()

        try:
            run_cmd = [
                "docker", "run", "-d",
                "--name", container_name,
                "-v", f"{data_dir}:/etc/x-ui",
                self.test_image
            ]
            subprocess.run(run_cmd, capture_output=True, text=True, timeout=20)
            time.sleep(3)

            # Run /app/x-ui setting -show
            show_res = subprocess.run(
                ["docker", "exec", container_name, "/app/x-ui", "setting", "-show"],
                capture_output=True,
                text=True,
                timeout=10
            )
            self.assertEqual(show_res.returncode, 0, f"setting -show failed: {show_res.stderr}")
            output = show_res.stdout

            self.assertIn("current panel settings as follows:", output)
            self.assertIn("port:", output)
            self.assertIn("webBasePath:", output)
            self.assertIn("hasDefaultCredential: false", output)
            # Plaintext password must NOT be in output
            self.assertNotIn("Password:", output)
            self.assertNotIn("password:", output)

        finally:
            subprocess.run(["docker", "rm", "-f", container_name], capture_output=True)

    def test_03_credential_reset_updates_user(self):
        """Item 3: /app/x-ui setting -username ... -password ... updates user and increments login_epoch."""
        if not self.docker_available:
            self.skipTest("Docker not available")

        container_name = f"test_cred_reset_{int(time.time())}"
        data_dir = self.make_temp_dir()

        try:
            subprocess.run([
                "docker", "run", "-d",
                "--name", container_name,
                "-v", f"{data_dir}:/etc/x-ui",
                self.test_image
            ], capture_output=True, text=True, timeout=20)
            time.sleep(3)

            # Set custom credentials via CLI
            new_user = "mikrozia_admin"
            new_pass = "P@ssw0rdSecure!2026"
            upd_res = subprocess.run([
                "docker", "exec", container_name,
                "/app/x-ui", "setting",
                "-username", new_user,
                "-password", new_pass
            ], capture_output=True, text=True, timeout=10)

            self.assertEqual(upd_res.returncode, 0, f"Failed to update credentials: {upd_res.stderr}")
            self.assertIn("Username and password updated successfully", upd_res.stdout)

            # Stop container before host reads SQLite to guarantee clean WAL flush and avoid virtiofs lock collisions
            subprocess.run(["docker", "stop", container_name], capture_output=True, timeout=10)

            # Verify database
            db_path = os.path.join(data_dir, "x-ui.db")
            con = sqlite3.connect(db_path)
            cur = con.cursor()
            after_row = cur.execute("SELECT username, password, login_epoch FROM users").fetchone()
            con.close()

            self.assertIsNotNone(after_row)
            updated_user, updated_hash, updated_epoch = after_row
            self.assertEqual(updated_user, new_user)
            self.assertTrue(updated_hash.startswith("$2a$10$"))
            self.assertEqual(len(updated_hash), 60)
            self.assertGreaterEqual(updated_epoch, 1)

        finally:
            subprocess.run(["docker", "rm", "-f", container_name], capture_output=True)

    def test_04_database_tables_preservation(self):
        """Item 4: Credential updates preserve all inbounds, client data, and general settings."""
        if not self.docker_available:
            self.skipTest("Docker not available")

        container_name = f"test_preservation_{int(time.time())}"
        data_dir = self.make_temp_dir()

        try:
            subprocess.run([
                "docker", "run", "-d",
                "--name", container_name,
                "-v", f"{data_dir}:/etc/x-ui",
                self.test_image
            ], capture_output=True, text=True, timeout=20)
            time.sleep(3)

            # Stop container to safely insert mock data without virtiofs locking conflicts
            subprocess.run(["docker", "stop", container_name], capture_output=True, timeout=10)

            db_path = os.path.join(data_dir, "x-ui.db")
            con = sqlite3.connect(db_path)
            cur = con.cursor()
            cur.execute("""
                INSERT INTO inbounds (user_id, up, down, total, remark, enable, expiry_time, listen, port, protocol, settings, stream_settings, tag, sniffing)
                VALUES (1, 1024, 2048, 4096, 'Production VLESS REALITY', 1, 0, '0.0.0.0', 443, 'vless', '{"clients":[]}', '{}', 'inbound-443', '{}')
            """)
            cur.execute("""
                INSERT INTO inbounds (user_id, up, down, total, remark, enable, expiry_time, listen, port, protocol, settings, stream_settings, tag, sniffing)
                VALUES (1, 500, 600, 1100, 'Shadowsocks Inbound', 1, 0, '0.0.0.0', 8388, 'shadowsocks', '{}', '{}', 'inbound-8388', '{}')
            """)
            con.commit()
            con.close()

            # Restart container
            subprocess.run(["docker", "start", container_name], capture_output=True, timeout=10)
            time.sleep(2)

            # Execute credential update via CLI
            upd_res = subprocess.run([
                "docker", "exec", container_name,
                "/app/x-ui", "setting",
                "-username", "operator_secure",
                "-password", "CorrectHorseBatteryStaple!"
            ], capture_output=True, text=True, timeout=10)
            self.assertEqual(upd_res.returncode, 0)
            self.assertIn("Username and password updated successfully", upd_res.stdout)

            # Stop container for inspection
            subprocess.run(["docker", "stop", container_name], capture_output=True, timeout=10)

            # Query database and verify all inbounds and settings survived
            con = sqlite3.connect(db_path)
            cur = con.cursor()
            inbounds = cur.execute("SELECT remark, port, protocol FROM inbounds ORDER BY port").fetchall()
            user_info = cur.execute("SELECT username FROM users").fetchall()
            con.close()

            self.assertEqual(len(user_info), 1)
            self.assertEqual(user_info[0][0], "operator_secure")

            self.assertEqual(len(inbounds), 2)
            self.assertEqual(inbounds[0], ("Production VLESS REALITY", 443, "vless"))
            self.assertEqual(inbounds[1], ("Shadowsocks Inbound", 8388, "shadowsocks"))

        finally:
            subprocess.run(["docker", "rm", "-f", container_name], capture_output=True)

    def test_05_two_factor_auth_reset(self):
        """Item 5: -resetTwoFactor=true disables 2FA in settings table when recovering credentials."""
        if not self.docker_available:
            self.skipTest("Docker not available")

        container_name = f"test_2fa_reset_{int(time.time())}"
        data_dir = self.make_temp_dir()

        try:
            subprocess.run([
                "docker", "run", "-d",
                "--name", container_name,
                "-v", f"{data_dir}:/etc/x-ui",
                self.test_image
            ], capture_output=True, text=True, timeout=20)
            time.sleep(3)

            # Stop container to safely populate 2FA settings
            subprocess.run(["docker", "stop", container_name], capture_output=True, timeout=10)

            db_path = os.path.join(data_dir, "x-ui.db")
            con = sqlite3.connect(db_path)
            cur = con.cursor()
            cur.execute("DELETE FROM settings WHERE key IN ('twoFactorEnable', 'twoFactorToken')")
            cur.execute("INSERT INTO settings (key, value) VALUES ('twoFactorEnable', 'true')")
            cur.execute("INSERT INTO settings (key, value) VALUES ('twoFactorToken', 'MYSECRETTOKEN123')")
            con.commit()
            con.close()

            # Restart container
            subprocess.run(["docker", "start", container_name], capture_output=True, timeout=10)
            time.sleep(2)

            # Execute resetTwoFactor command
            res = subprocess.run([
                "docker", "exec", container_name,
                "/app/x-ui", "setting",
                "-username", "recovered_user",
                "-password", "NewPass999!",
                "-resetTwoFactor=true"
            ], capture_output=True, text=True, timeout=10)
            self.assertEqual(res.returncode, 0)
            self.assertIn("Two-factor authentication reset successfully", res.stdout)

            # Stop container for inspection
            subprocess.run(["docker", "stop", container_name], capture_output=True, timeout=10)

            # Verify in settings table
            con = sqlite3.connect(db_path)
            cur = con.cursor()
            enable_val = cur.execute("SELECT value FROM settings WHERE key='twoFactorEnable'").fetchone()
            token_val = cur.execute("SELECT value FROM settings WHERE key='twoFactorToken'").fetchone()
            con.close()

            self.assertEqual(enable_val[0], "false")
            self.assertEqual(token_val[0], "")

        finally:
            subprocess.run(["docker", "rm", "-f", container_name], capture_output=True)

    def test_06_setting_reset_preserves_user(self):
        """Item 6: /app/x-ui setting -reset resets panel config without wiping user credentials."""
        if not self.docker_available:
            self.skipTest("Docker not available")

        container_name = f"test_setting_reset_{int(time.time())}"
        data_dir = self.make_temp_dir()

        try:
            subprocess.run([
                "docker", "run", "-d",
                "--name", container_name,
                "-v", f"{data_dir}:/etc/x-ui",
                self.test_image
            ], capture_output=True, text=True, timeout=20)
            time.sleep(3)

            # Set custom credentials first
            subprocess.run([
                "docker", "exec", container_name,
                "/app/x-ui", "setting",
                "-username", "custom_admin",
                "-password", "SuperSecretPass!"
            ], capture_output=True, text=True, timeout=10)

            # Now execute setting -reset
            reset_res = subprocess.run([
                "docker", "exec", container_name,
                "/app/x-ui", "setting",
                "-reset"
            ], capture_output=True, text=True, timeout=10)
            self.assertEqual(reset_res.returncode, 0)

            # Verify user record was NOT wiped or changed back to admin
            db_path = os.path.join(data_dir, "x-ui.db")
            con = sqlite3.connect(db_path)
            cur = con.cursor()
            user_row = cur.execute("SELECT username, password FROM users").fetchone()
            con.close()

            self.assertIsNotNone(user_row)
            self.assertEqual(user_row[0], "custom_admin")
            self.assertTrue(user_row[1].startswith("$2a$10$"))

        finally:
            subprocess.run(["docker", "rm", "-f", container_name], capture_output=True)

    def test_07_xui_sh_cli_forwarding(self):
        """Item 7: x-ui.sh forwards 'setting' and 'cert' to /app/x-ui without interactive menus."""
        if not self.docker_available:
            self.skipTest("Docker not available")

        container_name = f"test_cli_forwarding_{int(time.time())}"
        data_dir = self.make_temp_dir()

        try:
            subprocess.run([
                "docker", "run", "-d",
                "--name", container_name,
                "-v", f"{data_dir}:/etc/x-ui",
                self.test_image
            ], capture_output=True, text=True, timeout=20)
            time.sleep(3)

            # Copy latest x-ui.sh into container
            cp_res = subprocess.run([
                "docker", "cp", self.xui_sh_path, f"{container_name}:/usr/bin/x-ui"
            ], capture_output=True, text=True)
            self.assertEqual(cp_res.returncode, 0)
            subprocess.run(["docker", "exec", container_name, "chmod", "+x", "/usr/bin/x-ui"], check=True)

            # Test x-ui setting -show
            show_res = subprocess.run([
                "docker", "exec", container_name,
                "x-ui", "setting", "-show"
            ], capture_output=True, text=True, timeout=10)
            self.assertEqual(show_res.returncode, 0)
            self.assertIn("current panel settings as follows:", show_res.stdout)
            self.assertNotIn("3X-UI Panel Management Script", show_res.stdout)  # No interactive menu

            # Test x-ui setting -username ... -password ...
            upd_res = subprocess.run([
                "docker", "exec", container_name,
                "x-ui", "setting",
                "-username", "forwarded_user",
                "-password", "ForwardedPass123!"
            ], capture_output=True, text=True, timeout=10)
            self.assertEqual(upd_res.returncode, 0)
            self.assertIn("Username and password updated successfully", upd_res.stdout)

            # Verify in DB
            db_path = os.path.join(data_dir, "x-ui.db")
            con = sqlite3.connect(db_path)
            user_row = con.cursor().execute("SELECT username FROM users").fetchone()
            con.close()
            self.assertEqual(user_row[0], "forwarded_user")

        finally:
            subprocess.run(["docker", "rm", "-f", container_name], capture_output=True)

    def test_08_container_restart_preserves_updated_credentials(self):
        """Item 8: Container recreate preserves updated credentials when /etc/x-ui is mounted."""
        if not self.docker_available:
            self.skipTest("Docker not available")

        container_name_1 = f"test_persist_1_{int(time.time())}"
        container_name_2 = f"test_persist_2_{int(time.time())}"
        data_dir = self.make_temp_dir()

        try:
            # 1. Start first container and set credentials
            subprocess.run([
                "docker", "run", "-d",
                "--name", container_name_1,
                "-v", f"{data_dir}:/etc/x-ui",
                self.test_image
            ], capture_output=True, text=True, timeout=20)
            time.sleep(3)

            subprocess.run([
                "docker", "exec", container_name_1,
                "/app/x-ui", "setting",
                "-username", "persistent_admin",
                "-password", "KeepThisSafePassword!"
            ], capture_output=True, text=True, timeout=10)

            # Stop and remove first container (simulating upgrade or router reboot)
            subprocess.run(["docker", "rm", "-f", container_name_1], capture_output=True)

            # 2. Start second container with same mounted volume
            subprocess.run([
                "docker", "run", "-d",
                "--name", container_name_2,
                "-v", f"{data_dir}:/etc/x-ui",
                self.test_image
            ], capture_output=True, text=True, timeout=20)
            time.sleep(3)

            # Check logs of second container - should NOT say "No panel user existed"
            log_res = subprocess.run(["docker", "logs", container_name_2], capture_output=True, text=True)
            self.assertNotIn("No panel user existed, so one was generated", log_res.stdout + log_res.stderr)

            # Verify credentials in database are still persistent_admin
            db_path = os.path.join(data_dir, "x-ui.db")
            con = sqlite3.connect(db_path)
            user_row = con.cursor().execute("SELECT username FROM users").fetchone()
            con.close()

            self.assertEqual(user_row[0], "persistent_admin")

        finally:
            subprocess.run(["docker", "rm", "-f", container_name_1], capture_output=True)
            subprocess.run(["docker", "rm", "-f", container_name_2], capture_output=True)


if __name__ == "__main__":
    unittest.main()
