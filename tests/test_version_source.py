#!/usr/bin/env python3
"""Regression tests for the canonical Mikrozia release-version contract.

MIKROZIA_VERSION is the only editable release input and must contain exactly
one release value with no whitespace. Build arguments must match it byte for
byte; whitespace is rejected, never trimmed or normalized.
"""

import os
import re
import subprocess
import tempfile
import unittest

REPO_ROOT = os.path.abspath(os.path.join(os.path.dirname(__file__), ".."))
VERSION_FILE = os.path.join(REPO_ROOT, "MIKROZIA_VERSION")
VALIDATOR = os.path.join(REPO_ROOT, "scripts", "build", "verify-mikrozia-version.sh")
DOCKERFILE = os.path.join(REPO_ROOT, "Dockerfile")
DOCKER_WORKFLOW = os.path.join(REPO_ROOT, ".github", "workflows", "docker.yml")
MAIN_GO = os.path.join(REPO_ROOT, "main.go")
SERVER_GO = os.path.join(REPO_ROOT, "internal", "web", "service", "server.go")
PANEL_GO = os.path.join(REPO_ROOT, "internal", "web", "service", "panel", "panel.go")
README = os.path.join(REPO_ROOT, "README.md")

RELEASE_PATTERN = re.compile(r"^v(?:\d+\.\d+\.\d+-mikrozia\.\d+|\d+-mikrozia)$")


def read(path):
    with open(path, encoding="utf-8", newline="") as handle:
        return handle.read()


class CanonicalVersionInputTest(unittest.TestCase):
    def test_canonical_file_is_exactly_one_value(self):
        content = read(VERSION_FILE)
        self.assertIsNotNone(RELEASE_PATTERN.fullmatch(content))
        self.assertFalse(any(char.isspace() for char in content), repr(content))
        self.assertEqual(read(VERSION_FILE), read(VERSION_FILE).strip(), "canonical file has surrounding whitespace")


class ValidatorScriptTest(unittest.TestCase):
    def setUp(self):
        self.canonical = read(VERSION_FILE)

    def run_validator(self, *args, cwd=REPO_ROOT):
        return subprocess.run(
            ["sh", VALIDATOR, *args],
            cwd=cwd,
            capture_output=True,
            text=True,
            check=False,
        )

    def test_accepts_canonical_value_and_prints_it_exactly(self):
        result = self.run_validator(self.canonical)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout, self.canonical)

    def test_canonical_only_prints_the_exact_file_value(self):
        result = self.run_validator("--canonical-only")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout, self.canonical)

    def test_rejects_missing_value(self):
        for args in ((), ("",)):
            result = self.run_validator(*args)
            self.assertNotEqual(result.returncode, 0, args)
            self.assertIn("required", result.stderr)

    def test_rejects_whitespace_only_value(self):
        for value in (" ", "\t", "\n", "\r\n", " \t\n"):
            result = self.run_validator(value)
            self.assertNotEqual(result.returncode, 0, repr(value))
            self.assertIn("no whitespace", result.stderr, repr(value))

    def test_rejects_dev_sentinel(self):
        result = self.run_validator("dev")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("invalid", result.stderr)

    def test_rejects_malformed_versions(self):
        for value in ("v3.11", "3.11.0-mikrozia.4", "v3.11.0-", "v1", "v1-mikrozia.0", "v1-mikrozia extra", "vv1-mikrozia"):
            result = self.run_validator(value)
            self.assertNotEqual(result.returncode, 0, value)

    def test_rejects_every_whitespace_variant(self):
        canonical = self.canonical
        variants = [
            "v3. 11.0-mikrozia.4",
            "v3.11 .0-mikrozia.4",
            "v3.11.0-mikrozia. 4",
            " " + canonical,
            canonical + " ",
            "\t" + canonical,
            canonical + "\t",
            canonical + "\n",
            canonical + "\r\n",
            canonical + "\n" + canonical,
            canonical + "\nextra",
            canonical + "\n\n",
        ]
        for value in variants:
            result = self.run_validator(value)
            self.assertNotEqual(result.returncode, 0, repr(value))
            self.assertIn("no whitespace", result.stderr, repr(value))

    def test_rejects_value_that_disagrees_with_canonical_file(self):
        mismatched = "v2-mikrozia"
        if mismatched == self.canonical:
            mismatched = "v3-mikrozia"
        result = self.run_validator(mismatched)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("does not match canonical", result.stderr)

    def test_rejects_invalid_canonical_file(self):
        canonical = self.canonical
        invalid_files = [
            "",
            "dev",
            "v1-mikrozia.0",
            canonical + "\n",
            canonical + " ",
            " " + canonical,
            canonical + "\t",
            canonical + "\n" + canonical,
            canonical.replace("-", "- ", 1),
        ]
        for content in invalid_files:
            with tempfile.TemporaryDirectory() as tmpdir:
                with open(os.path.join(tmpdir, "MIKROZIA_VERSION"), "w", encoding="utf-8", newline="") as handle:
                    handle.write(content)
                result = self.run_validator(canonical, cwd=tmpdir)
            self.assertNotEqual(result.returncode, 0, repr(content))
            self.assertIn("canonical MIKROZIA_VERSION file", result.stderr, repr(content))


