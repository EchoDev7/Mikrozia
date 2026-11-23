#!/usr/bin/env python3
"""
Integration Tests for the Mikrozia Environment-Authoritative Administrator Contract.

The RouterOS Container Environment owns the panel administrator account:

1. Environment-owned account:
   - MIKROZIA_ADMIN_USERNAME and MIKROZIA_ADMIN_PASSWORD must be set together.
   - Both set -> the stored account is reconciled to exactly those values on
     every start; the password is stored as a bcrypt hash.
   - Only one set -> the panel refuses to start and names the incomplete pair.
   - Neither set and no account -> the panel refuses to start and asks for both.
   - Neither set and an account exists -> the account is preserved unchanged.
   - Neither set and a legacy admin/admin account -> the account is kept but
     the panel refuses to start until the environment provides a replacement.
2. No random first-login credential is ever generated or printed, and there is
   no fixed admin/admin default.
3. While the environment owns the account, a CLI or panel-side credential
   change is refused so that two sources of truth cannot compete.
4. Credential work never touches inbounds, client traffic, settings, or any
   other user data.
"""

import os
import shutil
import sqlite3
import subprocess
import tempfile
import time
import unittest


ADMIN_USERNAME_ENV = "MIKROZIA_ADMIN_USERNAME"
ADMIN_PASSWORD_ENV = "MIKROZIA_ADMIN_PASSWORD"


