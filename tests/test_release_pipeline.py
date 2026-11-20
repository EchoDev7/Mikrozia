#!/usr/bin/env python3
"""P0-REL-01 release-pipeline closure tests.

The only supported Mikrozia production release is a versioned RouterOS OCI
image built and smoke-tested by .github/workflows/docker.yml. No other
workflow may publish artifacts, no mutable upstream channel may feed a
release input, and every published architecture must be explicit in the
workflow, in the release docs, and in the smoke matrix.
"""

import os
import re
import subprocess
import unittest
from pathlib import Path

REPO_ROOT = Path(os.path.abspath(os.path.join(os.path.dirname(__file__), "..")))
WORKFLOW_DIR = REPO_ROOT / ".github" / "workflows"
DOCKER_WORKFLOW = WORKFLOW_DIR / "docker.yml"

# One source of truth for the release platforms used by the assertions below.
EXPECTED_PLATFORMS = {
    "linux/amd64": ("amd64", "xray-linux-amd64", "mtg-linux-amd64"),
    "linux/arm64/v8": ("arm64", "xray-linux-arm64", "mtg-linux-arm64"),
    "linux/arm/v7": ("armv7", "xray-linux-arm32", "mtg-linux-arm"),
}

RELEASE_DOCS = (
    REPO_ROOT / "README.md",
    REPO_ROOT / "docs" / "DOCKER_HUB_OVERVIEW.md",
    REPO_ROOT / "docs" / "MIKROZIA_CHR_INSTALL_FA.md",
)


def read(path):
    with open(path, encoding="utf-8") as handle:
        return handle.read()


def workflows():
    return sorted(WORKFLOW_DIR.glob("*.yml"))


def release_inputs():
    paths = [
        DOCKER_WORKFLOW,
        REPO_ROOT / "Dockerfile",
        REPO_ROOT / "DockerInit.sh",
    ]
    paths.extend(sorted((REPO_ROOT / "scripts" / "build").glob("*.sh")))
    return paths


class SingleReleasePathTest(unittest.TestCase):
    def test_inherited_binary_release_workflow_and_assets_are_removed(self):
        self.assertFalse(
            (WORKFLOW_DIR / "release.yml").exists(),
            "the inherited binary release workflow must stay retired",
        )
        self.assertFalse((REPO_ROOT / "windows_files").exists(), "the Windows release assets have no supported consumer")
        for unit in ("x-ui.service.debian", "x-ui.service.arch", "x-ui.service.rhel", "x-ui.rc"):
            self.assertFalse(
                (REPO_ROOT / unit).exists(),
                f"{unit} only fed the retired host installation and must stay removed",
            )

    def test_only_the_oci_workflow_can_publish(self):
        publishers = []
        for path in workflows():
            content = read(path)
            if (
                "docker/login-action" in content
                or "svenstaro/upload-release-action" in content
                or "gh release create" in content
                or re.search(r"^\s*push:\s*true\s*$", content, re.MULTILINE)
            ):
                publishers.append(path.name)
        self.assertEqual(publishers, ["docker.yml"], "exactly one workflow may publish a Mikrozia artifact")

    def test_no_workflow_packages_or_uploads_panel_archives(self):
        for path in workflows():
            content = read(path)
            for token in ("x-ui-linux-", "x-ui-windows", "upload-release-action", "3xui_amd64.tar"):
                self.assertNotIn(token, content, f"{path.name} must not publish legacy panel archives ({token})")

    def test_broad_tag_globs_cannot_capture_mikrozia_tags(self):
        for path in workflows():
            content = read(path)
            self.assertNotRegex(
                content,
                r"^\s*-\s*[\"']v\*\.\*\.\*[\"']\s*$",
                f"{path.name} must not use the unqualified v*.*.* tag glob",
            )
        docker = read(DOCKER_WORKFLOW)
        self.assertIn('- "v*-mikrozia"', docker)
        self.assertIn('- "v*.*.*-mikrozia.*"', docker)
        self.assertEqual(docker.count('- "v*-mikrozia"'), 1)
        self.assertEqual(docker.count('- "v*.*.*-mikrozia.*"'), 1)

    def test_retired_assets_are_not_described_as_supported(self):
        architecture = read(REPO_ROOT / "docs" / "architecture.md")
        for token in ("release.yml", "x-ui.service.", "x-ui.rc", "windows_files/"):
            self.assertNotIn(token, architecture, f"docs/architecture.md still presents retired tooling: {token}")


