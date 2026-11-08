#!/usr/bin/env python3
"""Tests for firewall-free MikroTik container deployment assets."""

import copy
import json
import os
import subprocess
import sys
import tempfile
import unittest

from scripts.generator.mikrozia_generator import (
    DEFAULT_IMAGE,
    PreflightValidationError,
    PreflightValidator,
    RouterOSScriptGenerator,
)


BASE_PREFLIGHT = {
    "schema_version": "1.0.0",
    "system": {
        "version": "7.25.1 (stable)",
        "architecture": "x86_64",
        "board_name": "CHR",
        "total_memory_bytes": 4 * 1024 * 1024 * 1024,
        "free_memory_bytes": 3 * 1024 * 1024 * 1024,
        "total_hdd_space_bytes": 16 * 1024 * 1024 * 1024,
        "free_hdd_space_bytes": 12 * 1024 * 1024 * 1024,
    },
    "subsystems": {
        "container_package_installed": True,
        "container_package_enabled": True,
        "container_package_version": "7.25.1",
        "device_mode_container_enabled": True,
    },
    "storage": [],
    "network": {
        "interfaces": [
            {"name": "ether1", "type": "ether", "disabled": False},
            {"name": "bridge-lan", "type": "bridge", "disabled": False},
        ],
        "interface_lists": {"WAN": ["ether1"], "LAN": ["bridge-lan"]},
        "ip_addresses": [
            {"address": "203.0.113.10/24", "network": "203.0.113.0", "interface": "ether1", "disabled": False},
            {"address": "192.168.88.1/24", "network": "192.168.88.0", "interface": "bridge-lan", "disabled": False},
        ],
        "ip_routes": [{"dst_address": "0.0.0.0/0", "disabled": False}],
        "bridges": [{"name": "bridge-lan", "comment": "LAN", "disabled": False}],
        "veths": [],
    },
    "existing_containers": {"containers": []},
}


def validated(options=None, data=None):
    opts = {"target_storage": "containers", **(options or {})}
    payload = copy.deepcopy(data or BASE_PREFLIGHT)
    result = PreflightValidator(payload, opts).validate_all()
    return opts, result


