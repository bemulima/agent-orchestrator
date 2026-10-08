import contextlib
import importlib.util
import io
import os
from pathlib import Path
import tempfile
import unittest
from unittest import mock


SCRIPT = Path(__file__).resolve().parents[2] / "scripts" / "native_config.py"
SPEC = importlib.util.spec_from_file_location("auth_native_config", SCRIPT)
NATIVE_CONFIG = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(NATIVE_CONFIG)


class NativeJWTConfigTest(unittest.TestCase):
    def run_config(self, overrides=()):
        with tempfile.TemporaryDirectory() as directory:
            env_file = Path(directory) / ".env"
            env_file.write_text(
                "LW_AUTH_DB_PASSWORD=test-db-password\n"
                "LW_POSTGRES_PASSWORD=test-db-password\n"
                "LW_IDENTITY_TARANTOOL_USER=test-identity\n"
                "LW_IDENTITY_TARANTOOL_PASSWORD=test-identity-password\n",
                encoding="utf-8",
            )
            environment = {"LW_INFRA_ENV_FILE": str(env_file), **dict(overrides)}
            output = io.StringIO()
            with mock.patch.dict(os.environ, environment, clear=True), contextlib.redirect_stdout(output):
                result = NATIVE_CONFIG.native_environment(require_tarantool=False)
            self.assertNotIn("test-db-password", output.getvalue())
            self.assertNotIn("test-identity-password", output.getvalue())
            return result

    def test_native_defaults_match_shared_auth_and_user_contract(self):
        environment = self.run_config()
        self.assertEqual(environment["AUTH_JWT_ISSUER"], "lw-auth")
        self.assertEqual(environment["AUTH_JWT_AUDIENCE"], "frontend")

    def test_native_identity_endpoint_is_direct_and_credentials_are_opaque(self):
        environment = self.run_config()
        self.assertEqual(environment["TARANTOOL_HOST"], "127.0.0.1")
        self.assertEqual(environment["TARANTOOL_PORT"], "3301")
        self.assertEqual(environment["TARANTOOL_USER"], "test-identity")
        self.assertEqual(environment["TARANTOOL_PASSWORD"], "test-identity-password")
        self.assertNotIn("TARANTOOL_SIGNUP_URL", environment)

    def test_native_jwt_overrides_are_explicit(self):
        environment = self.run_config(
            {
                "AUTH_NATIVE_JWT_ISSUER": "test-issuer",
                "AUTH_NATIVE_JWT_AUDIENCE": "test-audience",
            }
        )
        self.assertEqual(environment["AUTH_JWT_ISSUER"], "test-issuer")
        self.assertEqual(environment["AUTH_JWT_AUDIENCE"], "test-audience")


if __name__ == "__main__":
    unittest.main()