class MutableReleaseInputTest(unittest.TestCase):
    def test_supported_release_inputs_have_no_mutable_channels(self):
        for path in release_inputs():
            content = read(path)
            for token in ("releases/latest", "latest/download", "check-latest", "toolchains.bootlin"):
                self.assertNotIn(token, content, f"{path.relative_to(REPO_ROOT)} must not use a mutable release input ({token})")

    def test_release_workflow_installs_nothing_from_latest(self):
        docker = read(DOCKER_WORKFLOW)
        self.assertNotRegex(docker, r"@latest\b", "the release workflow must not install moving tool versions")
        for token in ("curl -Ls", "wget "):
            self.assertNotIn(token, docker, "the release workflow must not download unpinned build inputs")


class ArchitectureMatrixTest(unittest.TestCase):
    def matrix(self):
        docker = read(DOCKER_WORKFLOW)
        entries = re.findall(
            r"- platform: (linux/\S+)\s*\n\s+arch: (\S+)\s*\n\s+xray_bin: (\S+)\s*\n\s+mtg_bin: (\S+)",
            docker,
        )
        return {platform: (arch, xray, mtg) for platform, arch, xray, mtg in entries}

    def test_matrix_is_explicit_and_matches_the_supported_set(self):
        self.assertEqual(self.matrix(), EXPECTED_PLATFORMS)

    def test_supported_platform_list_matches_the_matrix(self):
        docker = read(DOCKER_WORKFLOW)
        match = re.search(r'SUPPORTED_PLATFORMS:\s*"([^"]+)"', docker)
        self.assertIsNotNone(match, "docker.yml must declare SUPPORTED_PLATFORMS")
        self.assertEqual(match.group(1).split(","), list(EXPECTED_PLATFORMS))
        self.assertIn("platforms: ${{ env.SUPPORTED_PLATFORMS }}", docker, "publish must use the declared platform set")

    def test_every_published_architecture_is_built_and_started(self):
        docker = read(DOCKER_WORKFLOW)
        self.assertIn("platforms: ${{ matrix.platform }}", docker, "each matrix entry must be built for its own platform")
        self.assertIn("load: true", docker)
        self.assertIn("push: false", docker, "the smoke build must never publish")
        self.assertIn('--platform "$platform"', docker, "the runtime contract must run the matching architecture")
        self.assertIn("if: matrix.arch != 'amd64'", docker, "non-native architectures require QEMU")
        self.assertIn("if: matrix.integration", docker, "the integration test must be scoped explicitly")

    def test_arm_binary_names_match_the_build(self):
        dockerinit = read(REPO_ROOT / "DockerInit.sh")
        self.assertIn('XRAY_BIN="xray-linux-arm32"', dockerinit)
        self.assertIn('MTG_BIN="mtg-linux-arm"', dockerinit)
        matrix = self.matrix()
        self.assertEqual(matrix["linux/arm/v7"][1:], ("xray-linux-arm32", "mtg-linux-arm"))

    def test_release_docs_list_only_the_published_platforms(self):
        for path in RELEASE_DOCS:
            content = read(path)
            for platform in EXPECTED_PLATFORMS:
                self.assertIn(platform, content, f"{path.relative_to(REPO_ROOT)} must name every published platform")
            for unsupported in ("linux/386", "linux/s390x", "linux/arm/v5", "linux/arm/v6"):
                self.assertNotIn(unsupported, content, f"{path.relative_to(REPO_ROOT)} must not claim {unsupported}")