class TestMikroziaGenerator(unittest.TestCase):
    def test_valid_chr_platform(self):
        _, result = validated()
        self.assertEqual(result["platform"]["image_arch"], "amd64")
        self.assertTrue(result["platform"]["is_chr"])

    def test_routeros_baseline(self):
        data = copy.deepcopy(BASE_PREFLIGHT)
        data["system"]["version"] = "7.24.6 (stable)"
        data["subsystems"]["container_package_version"] = "7.24.6"
        with self.assertRaises(PreflightValidationError) as ctx:
            validated(data=data)
        self.assertIn("below the supported baseline", str(ctx.exception))

    def test_prerelease_routeros_rejected(self):
        data = copy.deepcopy(BASE_PREFLIGHT)
        data["system"]["version"] = "7.26rc1 (testing)"
        data["subsystems"]["container_package_version"] = "7.26rc1"
        with self.assertRaises(PreflightValidationError) as ctx:
            validated(data=data)
        self.assertIn("stable RouterOS", str(ctx.exception))

    def test_container_package_must_match(self):
        data = copy.deepcopy(BASE_PREFLIGHT)
        data["subsystems"]["container_package_version"] = "7.25"
        with self.assertRaises(PreflightValidationError) as ctx:
            validated(data=data)
        self.assertIn("does not match", str(ctx.exception))

    def test_device_mode_required(self):
        data = copy.deepcopy(BASE_PREFLIGHT)
        data["subsystems"]["device_mode_container_enabled"] = False
        with self.assertRaises(PreflightValidationError) as ctx:
            validated(data=data)
        self.assertIn("Device-mode", str(ctx.exception))

    def test_chr_ram_floor(self):
        data = copy.deepcopy(BASE_PREFLIGHT)
        data["system"]["total_memory_bytes"] = 1024 * 1024 * 1024
        with self.assertRaises(PreflightValidationError) as ctx:
            validated(data=data)
        self.assertIn("Insufficient RAM", str(ctx.exception))

    def test_chr_free_space_floor(self):
        data = copy.deepcopy(BASE_PREFLIGHT)
        data["system"]["free_hdd_space_bytes"] = 1024 * 1024 * 1024
        with self.assertRaises(PreflightValidationError) as ctx:
            validated(data=data)
        self.assertIn("CHR disk", str(ctx.exception))

    def test_subnet_overlap_rejected(self):
        data = copy.deepcopy(BASE_PREFLIGHT)
        data["network"]["ip_addresses"].append(
            {"address": "172.16.10.1/24", "interface": "other", "disabled": False}
        )
        with self.assertRaises(PreflightValidationError) as ctx:
            validated(data=data)
        self.assertIn("overlaps address", str(ctx.exception))

    def test_route_overlap_rejected(self):
        data = copy.deepcopy(BASE_PREFLIGHT)
        data["network"]["ip_routes"].append(
            {"dst_address": "172.16.0.0/16", "disabled": False}
        )
        with self.assertRaises(PreflightValidationError) as ctx:
            validated(data=data)
        self.assertIn("overlaps route", str(ctx.exception))

    def test_unowned_bridge_rejected(self):
        data = copy.deepcopy(BASE_PREFLIGHT)
        data["network"]["interfaces"].append(
            {"name": "br-mikrozia", "type": "bridge", "disabled": False}
        )
        data["network"]["bridges"].append(
            {"name": "br-mikrozia", "comment": "someone else", "disabled": False}
        )
        with self.assertRaises(PreflightValidationError) as ctx:
            validated(data=data)
        self.assertIn("not owned", str(ctx.exception))

    def test_custom_network_values(self):
        opts, result = validated({
            "container_subnet": "10.77.8.0/24",
            "gateway_ip": "10.77.8.10",
            "container_ip": "10.77.8.20",
        })
        deploy = RouterOSScriptGenerator(result, opts).generate_deploy_script()
        self.assertIn('address="10.77.8.10/24"', deploy)
        self.assertIn('address="10.77.8.20/24"', deploy)

    def test_deploy_is_firewall_free(self):
        opts, result = validated()
        deploy = RouterOSScriptGenerator(result, opts).generate_deploy_script()
        self.assertNotIn("/ip/firewall", deploy)
        self.assertNotIn("dstnat", deploy.lower())
        self.assertNotIn("srcnat", deploy.lower())
        self.assertNotIn("masquerade", deploy.lower())

    def test_uninstall_is_firewall_free(self):
        opts, result = validated()
        uninstall = RouterOSScriptGenerator(result, opts).generate_uninstall_script()
        self.assertNotIn("/ip/firewall", uninstall)
        self.assertNotIn("/file/remove", uninstall)
        self.assertIn("Preserved data", uninstall)

    def test_routeros_runtime_contract(self):
        opts, result = validated()
        deploy = RouterOSScriptGenerator(result, opts).generate_deploy_script()
        self.assertIn(f'remote-image="{DEFAULT_IMAGE}"', deploy)
        self.assertIn('dst="/etc/x-ui"', deploy)
        self.assertIn('dst="/root/cert"', deploy)
        self.assertNotIn("XUI_ENABLE_FAIL2BAN", deploy)
        self.assertIn('key="HOME" value="/root"', deploy)
        self.assertIn("memory-high=512M memory-max=1024M", deploy)
        self.assertIn("auto-restart-interval=10s", deploy)
        self.assertIn("start-on-boot=yes", deploy)

    def test_versioned_root_and_stable_data_paths(self):
        opts, result = validated()
        deploy = RouterOSScriptGenerator(result, opts).generate_deploy_script()
        self.assertIn('root-dir="containers/mikrozia/root-v3.11.0-mikrozia.4"', deploy)
        self.assertIn('src="containers/mikrozia/data"', deploy)
        self.assertIn('src="containers/mikrozia/cert"', deploy)

    def test_offline_image_is_optional(self):
        options = {
            "target_storage": "containers",
            "image_source": "offline_file",
            "image_reference": "containers/mikrozia-v3.11.0.tar",
        }
        result = PreflightValidator(copy.deepcopy(BASE_PREFLIGHT), options).validate_all()
        deploy = RouterOSScriptGenerator(result, options).generate_deploy_script()
        self.assertIn('file="containers/mikrozia-v3.11.0.tar"', deploy)

    def test_cli_dry_run_and_write(self):
        repo = os.path.abspath(os.path.join(os.path.dirname(__file__), ".."))
        script = os.path.join(repo, "scripts", "generator", "mikrozia_generator.py")
        with tempfile.TemporaryDirectory() as temp_dir:
            preflight_path = os.path.join(temp_dir, "preflight.json")
            with open(preflight_path, "w", encoding="utf-8") as handle:
                json.dump(BASE_PREFLIGHT, handle)
            command = [
                sys.executable,
                script,
                "--preflight", preflight_path,
                "--target-storage", "containers",
                "--output-dir", temp_dir,
            ]
            dry = subprocess.run(command, capture_output=True, text=True, check=False)
            self.assertEqual(dry.returncode, 0, dry.stderr)
            self.assertIn("Firewall:  untouched", dry.stdout)
            self.assertFalse(os.path.exists(os.path.join(temp_dir, "mikrozia-deploy.rsc")))

            write = subprocess.run(command + ["--write"], capture_output=True, text=True, check=False)
            self.assertEqual(write.returncode, 0, write.stderr)
            with open(os.path.join(temp_dir, "mikrozia-deploy.rsc"), encoding="utf-8") as handle:
                generated = handle.read()
            self.assertNotIn("/ip/firewall", generated)

    def test_static_routeros_assets_are_firewall_free(self):
        repo = os.path.abspath(os.path.join(os.path.dirname(__file__), ".."))
        for name in (
            "mikrozia-clean-room-deploy.rsc",
            "mikrozia-clean-room-teardown.rsc",
            "mikrozia-preflight.rsc",
        ):
            path = os.path.join(repo, "scripts", "routeros", name)
            with open(path, encoding="utf-8") as handle:
                content = handle.read()
            self.assertNotIn("/ip/firewall", content, name)

    def test_publish_workflow_targets_mikrozia(self):
        repo = os.path.abspath(os.path.join(os.path.dirname(__file__), ".."))
        workflow = os.path.join(repo, ".github", "workflows", "docker.yml")
        with open(workflow, encoding="utf-8") as handle:
            content = handle.read()
        self.assertIn("IMAGE_NAME: majlot/mikrozia", content)
        self.assertIn('"v*.*.*-mikrozia.*"', content)
        self.assertIn("linux/amd64,linux/arm64/v8,linux/arm/v7", content)
        self.assertIn("DOCKER_HUB_TOKEN", content)
        self.assertIn("Assert firewall-free RouterOS installers", content)

    def test_dockerfile_declares_persistent_contract(self):
        repo = os.path.abspath(os.path.join(os.path.dirname(__file__), ".."))
        with open(os.path.join(repo, "Dockerfile"), encoding="utf-8") as handle:
            content = handle.read()
        self.assertNotIn("fail2ban", content.lower())
        self.assertIn('VOLUME [ "/etc/x-ui", "/root/cert" ]', content)
        self.assertIn("ARG MIKROZIA_VERSION=dev", content)
        self.assertIn("EXPOSE 2053 2096", content)


if __name__ == "__main__":
    unittest.main()
