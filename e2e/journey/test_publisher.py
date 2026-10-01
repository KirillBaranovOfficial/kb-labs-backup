import base64
import hashlib
import io
import json
import os
import subprocess
import tarfile
import tempfile
import unittest
from pathlib import Path


ROOT = Path(__file__).parents[2]
PUBLISHER = ROOT / "e2e" / "publisher" / "publish.sh"


class PublisherConflictTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="kb-publisher-test-")
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.bin = self.root / "bin"
        self.bin.mkdir()
        self.packages = self.root / "packages"
        self.packages.mkdir()
        self.tarball = self.packages / "sample-1.2.3.tgz"
        self.write_tarball(self.tarball, "1.2.3", "default")
        self.local_integrity = self.integrity(self.tarball)
        self.local_shasum = hashlib.sha1(self.tarball.read_bytes()).hexdigest()
        self.publish_log = self.root / "published-digests.txt"
        self._write_executable(
            "curl",
            "#!/bin/sh\n"
            "case \"$*\" in *'/-/ping'*) printf 'pong\\n' ;; *) printf '{}\\n' ;; esac\n",
        )
        self._write_executable(
            "npm",
            "#!/bin/sh\n"
            "case \"$1\" in\n"
            "  config) exit 0 ;;\n"
            "  publish) printf 'sha512-%s\\n' \"$(python3 -c 'import hashlib,sys; print(hashlib.sha512(open(sys.argv[1],\"rb\").read()).hexdigest())' \"$2\")\" >> \"$PUBLISH_LOG\"; "
            "if [ \"$PUBLISH_CONFLICT\" = 1 ]; then echo 'npm ERR! code EPUBLISHCONFLICT: 409 already exists'; exit 1; fi; "
            "echo published; exit 0 ;;\n"
            "  view) case \"$*\" in *dist.integrity*) printf '%s\\n' \"$REGISTRY_INTEGRITY\" ;; "
            "*) printf '%s\\n' \"$REGISTRY_SHASUM\" ;; esac ;;\n"
            "  *) exit 2 ;;\n"
            "esac\n",
        )

    def _write_executable(self, name, contents):
        path = self.bin / name
        path.write_text(contents)
        path.chmod(0o755)

    def write_tarball(self, path, version, marker):
        path.parent.mkdir(parents=True, exist_ok=True)
        with tarfile.open(path, "w:gz") as archive:
            files = {
                "package/package.json": json.dumps(
                    {"name": "@kb-labs/sample", "version": version}
                ).encode(),
                "package/build-marker.txt": marker.encode(),
            }
            for name, contents in files.items():
                info = tarfile.TarInfo(name)
                info.size = len(contents)
                archive.addfile(info, io.BytesIO(contents))

    def integrity(self, path):
        return "sha512-" + base64.b64encode(hashlib.sha512(path.read_bytes()).digest()).decode()

    def publish(self, registry_integrity="", registry_shasum="", conflict=True, packages_dir=None):
        env = os.environ.copy()
        env.update(
            {
                "PATH": f"{self.bin}:{env['PATH']}",
                "VERDACCIO_URL": "http://registry.invalid",
                "PUBLISH_PACKAGES_DIR": str(packages_dir or self.packages),
                "PUBLISH_LOG": str(self.publish_log),
                "PUBLISH_CONFLICT": "1" if conflict else "0",
                "REGISTRY_INTEGRITY": registry_integrity,
                "REGISTRY_SHASUM": registry_shasum,
            }
        )
        return subprocess.run(
            ["sh", str(PUBLISHER)],
            env=env,
            text=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            check=False,
        )

    # case: S-195.1
    def test_matching_registry_digest_skips_conflict(self):
        result = self.publish(self.local_integrity)
        self.assertEqual(result.returncode, 0, result.stdout)
        self.assertIn("matching tarball digest", result.stdout)
        self.assertIn("@kb-labs/sample@1.2.3", result.stdout)

    # case: S-195.1
    def test_matching_registry_shasum_skips_conflict(self):
        result = self.publish(registry_shasum=self.local_shasum)
        self.assertEqual(result.returncode, 0, result.stdout)
        self.assertIn(f"matching tarball digest: sha1-{self.local_shasum}", result.stdout)

    # case: S-195.4
    def test_pr_tarball_supersedes_same_version_base_tarball(self):
        packages_dir = self.root / "two-phase-packages"
        pr_tarball = packages_dir / "pr" / "sample-pr.tgz"
        base_tarball = packages_dir / "base" / "sample-base.tgz"
        self.write_tarball(pr_tarball, "1.2.3", "pr-build")
        self.write_tarball(base_tarball, "1.2.3", "base-build")

        result = self.publish(conflict=False, packages_dir=packages_dir)

        self.assertEqual(result.returncode, 0, result.stdout)
        self.assertIn("@kb-labs/sample@1.2.3 (superseded by PR tarball)", result.stdout)
        published_digest = "sha512-" + hashlib.sha512(pr_tarball.read_bytes()).hexdigest()
        self.assertEqual(self.publish_log.read_text().splitlines(), [published_digest])

    # case: S-195.2
    def test_different_registry_digest_fails_with_both_digests_and_recovery_hint(self):
        registry_integrity = "sha512-YmFkLWJ5dGVz"
        result = self.publish(registry_integrity)
        self.assertNotEqual(result.returncode, 0, result.stdout)
        self.assertIn("@kb-labs/sample@1.2.3", result.stdout)
        self.assertIn(registry_integrity, result.stdout)
        self.assertIn(self.local_integrity, result.stdout)
        self.assertIn("docker volume rm e2e_verdaccio-storage", result.stdout)
        self.assertIn("bump the package version", result.stdout)


if __name__ == "__main__":
    unittest.main()
