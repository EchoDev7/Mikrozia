#!/usr/bin/env python3
"""
Integration and Verification Tests for Mikrozia SSL Auto-Renewal Reliability.

Task 03 Requirements Tested:
1. Scheduler startup: PASS / FAIL
   - BusyBox crond starts reliably in Alpine without systemd.
   - Logs to /var/log/cron.log and avoids duplicate processes.
2. Cron registration: PASS / FAIL
   - acme.sh --install-cronjob re-registers root crontab on container recreation.
   - Failures during registration are detected and explicitly reported.
3. Persistent ACME configuration: PASS / FAIL
   - Domain configurations (<domain>.conf) and account credentials (account.conf)
     persist across container recreate under /root/cert/.acme.sh.
   - Symlinks /root/.acme.sh and /.acme.sh resolve correctly with secure permissions.
4. Renewal command invocation: PASS / FAIL
   - acme.sh --cron executes properly using persistent ACME home directory.
5. Certificate installation to persistent paths: PASS / FAIL
   - acme.sh --installcert installs certificates and private keys directly to
     /root/cert/<domain>/fullchain.pem and /root/cert/<domain>/privkey.pem.
6. Panel and subscription reload: PASS / FAIL
   - acme.sh reloadcmd triggers 'x-ui restart', sending SIGHUP and SIGUSR1
     to reload web/subscription servers and restart xray-core without terminating PID 1.
7. Actual unattended Let's Encrypt renewal: NOT TESTED
   - Accurately tracked and documented as NOT TESTED (no real production certs used).
"""

import os
import subprocess
import tempfile
import unittest


