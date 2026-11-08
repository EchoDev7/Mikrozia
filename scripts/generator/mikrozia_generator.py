#!/usr/bin/env python3
"""Generate container-only RouterOS deployment and rollback scripts.

Mikrozia deliberately does not generate, inspect, reorder, or remove RouterOS
firewall/NAT rules. The generator is limited to compatibility checks, isolated
VETH networking, persistent storage, environment variables, and the container
object itself.
"""

import argparse
import ipaddress
import json
import os
import re
import sys
from typing import Any, Dict, List, Optional


GENERATOR_VERSION = "2.0.0"
DEFAULT_IMAGE = "majlot/mikrozia:v3.11.0-mikrozia.4"
MIN_ROUTEROS = (7, 25, 0)
MIN_CHR_RAM = 2 * 1024 * 1024 * 1024
MIN_FREE_STORAGE = 2 * 1024 * 1024 * 1024


class PreflightValidationError(Exception):
    """Raised when the target cannot safely run the generated deployment."""


class PreflightValidator:
    def __init__(self, preflight_data: Dict[str, Any], operator_options: Dict[str, Any]):
        self.raw = preflight_data
        self.opts = operator_options
        self.errors: List[str] = []
        self.warnings: List[str] = []

    def validate_all(self) -> Dict[str, Any]:
        self._validate_schema()
        self._validate_options()
        platform = self._validate_platform()
        storage = self._validate_storage(platform)
        network = self._validate_network()
        container = self._validate_existing_container(storage)

        if self.errors:
            details = "\n".join(f"  - {item}" for item in self.errors)
            raise PreflightValidationError(
                f"Preflight validation failed with {len(self.errors)} error(s):\n{details}"
            )

        return {
            "platform": platform,
            "storage": storage,
            "network": network,
            "container": container,
            "warnings": self.warnings,
        }

    def _validate_schema(self) -> None:
        if not isinstance(self.raw, dict):
            self.errors.append("Preflight payload must be a JSON object.")
            return
        if not self.raw.get("schema_version"):
            self.errors.append("Preflight payload is missing schema_version.")
        for key in ("system", "subsystems", "storage", "network"):
            if key not in self.raw:
                self.errors.append(f"Preflight payload is missing '{key}'.")

    def _validate_options(self) -> None:
        instance_id = str(self.opts.get("instance_id", "main"))
        if not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9_-]{0,31}", instance_id):
            self.errors.append(
                "instance-id must be 1-32 ASCII letters, digits, '_' or '-', beginning with a letter or digit."
            )

        storage = str(self.opts.get("target_storage", "")).strip("/ ")
        if (
            not storage
            or not re.fullmatch(r"[A-Za-z0-9._/-]+", storage)
            or any(part in ("", ".", "..") for part in storage.split("/"))
        ):
            self.errors.append("target-storage must be a safe relative RouterOS path; '..' is forbidden.")

        for field, default in (("bridge_name", "br-mikrozia"), ("veth_name", "veth-mikrozia")):
            value = str(self.opts.get(field, default))
            if not re.fullmatch(r"[A-Za-z0-9_.-]{1,63}", value):
                self.errors.append(f"Invalid {field.replace('_', '-')}: '{value}'.")

        image_reference = self.opts.get("image_reference")
        if image_reference and not re.fullmatch(r"[A-Za-z0-9._/@:+-]+", str(image_reference)):
            self.errors.append("image-reference contains unsupported or unsafe characters.")

        for field, default in (("memory_high", "512M"), ("memory_max", "1024M")):
            value = str(self.opts.get(field, default))
            if not re.fullmatch(r"[1-9][0-9]*[KMG]", value):
                self.errors.append(f"{field.replace('_', '-')} must look like 512M or 1G.")

        panel_port = self.opts.get("panel_port", 2053)
        try:
            panel_port_num = int(panel_port)
            if not 1 <= panel_port_num <= 65535:
                raise ValueError
        except (TypeError, ValueError):
            self.errors.append("panel-port must be between 1 and 65535.")

    @staticmethod
    def _version_tuple(version: str) -> tuple:
        parts = re.findall(r"\d+", version)
        values = list(map(int, parts[:3]))
        return tuple((values + [0, 0, 0])[:3])

    def _validate_platform(self) -> Dict[str, Any]:
        system = self.raw.get("system", {})
        subsystems = self.raw.get("subsystems", {})
        version = str(system.get("version", ""))
        arch = str(system.get("architecture", "")).lower()
        board = str(system.get("board_name", ""))
        total_ram = int(system.get("total_memory_bytes", 0) or 0)
        version_tuple = self._version_tuple(version)

        if version_tuple < MIN_ROUTEROS:
            self.errors.append(f"RouterOS {version or '<unknown>'} is below the supported baseline 7.25.0.")
        if any(marker in version.lower() for marker in ("beta", "rc", "testing", "development")):
            self.errors.append("A stable RouterOS release is required for production deployment.")

        architecture_map = {
            "x86_64": "amd64",
            "x86": "amd64",
            "arm64": "arm64",
            "aarch64": "arm64",
            "arm": "armv7",
            "armhf": "armv7",
        }
        image_arch = architecture_map.get(arch)
        if image_arch is None:
            self.errors.append(f"Unsupported RouterOS architecture: '{arch}'.")
            image_arch = "unknown"

        is_chr = "chr" in board.lower()
        min_ram = MIN_CHR_RAM if is_chr else 1024 * 1024 * 1024
        if total_ram < min_ram:
            self.errors.append(
                f"Insufficient RAM: {total_ram / (1024 * 1024):.0f} MiB detected; "
                f"{min_ram / (1024 * 1024):.0f} MiB required."
            )

        package_installed = bool(subsystems.get("container_package_installed", False))
        package_enabled = bool(subsystems.get("container_package_enabled", False))
        package_version = str(subsystems.get("container_package_version", ""))
        if not package_installed:
            self.errors.append("RouterOS container package is not installed.")
        elif not package_enabled:
            self.errors.append("RouterOS container package is disabled.")
        if package_installed:
            if not package_version:
                self.errors.append("Container package version is missing; exact matching cannot be verified.")
            elif self._version_tuple(package_version) != version_tuple:
                self.errors.append(
                    f"Container package {package_version} does not match RouterOS {version}."
                )

        if not bool(subsystems.get("device_mode_container_enabled", False)):
            self.errors.append("Device-mode container support is not enabled.")

        return {
            "routeros_version": version,
            "architecture": arch,
            "image_arch": image_arch,
            "board_name": board,
            "is_chr": is_chr,
            "total_ram": total_ram,
        }

    def _validate_storage(self, platform: Dict[str, Any]) -> Dict[str, Any]:
        target = str(self.opts.get("target_storage", "")).strip("/ ")
        disks = self.raw.get("storage", [])
        matched = next(
            (disk for disk in disks if str(disk.get("name", "")).strip("/ ") == target),
            None,
        )

        if matched:
            disk_type = str(matched.get("type", "")).lower()
            free_bytes = int(matched.get("free_bytes", 0) or 0)
            if not platform.get("is_chr") and ("nand" in disk_type or "nand" in target.lower()):
                self.errors.append("Internal NAND is not supported for container storage.")
            if free_bytes and free_bytes < MIN_FREE_STORAGE:
                self.errors.append(
                    f"Storage '{target}' has only {free_bytes / (1024 ** 3):.2f} GiB free; 2 GiB is required."
                )
        else:
            system_free = int(self.raw.get("system", {}).get("free_hdd_space_bytes", 0) or 0)
            if platform.get("is_chr"):
                if system_free and system_free < MIN_FREE_STORAGE:
                    self.errors.append(
                        f"CHR disk has only {system_free / (1024 ** 3):.2f} GiB free; 2 GiB is required."
                    )
                elif not system_free:
                    self.warnings.append("CHR free disk space could not be verified from preflight data.")
            else:
                self.errors.append(f"Storage '{target}' was not found in the RouterOS disk inventory.")

        return {"target": target, "matched_disk": matched}

    def _validate_network(self) -> Dict[str, Any]:
        network_data = self.raw.get("network", {})
        subnet_text = str(self.opts.get("container_subnet", "172.16.10.0/24"))
        try:
            subnet = ipaddress.ip_network(subnet_text, strict=True)
            if subnet.version != 4:
                raise ValueError("only IPv4 container subnets are supported")
            if not 16 <= subnet.prefixlen <= 30:
                raise ValueError("prefix must be between /16 and /30")
        except ValueError as exc:
            self.errors.append(f"Invalid container subnet '{subnet_text}': {exc}.")
            subnet = ipaddress.ip_network("172.16.10.0/24")

        hosts = subnet.hosts()
        default_gateway = next(hosts)
        default_container = next(hosts)

        def select_address(option: str, default: ipaddress.IPv4Address, label: str) -> str:
            raw_value = self.opts.get(option)
            if raw_value is None:
                return str(default)
            try:
                value = ipaddress.ip_address(str(raw_value))
                if value.version != 4 or value not in subnet:
                    raise ValueError
                if value in (subnet.network_address, subnet.broadcast_address):
                    raise ValueError
                return str(value)
            except ValueError:
                self.errors.append(f"{label} '{raw_value}' is not a usable host in {subnet}.")
                return str(default)

        gateway_ip = select_address("gateway_ip", default_gateway, "Gateway IP")
        container_ip = select_address("container_ip", default_container, "Container IP")
        if gateway_ip == container_ip:
            self.errors.append("Gateway IP and container IP cannot be identical.")

        bridge_name = str(self.opts.get("bridge_name", "br-mikrozia"))
        veth_name = str(self.opts.get("veth_name", "veth-mikrozia"))
        instance_id = str(self.opts.get("instance_id", "main"))
        tag = f"[Mikrozia:{instance_id}]"

        bridges = network_data.get("bridges", [])
        veths = network_data.get("veths", [])
        interfaces = network_data.get("interfaces", [])
        interface_names = {str(item.get("name", "")) for item in interfaces}

        if bridge_name in interface_names:
            bridge = next((item for item in bridges if item.get("name") == bridge_name), None)
            if not bridge:
                self.errors.append(f"'{bridge_name}' exists but is not a bridge.")
            elif tag not in str(bridge.get("comment", "")):
                self.errors.append(f"Bridge '{bridge_name}' is not owned by {tag}.")
        if veth_name in interface_names:
            veth = next((item for item in veths if item.get("name") == veth_name), None)
            if not veth:
                self.errors.append(f"'{veth_name}' exists but is not a VETH.")
            elif tag not in str(veth.get("comment", "")):
                self.errors.append(f"VETH '{veth_name}' is not owned by {tag}.")

        for entry in network_data.get("ip_addresses", []):
            if entry.get("disabled", False):
                continue
            if entry.get("interface") == bridge_name:
                continue
            try:
                existing = ipaddress.ip_network(str(entry.get("address", "")), strict=False)
                if subnet.overlaps(existing):
                    self.errors.append(
                        f"Container subnet {subnet} overlaps address {entry.get('address')} on {entry.get('interface')}."
                    )
            except ValueError:
                continue

        for entry in network_data.get("ip_routes", []):
            if entry.get("disabled", False):
                continue
            destination = str(entry.get("dst_address", ""))
            if destination in ("", "0.0.0.0/0"):
                continue
            try:
                existing = ipaddress.ip_network(destination, strict=False)
                if subnet.overlaps(existing):
                    self.errors.append(f"Container subnet {subnet} overlaps route {destination}.")
            except ValueError:
                continue

        dns_values = [
            item.strip()
            for item in re.split(r"[,\s]+", str(self.opts.get("dns_servers", "1.1.1.1,8.8.8.8")))
            if item.strip()
        ]
        validated_dns: List[str] = []
        for value in dns_values:
            try:
                address = ipaddress.ip_address(value)
                if address.version != 4:
                    raise ValueError
                validated_dns.append(str(address))
            except ValueError:
                self.errors.append(f"Invalid IPv4 DNS server: '{value}'.")
        if not validated_dns:
            self.errors.append("At least one IPv4 DNS server is required.")

        return {
            "subnet": subnet,
            "gateway_ip": gateway_ip,
            "container_ip": container_ip,
            "bridge_name": bridge_name,
            "veth_name": veth_name,
            "dns_servers": validated_dns,
        }

    def _validate_existing_container(self, storage: Dict[str, Any]) -> Dict[str, Any]:
        instance_id = str(self.opts.get("instance_id", "main"))
        tag = f"[Mikrozia:{instance_id}]"
        root_dir = self._root_dir(storage["target"])
        containers = self.raw.get("existing_containers", {}).get("containers", [])
        for container in containers:
            if container.get("root_dir") == root_dir and tag not in str(container.get("comment", "")):
                self.errors.append(f"Container root-dir '{root_dir}' is already used by another container.")
        return {"root_dir": root_dir, "count": len(containers)}

    def _root_dir(self, storage: str) -> str:
        image_reference = str(self.opts.get("image_reference") or DEFAULT_IMAGE)
        image_token = image_reference.rsplit(":", 1)[-1] if ":" in image_reference else os.path.basename(image_reference)
        safe_token = re.sub(r"[^A-Za-z0-9._-]+", "-", image_token).strip("-") or "image"
        instance_id = str(self.opts.get("instance_id", "main"))
        base = "mikrozia" if instance_id == "main" else f"mikrozia-{instance_id}"
        return f"{storage}/{base}/root-{safe_token}"