class VersionAndEvidenceTest(unittest.TestCase):
    def test_release_tag_contract(self):
        docker = read(DOCKER_WORKFLOW)
        self.assertIn("sh scripts/build/verify-mikrozia-version.sh --canonical-only", docker)
        self.assertIn('if [[ "${release_tag}" != "${canonical_version}" ]]', docker)
        self.assertIn('sh scripts/build/verify-mikrozia-version.sh "$RELEASE_TAG"', docker)

    def test_smoke_and_publish_share_version_inputs(self):
        docker = read(DOCKER_WORKFLOW)
        self.assertIn("MIKROZIA_VERSION=${{ env.RELEASE_TAG }}", docker)
        self.assertIn("XRAY_VERSION=${{ env.XRAY_VERSION }}", docker)
        self.assertIn("MTG_VERSION=${{ env.MTG_VERSION }}", docker)
        self.assertIn("org.opencontainers.image.version", docker)
        self.assertIn("io.mikrozia.xray.version", docker)
        self.assertIn("io.mikrozia.mtg.version", docker)

    def test_runtime_version_and_labels_agree(self):
        docker = read(DOCKER_WORKFLOW)
        self.assertIn('test "$actual_mikrozia_ver" = "$RELEASE_TAG"', docker)
        self.assertIn('expected_runtime_mikrozia_ver="Mikrozia ${RELEASE_TAG%-mikrozia}"', docker)
        self.assertIn('test "$runtime_mikrozia_ver" = "$expected_runtime_mikrozia_ver"', docker)
        self.assertIn('test "$actual_xray_ver" = "$XRAY_VERSION"', docker)
        self.assertIn('test "$actual_mtg_ver" = "$MTG_VERSION"', docker)

    def test_runtime_smoke_expectation_tracks_the_canonical_release_tag(self):
        docker = read(DOCKER_WORKFLOW)
        match = re.search(r'^\s*(expected_runtime_mikrozia_ver="[^"]*")$', docker, re.MULTILINE)
        self.assertIsNotNone(match, "smoke must derive the displayed version from RELEASE_TAG")
        assignment = match.group(1)
        self.assertNotRegex(assignment, r"v\d", "the smoke expectation must not hard-code a version")
        tag = read(REPO_ROOT / "MIKROZIA_VERSION").strip()
        script = 'RELEASE_TAG="$1"\n' + assignment + '\nprintf %s "$expected_runtime_mikrozia_ver"'
        result = subprocess.run(["sh", "-c", script, "sh", tag], capture_output=True, text=True, check=True)
        self.assertEqual(result.stdout, "Mikrozia " + tag.removesuffix("-mikrozia"))

    def test_release_record_carries_sbom_provenance_and_digest(self):
        docker = read(DOCKER_WORKFLOW)
        self.assertIn("sbom: true", docker)
        self.assertIn("provenance: mode=max", docker)
        self.assertIn("steps.build.outputs.digest", docker)

    def test_publish_waits_for_every_architecture_smoke_test(self):
        docker = read(DOCKER_WORKFLOW)
        publish = docker.index("  publish:")
        self.assertIn("needs: [validate, smoke]", docker[publish:])
        smoke = docker.index("  smoke:")
        self.assertIn("needs: validate", docker[smoke:publish])

    def test_latest_is_only_a_convenience_alias(self):
        docker = read(DOCKER_WORKFLOW)
        self.assertIn("type=raw,value=latest", docker)
        readme = read(REPO_ROOT / "README.md")
        self.assertIn("برای محیط Production از Tag نسخه‌دار استفاده کنید، نه `latest`", readme)


class LocalPublicationSafetyTest(unittest.TestCase):
    def test_release_workflow_has_no_branch_or_pull_request_trigger(self):
        docker = read(DOCKER_WORKFLOW)
        self.assertNotIn("pull_request:", docker)
        self.assertNotIn("branches:", docker)
        self.assertIn("workflow_dispatch:", docker)
        self.assertIn('"v*-mikrozia"', docker)
        self.assertIn('"v*.*.*-mikrozia.*"', docker)

    def test_credentials_are_confined_to_the_publish_job(self):
        docker = read(DOCKER_WORKFLOW)
        publish = docker.index("  publish:")
        self.assertNotIn("docker/login-action", docker[:publish], "no pre-publish job may authenticate to a registry")
        self.assertIn("docker/login-action", docker[publish:])
        self.assertIn("secrets.DOCKER_HUB_USERNAME", docker[publish:])
        self.assertIn("secrets.DOCKER_HUB_TOKEN", docker[publish:])

    def test_no_local_script_pushes_or_logs_in(self):
        candidates = [REPO_ROOT / "Makefile", REPO_ROOT / "docker-compose.yml"]
        candidates.extend(sorted((REPO_ROOT / "scripts").rglob("*.sh")))
        for path in candidates:
            if not path.exists():
                continue
            content = read(path)
            for token in ("docker push", "docker login", "build-push-action", "DOCKER_HUB_TOKEN"):
                self.assertNotIn(token, content, f"{path.relative_to(REPO_ROOT)} must not publish during local verification")

if __name__ == "__main__":
    unittest.main()