class TestEnvironmentAuthoritativeCredentials(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.repo_root = os.path.abspath(os.path.join(os.path.dirname(__file__), ".."))
        cls.xui_sh_path = os.path.join(cls.repo_root, "x-ui.sh")

        # Check docker availability
        try:
            res = subprocess.run(["docker", "info"], capture_output=True, text=True, timeout=10)
            cls.docker_available = (res.returncode == 0)
        except Exception:
            cls.docker_available = False

        # Release CI injects the freshly built local image. Never pull the new
        # release tag before it has passed tests and been published.
        cls.test_image = os.environ.get(
            "MIKROZIA_TEST_IMAGE", "echodev7/mikrozia:v1-mikrozia"
        )
        if cls.docker_available:
            images = subprocess.run(["docker", "images", "-q", cls.test_image], capture_output=True, text=True).stdout.strip()
            if not images and not os.environ.get("MIKROZIA_TEST_IMAGE"):
                # Fallback to whatever mikrozia or 3x-ui image exists
                for fallback in ["ghcr.io/mhsanaei/3x-ui:v3.11.0", "echodev7/mikrozia:0.1.0-alpha.7"]:
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

    def _connect_db(self, data_dir):
        db_path = os.path.join(data_dir, "x-ui.db")
        if hasattr(os, "getuid") and os.path.exists(db_path):
            uid = os.getuid()
            gid = os.getgid()
            try:
                if os.stat(db_path).st_uid != uid:
                    subprocess.run(
                        [
                            "docker", "run", "--rm",
                            "--entrypoint", "/bin/sh",
                            "-v", f"{data_dir}:/etc/x-ui",
                            self.test_image,
                            "-ec", f"chown -R {uid}:{gid} /etc/x-ui",
                        ],
                        check=True,
                        capture_output=True,
                        text=True,
                        timeout=15,
                    )
            except OSError:
                pass
        return sqlite3.connect(db_path)

    # --- helpers -----------------------------------------------------------

    def _run_container(self, name, data_dir, cert_dir=None, env=None, detach=True):
        cmd = ["docker", "run"]
        if detach:
            cmd.append("-d")
        cmd += ["--name", name, "-v", f"{data_dir}:/etc/x-ui"]
        if cert_dir:
            cmd += ["-v", f"{cert_dir}:/root/cert"]
        for key, value in (env or {}).items():
            cmd += ["-e", f"{key}={value}"]
        cmd.append(self.test_image)
        return subprocess.run(cmd, capture_output=True, text=True, timeout=60)

    def _logs(self, name):
        res = subprocess.run(["docker", "logs", name], capture_output=True, text=True)
        return res.stdout + res.stderr

    def _exit_code(self, name):
        res = subprocess.run(
            ["docker", "inspect", "-f", "{{.State.ExitCode}}", name],
            capture_output=True,
            text=True,
        )
        return res.stdout.strip()

    def _wait_for_log(self, name, needle, attempts=25):
        logs = ""
        for _ in range(attempts):
            logs = self._logs(name)
            if needle in logs:
                return True, logs
            time.sleep(1)
        return False, logs

    def _user_row(self, data_dir):
        db_path = os.path.join(data_dir, "x-ui.db")
        if not os.path.exists(db_path):
            return None
        con = self._connect_db(data_dir)
        try:
            return con.cursor().execute(
                "SELECT username, password, login_epoch FROM users ORDER BY id LIMIT 1"
            ).fetchone()
        finally:
            con.close()

    def _remove(self, *names):
        for name in names:
            subprocess.run(["docker", "rm", "-f", name], capture_output=True)

    # --- tests -------------------------------------------------------------

    def test_01_environment_credentials_create_the_admin_account(self):
        """Both environment values create the account exactly, hashed, with no credential printed."""
        if not self.docker_available:
            self.skipTest("Docker not available")

        data_dir = self.make_temp_dir()
        cert_dir = self.make_temp_dir()
        container_name = f"test_env_bootstrap_{int(time.time())}"
        username = "env_admin"
        password = "EnvOwnedPassword!2026"

        try:
            run_res = self._run_container(
                container_name,
                data_dir,
                cert_dir,
                env={ADMIN_USERNAME_ENV: username, ADMIN_PASSWORD_ENV: password},
            )
            self.assertEqual(run_res.returncode, 0, f"Container failed to start: {run_res.stderr}")

            found, logs = self._wait_for_log(
                container_name, "reconciled from the container environment"
            )
            self.assertTrue(found, f"Expected the reconciliation notice in:\n{logs}")

            # No random first-login credential may be generated or printed.
            self.assertNotIn("No panel user existed", logs)
            self.assertNotIn("Username:", logs)
            self.assertNotIn("Password:", logs)
            self.assertNotIn(password, logs)

            row = self._user_row(data_dir)
            self.assertIsNotNone(row, "The environment-owned account was not created")
            stored_user, stored_hash, _login_epoch = row
            self.assertEqual(stored_user, username)
            self.assertNotEqual(stored_user, "admin")
            self.assertTrue(
                stored_hash.startswith("$2a$10$"),
                f"Expected a bcrypt hash, got: {stored_hash[:7]}",
            )
            self.assertEqual(len(stored_hash), 60)
            self.assertNotIn(password, stored_hash)
        finally:
            self._remove(container_name)

    def test_02_missing_environment_without_account_refuses_to_start(self):
        """Without environment values and without an account the panel refuses to start."""
        if not self.docker_available:
            self.skipTest("Docker not available")

        data_dir = self.make_temp_dir()
        container_name = f"test_env_missing_{int(time.time())}"

        try:
            run_res = self._run_container(container_name, data_dir)
            self.assertEqual(run_res.returncode, 0, f"Container failed to start: {run_res.stderr}")

            found, logs = self._wait_for_log(container_name, ADMIN_USERNAME_ENV)
            self.assertTrue(found, f"Expected the missing-credential message in:\n{logs}")
            self.assertIn(ADMIN_PASSWORD_ENV, logs)

            # The refusal must not invent a credential.
            self.assertNotIn("Username:", logs)
            self.assertNotIn("Password:", logs)
            self.assertNotEqual(self._exit_code(container_name), "0")

            self.assertIsNone(self._user_row(data_dir))
        finally:
            self._remove(container_name)

    def test_03_half_configured_environment_refuses_to_start(self):
        """A half-configured environment pair stops the panel and names the incomplete pair."""
        if not self.docker_available:
            self.skipTest("Docker not available")

        for missing in (ADMIN_PASSWORD_ENV, ADMIN_USERNAME_ENV):
            with self.subTest(missing=missing):
                data_dir = self.make_temp_dir()
                container_name = f"test_env_half_{int(time.time())}"
                env = {ADMIN_USERNAME_ENV: "half_admin"}
                if missing == ADMIN_USERNAME_ENV:
                    env = {ADMIN_PASSWORD_ENV: "HalfOnlyPassword!2026"}
                try:
                    run_res = self._run_container(container_name, data_dir, env=env)
                    self.assertEqual(run_res.returncode, 0, run_res.stderr)

                    found, logs = self._wait_for_log(container_name, "must be set in the container environment")
                    self.assertTrue(found, f"Expected the incomplete-pair message in:\n{logs}")
                    self.assertIn(ADMIN_USERNAME_ENV, logs)
                    self.assertIn(ADMIN_PASSWORD_ENV, logs)
                    self.assertNotEqual(self._exit_code(container_name), "0")

                    self.assertIsNone(self._user_row(data_dir))
                finally:
                    self._remove(container_name)

    def test_04_existing_account_is_preserved_without_environment(self):
        """An existing account survives a start without environment values and is not reset."""
        if not self.docker_available:
            self.skipTest("Docker not available")

        data_dir = self.make_temp_dir()
        first = f"test_env_seed_{int(time.time())}"
        second = f"test_env_keep_{int(time.time())}"

        try:
            self._run_container(
                first,
                data_dir,
                env={ADMIN_USERNAME_ENV: "kept_admin", ADMIN_PASSWORD_ENV: "KeptPassword!2026"},
            )
            found, logs = self._wait_for_log(first, "reconciled from the container environment")
            self.assertTrue(found, logs)
            subprocess.run(["docker", "stop", first], capture_output=True, timeout=30)

            run_res = self._run_container(second, data_dir)
            self.assertEqual(run_res.returncode, 0, run_res.stderr)
            found, logs = self._wait_for_log(second, "was left unchanged")
            self.assertTrue(found, f"Expected the preservation notice in:\n{logs}")

            row = self._user_row(data_dir)
            self.assertIsNotNone(row)
            self.assertEqual(row[0], "kept_admin")
        finally:
            self._remove(first, second)

    def test_05_legacy_default_account_is_kept_but_blocks_start(self):
        """A stored admin/admin account is preserved but never left usable."""
        if not self.docker_available:
            self.skipTest("Docker not available")

        data_dir = self.make_temp_dir()
        first = f"test_env_legacy_{int(time.time())}"
        second = f"test_env_legacy_restart_{int(time.time())}"

        try:
            self._run_container(
                first,
                data_dir,
                env={ADMIN_USERNAME_ENV: "admin", ADMIN_PASSWORD_ENV: "admin"},
            )
            found, logs = self._wait_for_log(first, "reconciled from the container environment")
            self.assertTrue(found, logs)
            subprocess.run(["docker", "stop", first], capture_output=True, timeout=30)

            run_res = self._run_container(second, data_dir)
            self.assertEqual(run_res.returncode, 0, run_res.stderr)
            found, logs = self._wait_for_log(second, "legacy default credential")
            self.assertTrue(found, f"Expected the legacy-default refusal in:\n{logs}")
            self.assertIn(ADMIN_USERNAME_ENV, logs)
            self.assertIn(ADMIN_PASSWORD_ENV, logs)
            self.assertNotEqual(self._exit_code(second), "0")

            # The account is kept, never deleted or silently rewritten.
            row = self._user_row(data_dir)
            self.assertIsNotNone(row, "The legacy account was deleted")
            self.assertEqual(row[0], "admin")
        finally:
            self._remove(first, second)

    def test_06_environment_reconciles_on_every_start(self):
        """Changing the environment values updates the stored account on the next start."""
        if not self.docker_available:
            self.skipTest("Docker not available")

        data_dir = self.make_temp_dir()
        first = f"test_env_reconcile_a_{int(time.time())}"
        second = f"test_env_reconcile_b_{int(time.time())}"

        try:
            self._run_container(
                first,
                data_dir,
                env={ADMIN_USERNAME_ENV: "first_admin", ADMIN_PASSWORD_ENV: "FirstPassword!2026"},
            )
            found, logs = self._wait_for_log(first, "reconciled from the container environment")
            self.assertTrue(found, logs)
            subprocess.run(["docker", "stop", first], capture_output=True, timeout=30)

            before = self._user_row(data_dir)
            self.assertIsNotNone(before)

            self._run_container(
                second,
                data_dir,
                env={ADMIN_USERNAME_ENV: "second_admin", ADMIN_PASSWORD_ENV: "SecondPassword!2026"},
            )
            found, logs = self._wait_for_log(second, "reconciled from the container environment")
            self.assertTrue(found, logs)
            subprocess.run(["docker", "stop", second], capture_output=True, timeout=30)

            after = self._user_row(data_dir)
            self.assertIsNotNone(after)
            self.assertEqual(after[0], "second_admin")
            self.assertNotEqual(after[1], before[1])
            self.assertTrue(after[1].startswith("$2a$10$"))
            self.assertGreater(after[2], before[2], "login_epoch must advance on a password change")
        finally:
            self._remove(first, second)

    def test_07_cli_credential_change_is_refused_while_environment_owns_the_account(self):
        """The CLI refuses to write a competing credential and leaves the account untouched."""
        if not self.docker_available:
            self.skipTest("Docker not available")

        data_dir = self.make_temp_dir()
        container_name = f"test_env_refuse_{int(time.time())}"

        try:
            self._run_container(
                container_name,
                data_dir,
                env={ADMIN_USERNAME_ENV: "env_owner", ADMIN_PASSWORD_ENV: "EnvOwnedPassword!2026"},
            )
            found, logs = self._wait_for_log(container_name, "reconciled from the container environment")
            self.assertTrue(found, logs)

            before = self._user_row(data_dir)
            self.assertIsNotNone(before)

            upd_res = subprocess.run(
                [
                    "docker", "exec", container_name,
                    "/app/x-ui", "setting",
                    "-username", "cli_admin",
                    "-password", "CliPassword!2026",
                ],
                capture_output=True,
                text=True,
                timeout=30,
            )
            combined = upd_res.stdout + upd_res.stderr
            self.assertIn("Refusing to change the administrator credential", combined)
            self.assertIn(ADMIN_USERNAME_ENV, combined)
            self.assertIn(ADMIN_PASSWORD_ENV, combined)
            self.assertNotIn("Username and password updated successfully", combined)

            subprocess.run(["docker", "stop", container_name], capture_output=True, timeout=30)
            after = self._user_row(data_dir)
            self.assertEqual(after[0], before[0])
            self.assertEqual(after[1], before[1])
        finally:
            self._remove(container_name)

    def test_08_setting_show_never_prints_plaintext(self):
        """`setting -show` reports the panel settings without any plaintext credential."""
        if not self.docker_available:
            self.skipTest("Docker not available")

        container_name = f"test_env_show_{int(time.time())}"
        data_dir = self.make_temp_dir()

        try:
            self._run_container(
                container_name,
                data_dir,
                env={ADMIN_USERNAME_ENV: "env_owner", ADMIN_PASSWORD_ENV: "EnvOwnedPassword!2026"},
            )
            found, logs = self._wait_for_log(container_name, "reconciled from the container environment")
            self.assertTrue(found, logs)

            show_res = subprocess.run(
                ["docker", "exec", container_name, "/app/x-ui", "setting", "-show"],
                capture_output=True,
                text=True,
                timeout=30,
            )
            self.assertEqual(show_res.returncode, 0, f"setting -show failed: {show_res.stderr}")
            output = show_res.stdout + show_res.stderr

            self.assertIn("current panel settings as follows:", output)
            self.assertIn("port:", output)
            self.assertIn("webBasePath:", output)
            self.assertIn("hasDefaultCredential: false", output)
            self.assertNotIn("Password:", output)
            self.assertNotIn("EnvOwnedPassword!2026", output)
        finally:
            self._remove(container_name)

    def test_09_reconciliation_preserves_inbounds_and_settings(self):
        """Reconciling the credential never touches inbounds, client data, or settings."""
        if not self.docker_available:
            self.skipTest("Docker not available")

        data_dir = self.make_temp_dir()
        container_name = f"test_env_preserve_{int(time.time())}"

        try:
            self._run_container(
                container_name,
                data_dir,
                env={ADMIN_USERNAME_ENV: "env_owner", ADMIN_PASSWORD_ENV: "EnvOwnedPassword!2026"},
            )
            found, logs = self._wait_for_log(container_name, "reconciled from the container environment")
            self.assertTrue(found, logs)

            # Stop the container to safely insert mock data without virtiofs locking conflicts
            subprocess.run(["docker", "stop", container_name], capture_output=True, timeout=30)

            db_path = os.path.join(data_dir, "x-ui.db")
            con = self._connect_db(data_dir)
            cur = con.cursor()
            cur.execute("""
                INSERT INTO inbounds (user_id, up, down, total, remark, enable, expiry_time, listen, port, protocol, settings, stream_settings, tag, sniffing)
                VALUES (1, 1024, 2048, 4096, 'Production VLESS REALITY', 1, 0, '0.0.0.0', 443, 'vless', '{"clients":[]}', '{}', 'inbound-443', '{}')
            """)
            cur.execute("""
                INSERT INTO inbounds (user_id, up, down, total, remark, enable, expiry_time, listen, port, protocol, settings, stream_settings, tag, sniffing)
                VALUES (1, 500, 600, 1100, 'Shadowsocks Inbound', 1, 0, '0.0.0.0', 8388, 'shadowsocks', '{}', '{}', 'inbound-8388', '{}')
            """)
            cur.execute("INSERT INTO settings (key, value) VALUES ('webPort', '2053')")
            con.commit()
            con.close()

            # Restart with a changed environment value
            subprocess.run(["docker", "rm", "-f", container_name], capture_output=True)
            second = f"{container_name}_b"
            self._run_container(
                second,
                data_dir,
                env={ADMIN_USERNAME_ENV: "env_owner", ADMIN_PASSWORD_ENV: "RotatedPassword!2026"},
            )
            found, logs = self._wait_for_log(second, "reconciled from the container environment")
            self.assertTrue(found, logs)
            subprocess.run(["docker", "stop", second], capture_output=True, timeout=30)

            con = self._connect_db(data_dir)
            cur = con.cursor()
            inbounds = cur.execute("SELECT remark, port, protocol FROM inbounds ORDER BY port").fetchall()
            users = cur.execute("SELECT username FROM users").fetchall()
            port = cur.execute("SELECT value FROM settings WHERE key='webPort'").fetchone()
            con.close()

            self.assertEqual(len(users), 1)
            self.assertEqual(users[0][0], "env_owner")
            self.assertEqual(
                inbounds,
                [
                    ("Production VLESS REALITY", 443, "vless"),
                    ("Shadowsocks Inbound", 8388, "shadowsocks"),
                ],
            )
            self.assertEqual(port[0], "2053")
            self._remove(second)
        finally:
            self._remove(container_name)

    def test_10_xui_sh_forwards_setting_and_cert_without_a_menu(self):
        """x-ui.sh forwards `setting` and `cert` to the Go binary without an interactive menu."""
        if not self.docker_available:
            self.skipTest("Docker not available")

        container_name = f"test_env_forward_{int(time.time())}"
        data_dir = self.make_temp_dir()

        try:
            self._run_container(
                container_name,
                data_dir,
                env={ADMIN_USERNAME_ENV: "env_owner", ADMIN_PASSWORD_ENV: "EnvOwnedPassword!2026"},
            )
            found, logs = self._wait_for_log(container_name, "reconciled from the container environment")
            self.assertTrue(found, logs)

            cp_res = subprocess.run(
                ["docker", "cp", self.xui_sh_path, f"{container_name}:/usr/bin/x-ui"],
                capture_output=True,
                text=True,
            )
            self.assertEqual(cp_res.returncode, 0, cp_res.stderr)
            subprocess.run(["docker", "exec", container_name, "chmod", "+x", "/usr/bin/x-ui"], check=True)

            show_res = subprocess.run(
                ["docker", "exec", container_name, "x-ui", "setting", "-show"],
                capture_output=True,
                text=True,
                timeout=30,
            )
            self.assertEqual(show_res.returncode, 0, f"x-ui setting -show failed: {show_res.stderr}")
            self.assertIn("current panel settings as follows:", show_res.stdout)
            self.assertNotIn("3X-UI Panel Management Script", show_res.stdout)

            # The forwarded CLI still refuses a competing credential write.
            upd_res = subprocess.run(
                [
                    "docker", "exec", container_name,
                    "x-ui", "setting",
                    "-username", "forwarded_user",
                    "-password", "ForwardedPass123!",
                ],
                capture_output=True,
                text=True,
                timeout=30,
            )
            combined = upd_res.stdout + upd_res.stderr
            self.assertIn("Refusing to change the administrator credential", combined)
        finally:
            self._remove(container_name)


if __name__ == "__main__":
    unittest.main()