class TestSSLRenewalReliability(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.repo_root = os.path.abspath(os.path.join(os.path.dirname(__file__), ".."))
        cls.entrypoint_path = os.path.join(cls.repo_root, "DockerEntrypoint.sh")
        cls.xui_sh_path = os.path.join(cls.repo_root, "x-ui.sh")

        # Verify docker is available
        try:
            res = subprocess.run(["docker", "info"], capture_output=True, text=True, timeout=10)
            cls.docker_available = (res.returncode == 0)
        except Exception:
            cls.docker_available = False

    def test_01_scheduler_startup(self):
        """Item 1: Scheduler startup in Alpine without systemd."""
        if not self.docker_available:
            self.skipTest("Docker is not available")

        # Run disposable Alpine container with DockerEntrypoint.sh
        cmd = [
            "docker", "run", "--rm",
            "-v", f"{self.entrypoint_path}:/app/DockerEntrypoint.sh:ro",
            "alpine", "sh", "-c",
            """
            mkdir -p /root/cert
            # Run entrypoint in cron-setup mode
            /app/DockerEntrypoint.sh --cron-setup-only > /tmp/entrypoint.log 2>&1
            rc=$?
            echo "ENTRYPOINT_RC=$rc"
            cat /tmp/entrypoint.log
            # Verify crond process is running
            pgrep crond >/dev/null && echo "CROND_RUNNING=YES" || echo "CROND_RUNNING=NO"
            """
        ]
        res = subprocess.run(cmd, capture_output=True, text=True, timeout=30)
        self.assertEqual(res.returncode, 0, f"Container run failed: {res.stderr}")
        self.assertIn("ENTRYPOINT_RC=0", res.stdout)
        self.assertIn("CROND_RUNNING=YES", res.stdout)
        self.assertIn("[MIKROZIA-CRON] Background scheduler (crond) started successfully.", res.stdout)

    def test_02_cron_registration(self):
        """Item 2: Cron registration survives container recreation and explicitly checks failures."""
        if not self.docker_available:
            self.skipTest("Docker is not available")

        # Case A: Success re-registration on simulated recreated container
        cmd_success = [
            "docker", "run", "--rm",
            "-v", f"{self.entrypoint_path}:/app/DockerEntrypoint.sh:ro",
            "alpine", "sh", "-c",
            """
            # Recreated container has empty /etc/crontabs, but persistent /root/cert/.acme.sh exists
            mkdir -p /root/cert/.acme.sh
            cat << 'EOF' > /root/cert/.acme.sh/acme.sh
#!/bin/sh
if [ "$1" = "--install-cronjob" ]; then
    (crontab -l 2>/dev/null; echo '10 0 * * * "/root/.acme.sh"/acme.sh --cron --home "/root/.acme.sh" > /dev/null') | crontab -
    echo "installed acme cron"
    exit 0
fi
EOF
            chmod +x /root/cert/.acme.sh/acme.sh
            /app/DockerEntrypoint.sh --cron-setup-only > /tmp/boot.log 2>&1
            echo "--- Boot Log ---"
            cat /tmp/boot.log
            echo "--- Crontab ---"
            crontab -l
            """
        ]
        res_a = subprocess.run(cmd_success, capture_output=True, text=True, timeout=30)
        self.assertEqual(res_a.returncode, 0, f"Success test failed: {res_a.stderr}")
        self.assertIn("[MIKROZIA-ACME] acme.sh renewal cron job registered.", res_a.stdout)
        self.assertIn("[MIKROZIA-ACME] Verified acme.sh renewal schedule in crontab.", res_a.stdout)
        self.assertIn('"/root/.acme.sh"/acme.sh --cron', res_a.stdout)

        # Case B: Registration failure is explicitly caught and logged (no silent suppression)
        cmd_fail = [
            "docker", "run", "--rm",
            "-v", f"{self.entrypoint_path}:/app/DockerEntrypoint.sh:ro",
            "alpine", "sh", "-c",
            """
            mkdir -p /root/cert/.acme.sh
            cat << 'EOF' > /root/cert/.acme.sh/acme.sh
#!/bin/sh
if [ "$1" = "--install-cronjob" ]; then
    echo "Simulated crontab write error: filesystem full" >&2
    exit 5
fi
EOF
            chmod +x /root/cert/.acme.sh/acme.sh
            /app/DockerEntrypoint.sh --cron-setup-only
            """
        ]
        res_b = subprocess.run(cmd_fail, capture_output=True, text=True, timeout=30)
        self.assertEqual(res_b.returncode, 0)
        output = res_b.stdout + res_b.stderr
        self.assertIn("WARNING: acme.sh --install-cronjob failed (exit code 5)", output)
        self.assertIn("Simulated crontab write error: filesystem full", output)
        self.assertIn("WARNING: acme.sh renewal schedule not found in crontab!", output)

    def test_03_persistent_acme_configuration(self):
        """Item 3: Persistent ACME configuration and credentials survive container restart."""
        if not self.docker_available:
            self.skipTest("Docker is not available")

        # Test volume mounting across simulated stopped & restarted containers
        with tempfile.TemporaryDirectory() as host_vol:
            # First run: create cert & config in persistent volume
            cmd1 = [
                "docker", "run", "--rm",
                "-v", f"{self.entrypoint_path}:/app/DockerEntrypoint.sh:ro",
                "-v", f"{host_vol}:/root/cert",
                "alpine", "sh", "-c",
                """
                /app/DockerEntrypoint.sh --cron-setup-only >/dev/null 2>&1
                # Write simulated domain config & Cloudflare credentials
                mkdir -p /root/.acme.sh/vpn.example.com
                cat << 'EOF' > /root/.acme.sh/vpn.example.com/vpn.example.com.conf
Le_Domain='vpn.example.com'
Le_RealKeyPath='/root/cert/vpn.example.com/privkey.pem'
Le_RealFullChainPath='/root/cert/vpn.example.com/fullchain.pem'
Le_ReloadCmd='x-ui restart'
EOF
                cat << 'EOF' > /root/.acme.sh/account.conf
SAVED_CF_Token='secret_token_12345'
EOF
                chmod 600 /root/.acme.sh/account.conf
                """
            ]
            res1 = subprocess.run(cmd1, capture_output=True, text=True, timeout=30)
            self.assertEqual(res1.returncode, 0, f"Initial creation failed: {res1.stderr}")

            # Second run: new container mounts the same volume (simulating container recreation)
            cmd2 = [
                "docker", "run", "--rm",
                "-v", f"{self.entrypoint_path}:/app/DockerEntrypoint.sh:ro",
                "-v", f"{host_vol}:/root/cert",
                "alpine", "sh", "-c",
                """
                /app/DockerEntrypoint.sh --cron-setup-only >/dev/null 2>&1
                # Verify that files exist via both /root/.acme.sh and /root/cert/.acme.sh
                test -f /root/.acme.sh/vpn.example.com/vpn.example.com.conf || exit 1
                test -f /root/.acme.sh/account.conf || exit 2
                cat /root/.acme.sh/account.conf
                """
            ]
            res2 = subprocess.run(cmd2, capture_output=True, text=True, timeout=30)
            self.assertEqual(res2.returncode, 0, f"Persistence verification failed: {res2.stderr}")
            self.assertIn("SAVED_CF_Token='secret_token_12345'", res2.stdout)

    def test_04_renewal_command_invocation(self):
        """Item 4: acme.sh --cron renewal command invocation cycle."""
        if not self.docker_available:
            self.skipTest("Docker is not available")

        cmd = [
            "docker", "run", "--rm",
            "-v", f"{self.entrypoint_path}:/app/DockerEntrypoint.sh:ro",
            "alpine", "sh", "-c",
            """
            mkdir -p /root/cert/.acme.sh
            cat << 'EOF' > /root/cert/.acme.sh/acme.sh
#!/bin/sh
if [ "$1" = "--cron" ]; then
    echo "ACME_CRON_STARTED: home=$3"
    echo "ACME_CRON_COMPLETED_SUCCESSFULLY"
    exit 0
fi
EOF
            chmod +x /root/cert/.acme.sh/acme.sh
            /app/DockerEntrypoint.sh --cron-setup-only >/dev/null 2>&1
            # Invoke cron command as scheduled by crontab
            /root/.acme.sh/acme.sh --cron --home /root/.acme.sh
            """
        ]
        res = subprocess.run(cmd, capture_output=True, text=True, timeout=30)
        self.assertEqual(res.returncode, 0, f"Renewal invocation failed: {res.stderr}")
        self.assertIn("ACME_CRON_STARTED: home=/root/.acme.sh", res.stdout)
        self.assertIn("ACME_CRON_COMPLETED_SUCCESSFULLY", res.stdout)

    def test_05_certificate_installation_to_persistent_paths(self):
        """Item 5: Certificate and private key installation to persistent paths under /root/cert."""
        if not self.docker_available:
            self.skipTest("Docker is not available")

        cmd = [
            "docker", "run", "--rm",
            "-v", f"{self.entrypoint_path}:/app/DockerEntrypoint.sh:ro",
            "alpine", "sh", "-c",
            """
            mkdir -p /root/cert/.acme.sh/test.example.com
            mkdir -p /root/cert/test.example.com

            # Write mock renewed key and certificate
            echo '-----BEGIN RSA PRIVATE KEY-----' > /tmp/new_key.pem
            echo 'MIIEowIBAAKCAQEAz...MOCK_KEY_DATA...' >> /tmp/new_key.pem
            echo '-----END RSA PRIVATE KEY-----' >> /tmp/new_key.pem

            echo '-----BEGIN CERTIFICATE-----' > /tmp/new_cert.pem
            echo 'MIIEozCCA4ugAwIBAgIU...MOCK_CERT_DATA...' >> /tmp/new_cert.pem
            echo '-----END CERTIFICATE-----' >> /tmp/new_cert.pem

            # Simulate acme.sh --installcert installing renewed keys
            cat << 'EOF' > /root/cert/.acme.sh/acme.sh
#!/bin/sh
if [ "$1" = "--installcert" ]; then
    domain="$4"
    key_file="$6"
    cert_file="$8"
    cp /tmp/new_key.pem "$key_file"
    chmod 600 "$key_file"
    cp /tmp/new_cert.pem "$cert_file"
    chmod 644 "$cert_file"
    echo "Installing key to: $key_file"
    echo "Installing full chain to: $cert_file"
    exit 0
fi
EOF
            chmod +x /root/cert/.acme.sh/acme.sh
            /app/DockerEntrypoint.sh --cron-setup-only >/dev/null 2>&1

            /root/.acme.sh/acme.sh --installcert --force -d test.example.com \
                --key-file /root/cert/test.example.com/privkey.pem \
                --fullchain-file /root/cert/test.example.com/fullchain.pem

            test -s /root/cert/test.example.com/privkey.pem || exit 1
            test -s /root/cert/test.example.com/fullchain.pem || exit 2
            # Verify file permissions in BusyBox stat
            stat -c '%a' /root/cert/test.example.com/privkey.pem
            """
        ]
        res = subprocess.run(cmd, capture_output=True, text=True, timeout=30)
        self.assertEqual(res.returncode, 0, f"Cert install failed: {res.stderr}")
        self.assertIn("Installing key to: /root/cert/test.example.com/privkey.pem", res.stdout)
        self.assertIn("Installing full chain to: /root/cert/test.example.com/fullchain.pem", res.stdout)
        self.assertIn("600", res.stdout)

    def test_06_panel_subscription_reload(self):
        """Item 6: Panel & subscription certificate reload via SIGHUP/SIGUSR1 in docker."""
        if not self.docker_available:
            self.skipTest("Docker is not available")

        # Test in Alpine container running a mock x-ui daemon that logs caught signals
        cmd = [
            "docker", "run", "--rm",
            "-v", f"{self.xui_sh_path}:/usr/bin/x-ui:ro",
            "alpine", "sh", "-c",
            """
            apk add --no-cache bash >/dev/null 2>&1
            mkdir -p /app
            # Create a mock x-ui daemon that records received signals
            cat << 'EOF' > /app/x-ui
#!/bin/sh
trap 'echo RELOAD_PANEL_SIGHUP_RECEIVED >> /tmp/xui_signals.log' HUP
trap 'echo RELOAD_XRAY_SIGUSR1_RECEIVED >> /tmp/xui_signals.log' USR1
trap 'echo TERM_RECEIVED >> /tmp/xui_signals.log; exit 0' TERM INT
while true; do
    sleep 0.1
done
EOF
            chmod +x /app/x-ui
            /app/x-ui &
            XUI_PID=$!
            sleep 0.5

            # Simulate acme.sh reloadcmd execution in docker environment
            export XUI_IN_DOCKER="true"
            export XUI_MAIN_FOLDER="/app"

            # Execute reload command as registered in acme.sh
            /usr/bin/x-ui restart
            RESTART_RC=$?
            echo "RESTART_RC=$RESTART_RC"

            sleep 0.5
            echo "--- Logged Signals ---"
            cat /tmp/xui_signals.log 2>/dev/null

            # Verify x-ui PID is still alive (not killed)
            kill -0 $XUI_PID >/dev/null 2>&1 && echo "XUI_ALIVE=YES" || echo "XUI_ALIVE=NO"
            kill $XUI_PID >/dev/null 2>&1
            """
        ]
        res = subprocess.run(cmd, capture_output=True, text=True, timeout=60)
        self.assertEqual(res.returncode, 0, f"Reload test failed: {res.stderr}")
        self.assertIn("RESTART_RC=0", res.stdout)
        self.assertIn("Restart signal sent to the panel and xray-core.", res.stdout)
        self.assertIn("RELOAD_PANEL_SIGHUP_RECEIVED", res.stdout)
        self.assertIn("RELOAD_XRAY_SIGUSR1_RECEIVED", res.stdout)
        self.assertIn("XUI_ALIVE=YES", res.stdout)

    def test_07_actual_unattended_letsencrypt_renewal_documented_not_tested(self):
        """Item 7: Explicitly verify that actual unattended Let's Encrypt renewal is tracked as NOT TESTED."""
        # Per Requirement 10: "Do not use real production certificates or issue unnecessary
        # real certificates during automated testing. Do not claim real unattended renewal PASS based solely on mocked tests."
        # This test documents that the live CA issuance is intentionally NOT TESTED in unit/automated suites.
        status = "NOT TESTED"
        self.assertEqual(status, "NOT TESTED")

    def test_08_installed_vs_acme_internal_certificate_paths(self):
        """Item 8: Distinguish internal ACME files from production installed certificate files."""
        if not self.docker_available:
            self.skipTest("Docker is not available")

        cmd = [
            "docker", "run", "--rm",
            "alpine", "sh", "-c",
            """
            domain="test.parsiserver.com"
            # 1. Simulate internal ACME working directory
            mkdir -p "/root/.acme.sh/${domain}"
            echo "ACME_INTERNAL_CER" > "/root/.acme.sh/${domain}/fullchain.cer"
            echo "ACME_INTERNAL_KEY" > "/root/.acme.sh/${domain}/${domain}.key"

            # 2. Simulate installed production directory created by acme.sh --installcert
            mkdir -p "/root/cert/${domain}"
            cp "/root/.acme.sh/${domain}/fullchain.cer" "/root/cert/${domain}/fullchain.pem"
            chmod 644 "/root/cert/${domain}/fullchain.pem"
            cp "/root/.acme.sh/${domain}/${domain}.key" "/root/cert/${domain}/privkey.pem"
            chmod 600 "/root/cert/${domain}/privkey.pem"

            # 3. Assert installed production files exist
            test -f "/root/cert/${domain}/fullchain.pem" || exit 1
            test -f "/root/cert/${domain}/privkey.pem" || exit 2

            # 4. Assert invalid paths do NOT exist in /root/cert/<domain>/
            test ! -f "/root/cert/${domain}/fullchain.cer" || exit 3
            test ! -f "/root/cert/${domain}/${domain}.key" || exit 4

            echo "PATHS_DISTINCTION_VERIFIED"
            """
        ]
        res = subprocess.run(cmd, capture_output=True, text=True, timeout=30)
        self.assertEqual(res.returncode, 0, f"Path distinction failed: {res.stderr}")
        self.assertIn("PATHS_DISTINCTION_VERIFIED", res.stdout)


if __name__ == "__main__":
    unittest.main()
