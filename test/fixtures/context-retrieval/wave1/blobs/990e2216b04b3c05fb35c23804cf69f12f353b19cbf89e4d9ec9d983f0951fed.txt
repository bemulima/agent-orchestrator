import contextlib
import importlib.util
import io
import os
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch


ROOT = Path(__file__).resolve().parents[2]
SPEC = importlib.util.spec_from_file_location(
    "user_native_config", ROOT / "scripts" / "native_config.py"
)
NATIVE_CONFIG = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(NATIVE_CONFIG)


class NativeConfigTests(unittest.TestCase):
    def test_native_avatar_dependencies_use_scoped_credentials_and_loopback(self):
        credentials = {
            "LW_POSTGRES_PASSWORD": "postgres-test-secret",
            "LW_AUTH_JWT_SECRET": "jwt-test-secret",
            "LW_INTERNAL_TOKEN": "general-internal-test-secret",
            "LW_FILESTORAGE_USER_TOKEN": "user-filestorage-test-secret",
            "LW_USER_IMAGE_PROCESSOR_TOKEN": "user-image-processor-test-secret",
        }
        values = {
            **credentials,
            "USER_NATIVE_HTTP_PORT": "18082",
            "USER_NATIVE_FILESTORAGE_URL": "http://127.0.0.1:18085",
            "USER_NATIVE_IMAGE_PROCESSOR_URL": "http://127.0.0.1:18086",
        }
        with tempfile.TemporaryDirectory() as temporary:
            env_file = Path(temporary) / "private.env"
            env_file.write_text(
                "".join(f"{key}={value}\n" for key, value in values.items()),
                encoding="utf-8",
            )
            output = io.StringIO()
            with patch.dict(os.environ, {"LW_INFRA_ENV_FILE": str(env_file)}, clear=True):
                with contextlib.redirect_stdout(output):
                    runtime = NATIVE_CONFIG.native_environment()

        self.assertEqual(runtime["APP_HOST"], "127.0.0.1")
        self.assertEqual(runtime["APP_PORT"], "18082")
        self.assertEqual(runtime["MS_RBAC"], "http://127.0.0.1:18080")
        self.assertEqual(runtime["MS_FILESTORAGE_URL"], "http://127.0.0.1:18085")
        self.assertEqual(runtime["FILESTORAGE_INTERNAL_TOKEN"], credentials["LW_FILESTORAGE_USER_TOKEN"])
        self.assertEqual(
            runtime["IMAGE_PROCESSOR_SERVICE_BASE_URL"], "http://127.0.0.1:18086"
        )
        self.assertEqual(
            runtime["IMAGE_PROCESSOR_INTERNAL_TOKEN"],
            credentials["LW_USER_IMAGE_PROCESSOR_TOKEN"],
        )
        self.assertEqual(
            runtime["INTERNAL_API_TOKEN"], credentials["LW_INTERNAL_TOKEN"]
        )
        for secret in credentials.values():
            self.assertNotIn(secret, output.getvalue())


if __name__ == "__main__":
    unittest.main()
