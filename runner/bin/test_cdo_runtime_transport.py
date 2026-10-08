import importlib.util
import os
from pathlib import Path
import socket
import tempfile
import threading
import time
import unittest
import uuid
from types import SimpleNamespace
from unittest.mock import patch

import cdo_runtime_controller as runtime
import cdo_runtime_transport as transport


class TransportTests(unittest.TestCase):
    def setUp(self):
        self.registration = SimpleNamespace(
            execution_id=str(uuid.uuid4()), release_id="a" * 64,
            workpackage_id=str(uuid.uuid4()), role="reviewer", recipe="sdk-runtime",
            scope_sha256="b" * 64, execution_expires_at=int(time.time()) + 60,
            secret=b"s" * 32,
            image_id="sha256:" + "d" * 64,
        )
        self.controller = SimpleNamespace(
            config=SimpleNamespace(controller_id="c" * 64),
            handle=lambda request: {"operation": request["operation"]},
        )

    def run_server(self, action):
        server = transport.listener(self.controller, [self.registration])
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        try:
            client = transport.RuntimeClient("http://127.0.0.1:" + str(server.server_port),
                                             self.registration, "c" * 64)
            return action(client)
        finally:
            server.shutdown()
            thread.join(timeout=2)
            server.server_close()

    def test_request_and_response_bound_to_execution_controller_protocol_nonce(self):
        self.assertEqual(self.run_server(lambda client: client.call("inspect")), {"operation": "inspect"})

    def test_denial_is_typed_and_no_local_fallback(self):
        def denied(_):
            raise runtime.ControllerDenied("OWNERSHIP_MISMATCH")
        self.controller.handle = denied
        with self.assertRaisesRegex(runtime.ControllerDenied, "OWNERSHIP_MISMATCH"):
            self.run_server(lambda client: client.call("cancel"))

    def test_response_mac_tamper_denies(self):
        original = transport.response_signature
        calls = []
        def tamper(secret, value):
            calls.append(1)
            return "0" * 64 if len(calls) == 1 else original(secret, value)
        with patch.object(transport, "response_signature", side_effect=tamper):
            with self.assertRaisesRegex(runtime.ControllerDenied, "RESPONSE_SIGNATURE_INVALID"):
                self.run_server(lambda client: client.call("inspect"))

    def test_authenticated_wrong_controller_denies(self):
        self.controller.config.controller_id = "d" * 64
        with self.assertRaisesRegex(runtime.ControllerDenied, "RESPONSE_BINDING_INVALID"):
            self.run_server(lambda client: client.call("inspect"))

    def test_endpoint_is_finite_no_redirect_proxy_or_external_transport(self):
        for url in ("http://localhost:1234", "https://127.0.0.1:1234", "http://127.0.0.1:1234/exec",
                    "http://user:password@127.0.0.1:1234", "http://127.0.0.1:1234/?x=1"):
            with self.subTest(url=url), self.assertRaises(runtime.ControllerDenied):
                transport.RuntimeClient(url, self.registration, "c" * 64)

    def test_duplicate_fields_and_nonfinite_json_denied(self):
        for value in (b'{"operation":"inspect","operation":"cancel"}', b'{"x":NaN}'):
            with self.assertRaises(ValueError):
                transport.decode(value)

    def test_transport_unavailable_is_unknown(self):
        client = transport.RuntimeClient("http://127.0.0.1:1234", self.registration, "c" * 64)
        with patch.object(transport.http.client.HTTPConnection, "request", side_effect=OSError("unavailable")):
            with self.assertRaisesRegex(runtime.ControllerDenied, "TRANSPORT_UNKNOWN"):
                client.call("start")

    def test_fixture_copy_never_dereferences_link_or_accepts_hardlink(self):
        script = Path(__file__).resolve().parents[2] / "scripts/probe-runtime-transport.py"
        spec = importlib.util.spec_from_file_location("runtime_probe_copy_test", script)
        probe = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(probe)
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder).resolve()
            source = root / "source"
            source.mkdir()
            external = root / "external"
            external.write_text("outside fixture")
            (source / "link").symlink_to(external)
            with self.assertRaisesRegex(RuntimeError, "FIXTURE_LINK_DENIED"):
                probe.readonly_copy(source, root / "destination")
            self.assertFalse((root / "destination").exists())
            (source / "link").unlink()
            os.link(external, source / "hardlink")
            with self.assertRaisesRegex(RuntimeError, "FIXTURE_HARDLINK_DENIED"):
                probe.file_inventory(source)
            self.assertEqual(external.read_text(), "outside fixture")

    def test_absolute_request_read_deadline_and_bounded_parallel_cancel(self):
        def action(client):
            with socket.create_connection((client.host, client.port), timeout=1) as peer:
                peer.sendall(b"POST /v1/executions HTTP/1.0\r\nContent-Type: application/json\r\n")
                self.assertEqual(client.call("inspect"), {"operation": "inspect"})
                time.sleep(.2)
                peer.settimeout(1)
                self.assertEqual(peer.recv(1024), b"")
        with patch.object(transport, "REQUEST_READ_SECONDS", .1):
            self.run_server(action)

    def test_protected_source_rejected_before_secret_read(self):
        script = Path(__file__).resolve().parents[2] / "scripts/probe-runtime-transport.py"
        spec = importlib.util.spec_from_file_location("runtime_probe_protected_test", script)
        probe = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(probe)
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder).resolve()
            (root / ".env").write_text("dummy")
            with patch.object(Path, "read_bytes", side_effect=AssertionError("SECRET_READ")):
                with self.assertRaisesRegex(RuntimeError, "FIXTURE_PROTECTED_PATH_DENIED"):
                    probe.file_inventory(root)


if __name__ == "__main__":
    unittest.main()
