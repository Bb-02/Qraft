"""Deployment graph for read-only worker observations, with no instance data."""
from pathlib import Path
import unittest

import yaml


ROOT = Path(__file__).resolve().parents[1]
RELAY_URL = "http://worker:8092/generation/sandbox-readiness"


class SandboxReadinessWiringTests(unittest.TestCase):
    def test_server_and_generated_desktop_keep_execution_isolated(self):
        for filename in ("docker-compose.yml", "desktop/internal/app/runtime/compose.yml"):
            with self.subTest(compose=filename):
                compose = yaml.safe_load((ROOT / filename).read_text(encoding="utf-8"))
                services = compose["services"]
                api, worker, sandbox = (services[name] for name in ("api", "worker", "sandbox"))
                self.assertEqual(api["environment"]["ALGOFORGE_WORKER_SANDBOX_READINESS_URL"], RELAY_URL)
                self.assertEqual(worker["environment"]["SANDBOX_URL"], "http://sandbox:8090")
                self.assertEqual(worker["environment"]["OUTBOX_HEALTH_ADDR"], ":8092")
                self.assertEqual(set(api["networks"]), {"qraft"})
                self.assertEqual(set(worker["networks"]), {"qraft", "sandbox_internal"})
                self.assertEqual(set(sandbox["networks"]), {"sandbox_internal"})
                self.assertIs(compose["networks"]["sandbox_internal"]["internal"], True)
                for name, service in services.items():
                    if name not in ("worker", "sandbox"):
                        self.assertNotIn("sandbox_internal", service.get("networks", []), name)
                self.assertFalse(worker.get("ports"), "worker observations must remain internal")
                self.assertFalse(sandbox.get("ports"), "sandbox execution must never be published")
                self.assertIn("8092", worker["expose"])
                # A fresh installation must still open settings before the
                # generation worker is configured or started.
                self.assertNotIn("worker", api.get("depends_on", {}))


if __name__ == "__main__":
    unittest.main()