class RouterOSScriptGenerator:
    def __init__(self, validation_result: Dict[str, Any], operator_options: Dict[str, Any]):
        self.result = validation_result
        self.opts = operator_options
        self.instance_id = str(self.opts.get("instance_id", "main"))
        self.tag = f"[Mikrozia:{self.instance_id}]"

    def _paths(self) -> Dict[str, str]:
        storage = self.result["storage"]["target"]
        base_name = "mikrozia" if self.instance_id == "main" else f"mikrozia-{self.instance_id}"
        base = f"{storage}/{base_name}"
        return {
            "base": base,
            "data": f"{base}/data",
            "cert": f"{base}/cert",
            "tmp": f"{base}/tmp",
            "root": self.result["container"]["root_dir"],
        }

    def generate_deploy_script(self) -> str:
        net = self.result["network"]
        platform = self.result["platform"]
        paths = self._paths()
        prefix = net["subnet"].prefixlen
        bridge = net["bridge_name"]
        veth = net["veth_name"]
        gateway = net["gateway_ip"]
        container_ip = net["container_ip"]
        dns = ",".join(net["dns_servers"])
        panel_port = int(self.opts.get("panel_port", 2053))
        memory_high = str(self.opts.get("memory_high", "512M"))
        memory_max = str(self.opts.get("memory_max", "1024M"))
        auto_restart = str(self.opts.get("auto_restart_interval", "10s"))
        data_list = f"MIKROZIA_DATA_{self.instance_id}"
        cert_list = f"MIKROZIA_CERT_{self.instance_id}"
        env_list = f"MIKROZIA_ENV_{self.instance_id}"

        image_source = str(self.opts.get("image_source", "registry"))
        image_reference = str(self.opts.get("image_reference") or DEFAULT_IMAGE)
        if image_source == "registry":
            image_parameter = f'remote-image="{image_reference}"'
        else:
            image_parameter = f'file="{image_reference}"'

        return f'''# ==============================================================================
# MIKROZIA — CONTAINER-ONLY DEPLOYMENT
# Generator: {GENERATOR_VERSION}
# RouterOS: {platform["routeros_version"]} | Image architecture: {platform["image_arch"]}
# Instance: {self.instance_id}
# ==============================================================================
# This script creates NO RouterOS firewall or NAT rules.
# Routing, egress, port forwarding, and access control remain operator-owned.
# ==============================================================================

:put ">>> Deploying Mikrozia container [{self.instance_id}] <<<"

/container/config/set registry-url="https://registry-1.docker.io" tmpdir="{paths['tmp']}"

:if ([:len [/interface/bridge/find where name="{bridge}"]] = 0) do={{
    /interface/bridge/add name="{bridge}" comment="{self.tag} Bridge"
}}
:if ([:len [/ip/address/find where address="{gateway}/{prefix}" and interface="{bridge}"]] = 0) do={{
    /ip/address/add address="{gateway}/{prefix}" interface="{bridge}" comment="{self.tag} Gateway"
}}
:if ([:len [/interface/veth/find where name="{veth}"]] = 0) do={{
    /interface/veth/add name="{veth}" address="{container_ip}/{prefix}" gateway="{gateway}" comment="{self.tag} VETH"
}}
:if ([:len [/interface/bridge/port/find where bridge="{bridge}" and interface="{veth}"]] = 0) do={{
    /interface/bridge/port/add bridge="{bridge}" interface="{veth}" comment="{self.tag} Bridge Port"
}}

:if ([:len [/container/mounts/find where list="{data_list}" and dst="/etc/x-ui"]] = 0) do={{
    /container/mounts/add list="{data_list}" src="{paths['data']}" dst="/etc/x-ui" comment="{self.tag} Data Mount"
}}
:if ([:len [/container/mounts/find where list="{cert_list}" and dst="/root/cert"]] = 0) do={{
    /container/mounts/add list="{cert_list}" src="{paths['cert']}" dst="/root/cert" comment="{self.tag} Cert Mount"
}}

:if ([:len [/container/envs/find where list="{env_list}" and key="XUI_PORT"]] = 0) do={{
    /container/envs/add list="{env_list}" key="XUI_PORT" value="{panel_port}" comment="{self.tag} Environment"
}}
:if ([:len [/container/envs/find where list="{env_list}" and key="HOME"]] = 0) do={{
    /container/envs/add list="{env_list}" key="HOME" value="/root" comment="{self.tag} Environment"
}}

:if ([:len [/container/find where comment="{self.tag} Container"]] = 0) do={{
    /container/add {image_parameter} interface="{veth}" root-dir="{paths['root']}" mountlists="{data_list},{cert_list}" envlist="{env_list}" dns="{dns}" memory-high={memory_high} memory-max={memory_max} logging=yes start-on-boot=yes auto-restart-interval={auto_restart} comment="{self.tag} Container"
}} else={{
    :put "Container already exists; import skipped."
}}

:put ">>> Container configuration complete <<<"
:put "No RouterOS firewall or NAT rules were created or changed."
:put "After extraction: /container/start [find where comment=\\\"{self.tag} Container\\\"]"
:put "Logs: /log/print follow-only where topics~\\\"container\\\""
'''

    def generate_uninstall_script(self) -> str:
        net = self.result["network"]
        paths = self._paths()
        return f'''# ==============================================================================
# MIKROZIA — CONTAINER-ONLY ROLLBACK (INSTANCE: {self.instance_id})
# ==============================================================================
# Persistent data and certificates are preserved under {paths['base']}/.
# No RouterOS firewall or NAT rules are inspected or modified.
# ==============================================================================

:do {{ /container/stop [find where comment="{self.tag} Container"]; :delay 3s }} on-error={{}}
:do {{ /container/remove [find where comment="{self.tag} Container"] }} on-error={{}}
:do {{ /container/mounts/remove [find where comment="{self.tag} Data Mount"] }} on-error={{}}
:do {{ /container/mounts/remove [find where comment="{self.tag} Cert Mount"] }} on-error={{}}
:do {{ /container/envs/remove [find where comment="{self.tag} Environment"] }} on-error={{}}
:do {{ /interface/bridge/port/remove [find where comment="{self.tag} Bridge Port"] }} on-error={{}}
:do {{ /ip/address/remove [find where comment="{self.tag} Gateway"] }} on-error={{}}
:do {{ /interface/veth/remove [find where name="{net['veth_name']}" and comment="{self.tag} VETH"] }} on-error={{}}
:do {{ /interface/bridge/remove [find where name="{net['bridge_name']}" and comment="{self.tag} Bridge"] }} on-error={{}}

:put "Mikrozia container-only rollback complete."
:put "No firewall/NAT rules were inspected or modified."
:put "Preserved data: {paths['data']} and {paths['cert']}"
'''


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Generate firewall-free Mikrozia RouterOS container scripts"
    )
    parser.add_argument("--preflight", required=True, help="Preflight JSON file")
    parser.add_argument("--target-storage", required=True, help="RouterOS storage path")
    parser.add_argument("--container-subnet", default="172.16.10.0/24")
    parser.add_argument("--gateway-ip")
    parser.add_argument("--container-ip")
    parser.add_argument("--bridge-name", default="br-mikrozia")
    parser.add_argument("--veth-name", default="veth-mikrozia")
    parser.add_argument("--dns-servers", default="1.1.1.1,8.8.8.8")
    parser.add_argument("--panel-port", type=int, default=2053)
    parser.add_argument("--memory-high", default="512M")
    parser.add_argument("--memory-max", default="1024M")
    parser.add_argument("--auto-restart-interval", default="10s")
    parser.add_argument("--instance-id", default="main")
    parser.add_argument("--image-source", choices=("registry", "offline_file"), default="registry")
    parser.add_argument("--image-reference", help=f"Default registry image: {DEFAULT_IMAGE}")
    parser.add_argument("--output-dir", default="release/scripts")
    parser.add_argument("--prefix", default="mikrozia")
    parser.add_argument("--write", action="store_true")
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    try:
        with open(args.preflight, "r", encoding="utf-8") as handle:
            preflight = json.load(handle)
    except (OSError, json.JSONDecodeError) as exc:
        print(f"[FATAL] Cannot read preflight JSON: {exc}", file=sys.stderr)
        return 1

    options = {
        "target_storage": args.target_storage,
        "container_subnet": args.container_subnet,
        "gateway_ip": args.gateway_ip,
        "container_ip": args.container_ip,
        "bridge_name": args.bridge_name,
        "veth_name": args.veth_name,
        "dns_servers": args.dns_servers,
        "panel_port": args.panel_port,
        "memory_high": args.memory_high,
        "memory_max": args.memory_max,
        "auto_restart_interval": args.auto_restart_interval,
        "instance_id": args.instance_id,
        "image_source": args.image_source,
        "image_reference": args.image_reference,
    }

    try:
        result = PreflightValidator(preflight, options).validate_all()
    except PreflightValidationError as exc:
        print(f"[REJECTED]\n{exc}", file=sys.stderr)
        return 1

    for warning in result["warnings"]:
        print(f"[WARNING] {warning}")

    generator = RouterOSScriptGenerator(result, options)
    deploy = generator.generate_deploy_script()
    uninstall = generator.generate_uninstall_script()

    print("Mikrozia validation passed")
    print(f"  RouterOS:  {result['platform']['routeros_version']}")
    print(f"  Image arch: {result['platform']['image_arch']}")
    print(f"  Storage:   {result['storage']['target']}")
    print(f"  Subnet:    {result['network']['subnet']}")
    print("  Firewall:  untouched")

    if not args.write:
        print("\n[DRY RUN] No files written. Pass --write after reviewing the preview.\n")
        print("\n".join(deploy.splitlines()[:35]))
        return 0

    os.makedirs(args.output_dir, exist_ok=True)
    deploy_path = os.path.join(args.output_dir, f"{args.prefix}-deploy.rsc")
    uninstall_path = os.path.join(args.output_dir, f"{args.prefix}-uninstall.rsc")
    with open(deploy_path, "w", encoding="utf-8") as handle:
        handle.write(deploy)
    with open(uninstall_path, "w", encoding="utf-8") as handle:
        handle.write(uninstall)
    print(f"Wrote {deploy_path}")
    print(f"Wrote {uninstall_path}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
