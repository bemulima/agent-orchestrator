"""Authenticated lifecycle transport for the trusted host controller.

This staging listener binds only loopback. It is not a Docker proxy and is not
the production coordinator route. Both requests and responses are bound to one
execution and a fresh nonce; loss of transport never starts a local fallback.
"""
import hashlib
import hmac
import http.client
import http.server
import json
import re
import socket
import socketserver
import threading
import time
import urllib.parse

from cdo_runtime_controller import ControllerDenied as RuntimeDenied, signed_request, protocol_sha256

MAX_REQUEST_BYTES = 16384
MAX_RESPONSE_BYTES = 1048576
ENDPOINT = "/v1/executions"
MAX_HANDLERS = 4
REQUEST_READ_SECONDS = 5


def canonical(value):
    return json.dumps(value, sort_keys=True, separators=(",", ":"), allow_nan=False).encode()


def _object(pairs):
    value = {}
    for key, item in pairs:
        if key in value:
            raise ValueError("duplicate JSON field")
        value[key] = item
    return value


def decode(data):
    return json.loads(data, object_pairs_hook=_object,
                      parse_constant=lambda _: (_ for _ in ()).throw(ValueError("nonfinite JSON")))


def response_signature(secret, value):
    return hmac.new(secret, b"cdo-runtime-response-v1\0" + canonical(value), hashlib.sha256).hexdigest()


def handler_for(controller, registrations):
    """Keys and registrations come only from the trusted host, never HTTP input."""
    registrations = {r.execution_id: r for r in registrations}

    class Handler(http.server.BaseHTTPRequestHandler):
        protocol_version = "HTTP/1.0"

        def setup(self):
            super().setup()
            self.connection.settimeout(REQUEST_READ_SECONDS)
            # Absolute limit covers unfinished headers as well as slow bodies.
            # A socket inactivity timeout alone can be reset by a trickle attack.
            def expire():
                try:
                    self.connection.shutdown(socket.SHUT_RDWR)
                except OSError:
                    pass
            self.read_timer = threading.Timer(REQUEST_READ_SECONDS, expire)
            self.read_timer.daemon = True
            self.read_timer.start()

        def finish(self):
            self.read_timer.cancel()
            super().finish()

        def log_message(self, *_):
            pass

        def do_POST(self):
            if self.path != ENDPOINT or self.headers.get("Content-Type") != "application/json":
                self.send_error(400)
                return
            lengths = self.headers.get_all("Content-Length") or []
            if len(lengths) != 1 or not re.fullmatch(r"[0-9]{1,5}", lengths[0]) or self.headers.get("Transfer-Encoding"):
                self.send_error(400)
                return
            size = int(lengths[0])
            if not 0 < size <= MAX_REQUEST_BYTES:
                self.send_error(413)
                return
            try:
                body = self.rfile.read(size)
                if len(body) != size:
                    raise ValueError("short body")
                request = decode(body)
                if not isinstance(request, dict):
                    raise ValueError("object required")
                registration = registrations.get(request.get("execution_id"))
                if registration is None:
                    self.send_error(403)
                    return
                self.read_timer.cancel()
                try:
                    result = controller.handle(request)
                    value = {"ok": True, "result": result}
                except RuntimeDenied as error:
                    value = {"ok": False, "error": str(error)}
                except Exception:
                    # Never return subprocess errors, paths, credentials or raw traces.
                    value = {"ok": False, "error": "RUNTIME_TRANSPORT_UNKNOWN"}
                value.update(execution_id=registration.execution_id,
                             nonce=request.get("nonce"),
                             controller_id=controller.config.controller_id,
                             protocol_sha256=protocol_sha256())
                value["signature"] = response_signature(registration.secret, value)
                encoded = canonical(value)
                if len(encoded) > MAX_RESPONSE_BYTES:
                    raise ValueError("response limit")
                self.send_response(200)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(encoded)))
                self.end_headers()
                self.wfile.write(encoded)
            except (ValueError, TypeError, TimeoutError, OSError, RecursionError):
                self.send_error(400)

    return Handler


class BoundedServer(socketserver.ThreadingMixIn, http.server.HTTPServer):
    daemon_threads = True
    block_on_close = False

    def __init__(self, address, handler):
        self.handlers = threading.BoundedSemaphore(MAX_HANDLERS)
        super().__init__(address, handler)

    def process_request(self, request, client_address):
        if not self.handlers.acquire(blocking=False):
            self.shutdown_request(request)
            return
        try:
            super().process_request(request, client_address)
        except Exception:
            self.handlers.release()
            raise

    def process_request_thread(self, request, client_address):
        try:
            super().process_request_thread(request, client_address)
        finally:
            self.handlers.release()


def listener(controller, registrations):
    # Loopback deployment is deliberately not advertised as backend OCI routing.
    return BoundedServer(("127.0.0.1", 0), handler_for(controller, registrations))


class RuntimeClient:
    def __init__(self, url, registration, controller_id):
        parsed = urllib.parse.urlsplit(url)
        if parsed.scheme != "http" or parsed.hostname != "127.0.0.1" or parsed.username or parsed.password or parsed.query or parsed.fragment or parsed.path not in ("", "/") or not parsed.port:
            raise RuntimeDenied("RUNTIME_ENDPOINT_DENIED")
        self.host, self.port = parsed.hostname, parsed.port
        self.registration, self.controller_id = registration, controller_id

    def call(self, operation, expires_at=None):
        request = signed_request(self.registration, operation, int(time.time()) + 30 if expires_at is None else expires_at)
        encoded = canonical(request)
        if len(encoded) > MAX_REQUEST_BYTES:
            raise RuntimeDenied("RUNTIME_REQUEST_LIMIT")
        connection = http.client.HTTPConnection(self.host, self.port, timeout=10)
        try:
            connection.request("POST", ENDPOINT, encoded, {"Content-Type": "application/json"})
            response = connection.getresponse()
            # Redirects and HTTP/proxy errors are unknown, never local Docker fallbacks.
            if response.status != 200 or response.getheader("Content-Type") != "application/json":
                raise RuntimeDenied("RUNTIME_TRANSPORT_UNKNOWN")
            body = response.read(MAX_RESPONSE_BYTES + 1)
            if len(body) > MAX_RESPONSE_BYTES:
                raise RuntimeDenied("RUNTIME_RESPONSE_LIMIT")
            value = decode(body)
            if not isinstance(value, dict):
                raise RuntimeDenied("RUNTIME_RESPONSE_DENIED")
            signature = value.pop("signature", None)
            if not isinstance(signature, str) or not hmac.compare_digest(signature, response_signature(self.registration.secret, value)):
                raise RuntimeDenied("RUNTIME_RESPONSE_SIGNATURE_INVALID")
            if value.get("execution_id") != self.registration.execution_id or value.get("nonce") != request["nonce"] or value.get("controller_id") != self.controller_id or value.get("protocol_sha256") != protocol_sha256():
                raise RuntimeDenied("RUNTIME_RESPONSE_BINDING_INVALID")
            if value.get("ok") is not True:
                raise RuntimeDenied(value.get("error", "RUNTIME_TRANSPORT_UNKNOWN"))
            return value["result"]
        except (OSError, http.client.HTTPException, ValueError, TypeError, KeyError, RecursionError):
            raise RuntimeDenied("RUNTIME_TRANSPORT_UNKNOWN") from None
        finally:
            connection.close()