class DockerfileContractTest(unittest.TestCase):
    def setUp(self):
        self.content = read(DOCKERFILE)
        self.canonical = read(VERSION_FILE)

    def test_build_arg_is_required_not_defaulted(self):
        self.assertIn("ARG MIKROZIA_VERSION", self.content)
        self.assertNotRegex(self.content, r"ARG MIKROZIA_VERSION=\S")

    def test_validated_value_feeds_the_runtime_linker(self):
        self.assertIn(
            'RUN sh ./verify-mikrozia-version.sh "${MIKROZIA_VERSION}" > /tmp/mikrozia-version',
            self.content,
            "the validator must receive the build argument without shell normalization",
        )
        self.assertIn(
            "internal/config.mikroziaVersion=$(cat /tmp/mikrozia-version)",
            self.content,
            "ldflags must use the validated build-arg value, not a literal",
        )
        self.assertNotIn("tr -d '[:space:]' < MIKROZIA_VERSION", self.content)

    def test_oci_label_is_checked_against_the_linked_runtime_value(self):
        self.assertIn('org.opencontainers.image.version="${MIKROZIA_VERSION}"', self.content)
        self.assertIn(
            'test "$(cat /tmp/mikrozia-version)" = "${MIKROZIA_VERSION}"',
            self.content,
            "the final image must fail when the OCI label and runtime version disagree",
        )

    def test_release_literal_is_not_baked_into_the_build(self):
        self.assertNotIn(self.canonical, self.content)


class PublishWorkflowContractTest(unittest.TestCase):
    def setUp(self):
        self.content = read(DOCKER_WORKFLOW)

    def test_release_tag_uses_the_exact_canonical_file_value(self):
        self.assertIn(
            'canonical_version="$(sh scripts/build/verify-mikrozia-version.sh --canonical-only)"',
            self.content,
        )
        self.assertNotIn("tr -d '[:space:]' < MIKROZIA_VERSION", self.content)
        self.assertIn('if [[ "${release_tag}" != "${canonical_version}" ]]', self.content)

    def test_release_tag_is_validated_by_the_shared_build_contract(self):
        self.assertIn('sh scripts/build/verify-mikrozia-version.sh "$RELEASE_TAG"', self.content)

    def test_smoke_build_reuses_the_release_tag_and_checks_runtime_output(self):
        self.assertIn("MIKROZIA_VERSION=${{ env.RELEASE_TAG }}", self.content)
        self.assertIn("-entrypoint /app/x-ui", self.content)
        self.assertIn('test "$runtime_mikrozia_ver" = "$RELEASE_TAG"', self.content)
        self.assertIn("org.opencontainers.image.version", self.content)


class RuntimeConsumerTest(unittest.TestCase):
    def test_cli_status_and_panel_version_use_the_runtime_accessor(self):
        self.assertIn("config.GetPanelVersion()", read(MAIN_GO))
        self.assertIn("config.GetPanelVersion()", read(SERVER_GO))
        self.assertIn("config.GetMikroziaVersion()", read(PANEL_GO))


class NoWhitespaceNormalizationTest(unittest.TestCase):
    def test_active_release_path_does_not_delete_whitespace(self):
        self.assertNotIn("tr -d '[:space:]'", read(VALIDATOR))
        self.assertNotIn("tr -d '[:space:]' < MIKROZIA_VERSION", read(DOCKER_WORKFLOW))
        self.assertNotIn("tr -d '[:space:]' < MIKROZIA_VERSION", read(README))


if __name__ == "__main__":
    unittest.main()
