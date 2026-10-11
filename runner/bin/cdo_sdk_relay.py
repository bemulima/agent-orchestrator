"""Fixed certification HTTP capabilities over OCI stdio; never a network proxy.

The container has network=none. Both sides treat every frame as untrusted. The
host selects the only two destinations and retains all endpoint credentials.
"""
from __future__ import annotations
import base64
import concurrent.futures
import http.client
import http.server
import json
import os
import pathlib
import queue
import re
import socket
import socketserver
import stat
import threading
import time

MAX_FRAME = 65536
CHUNK = 16384
MAX_INPUT = 1048576
MAX_OUTPUT = 4194304
MAX_BODY = 2097152
MAX_PROVIDER_BODY = 16777216
MAX_RESPONSE = 4194304
MAX_AGGREGATE = 67108864
MAX_CALLS = 128
MAX_INFLIGHT = 2
HTTP_SECONDS = 90

class RelayDenied(RuntimeError):
    pass

def denied(code):
    raise RelayDenied('SDK_NETWORK_RELAY_' + code)

def _object(pairs):
    value = {}
    for key, item in pairs:
        if key in value: denied('DUPLICATE_FIELD')
        value[key] = item
    return value

def decode(data):
    try:
        return json.loads(data, object_pairs_hook=_object,
                          parse_constant=lambda _: denied('NONFINITE_JSON'))
    except (ValueError, RecursionError, UnicodeError):
        denied('FRAME_INVALID')

def read_frame(stream):
    line = stream.readline(MAX_FRAME + 1)
    if not line: return None
    if len(line) > MAX_FRAME or not line.endswith(b'\n'): denied('FRAME_LIMIT')
    value = decode(line)
    if not isinstance(value, dict): denied('FRAME_INVALID')
    return value

class Writer:
    def __init__(self, stream):
        self.stream = stream
        self.lock = threading.Lock()
    def send(self, kind, **fields):
        encoded = json.dumps(dict(kind=kind, **fields), separators=(',', ':'), allow_nan=False).encode() + b'\n'
        if len(encoded) > MAX_FRAME: denied('FRAME_LIMIT')
        with self.lock:
            try:
                self.stream.write(encoded); self.stream.flush()
            except (BrokenPipeError, OSError): denied('DISCONNECTED')

def blob(value):
    if not isinstance(value, str) or len(value) > 4 * CHUNK // 3 + 4: denied('CHUNK_LIMIT')
    try: result = base64.b64decode(value, validate=True)
    except (ValueError, TypeError): denied('CHUNK_INVALID')
    if len(result) > CHUNK: denied('CHUNK_LIMIT')
    return result

def chunks(data):
    for offset in range(0, len(data), CHUNK):
        yield base64.b64encode(data[offset:offset + CHUNK]).decode()

def exact(value, fields):
    if not isinstance(value,dict) or set(value) != set(fields): denied('FIELDS_DENIED')

def request_shape(target, method, path, content_type):
    allowed = {'mcp': ('POST', '/', 'application/json'),
               'provider': ('POST', '/v1/responses', 'application/json')}
    if target not in allowed or (method, path, content_type) != allowed[target]: denied('REQUEST_DENIED')

def body_limit(target):
    if target=='mcp':return MAX_BODY
    if target=='provider':return MAX_PROVIDER_BODY
    denied('TARGET_DENIED')

def endpoint(url, suffix):
    # No DNS, userinfo, IPv6, query, fragment, alternate encodings or redirects.
    if not isinstance(url, str): denied('ENDPOINT_DENIED')
    match = re.fullmatch(r'http://127\.0\.0\.1:([1-9][0-9]{0,4})' + re.escape(suffix), url)
    if not match or not 1024 <= int(match[1]) <= 65535: denied('ENDPOINT_DENIED')
    return int(match[1])

def load_config(filename):
    path = pathlib.Path(filename)
    if not path.is_absolute(): denied('CONFIG_DENIED')
    try:
        before = path.lstat()
        if (not stat.S_ISREG(before.st_mode) or before.st_nlink != 1 or
            before.st_uid != os.getuid() or stat.S_IMODE(before.st_mode) != 0o600 or
            before.st_size > 16384 or path.resolve(strict=True) != path): denied('CONFIG_DENIED')
        fd = os.open(str(path), os.O_RDONLY | getattr(os, 'O_NOFOLLOW', 0))
        with os.fdopen(fd, 'rb') as source:
            after = os.fstat(source.fileno())
            if (before.st_dev, before.st_ino) != (after.st_dev, after.st_ino): denied('CONFIG_DENIED')
            value = decode(source.read(16385))
    except OSError: denied('CONFIG_DENIED')
    exact(value, ('version', 'mode', 'mcp', 'provider'))
    if value['version'] != 1 or type(value['version']) is not int or value['mode'] != 'deterministic-certification': denied('CONFIG_DENIED')
    exact(value['mcp'], ('url', 'token')); exact(value['provider'], ('url',))
    endpoint(value['mcp']['url'], '/'); endpoint(value['provider']['url'], '/v1')
    token = value['mcp']['token']
    if not isinstance(token, str) or not re.fullmatch(r'[A-Za-z0-9._-]{32,256}', token): denied('CONFIG_DENIED')
    return value

def rewrite_args(args, ports):
    values = list(args)
    seen = set()
    replacements = {'mcp_servers.cdo.url': 'http://127.0.0.1:%d/' % ports['mcp'],
                    'model_providers.cdo_fixture.base_url': 'http://127.0.0.1:%d/v1' % ports['provider'],
                    'mcp_servers.cdo.http_headers.Authorization': 'Bearer local-relay'}
    for index, item in enumerate(values):
        if index == 0 or values[index - 1] not in ('--config', '-c'): continue
        key = item.split('=', 1)[0]
        if key in replacements:
            if key in seen: denied('CONFIG_DUPLICATE')
            seen.add(key)
            values[index] = key + '=' + json.dumps(replacements[key])
    required={'mcp_servers.cdo.url','model_providers.cdo_fixture.base_url'}
    if not required.issubset(seen): denied('CONFIG_ABSENT')
    if 'mcp_servers.cdo.http_headers.Authorization' not in seen:
        values+=['--config','mcp_servers.cdo.http_headers.Authorization="Bearer local-relay"']
    return values

class HostRelay:
    def __init__(self, config, child, stdout):
        self.config = config; self.child = child; self.stdout = stdout
        self.writer = Writer(child.stdin); self.pending = {}; self.calls = 0
        self.aggregate = 0; self.output = 0; self.failure = None; self.connections = {}
        self.lock = threading.Lock(); self.pool = concurrent.futures.ThreadPoolExecutor(max_workers=MAX_INFLIGHT)
        self.futures = []
    def fail(self, code):
        with self.lock:
            if self.failure is None: self.failure = code
            connections=list(self.connections.values())
        for item in connections:
            try:
                if item.get('socket'):item['socket'].shutdown(socket.SHUT_RDWR)
                item['connection'].close()
            except OSError:pass
        try:
            self.child.terminate()
            def force():
                if self.child.poll() is None:self.child.kill()
            timer=threading.Timer(1,force);timer.daemon=True;timer.start()
        except OSError: pass
    def fetch(self, ident, request):
        connection = None; timer = None; socket_handle=None
        try:
            target, body = request['target'], bytes(request['body'])
            port = endpoint(self.config[target]['url'], '/' if target == 'mcp' else '/v1')
            connection = http.client.HTTPConnection('127.0.0.1', port, timeout=5)
            with self.lock:
                if self.failure:raise RelayDenied(self.failure)
                self.connections[ident]={'connection':connection,'socket':None}
            def expire():
                self.fail('SDK_NETWORK_RELAY_HTTP_TIMEOUT')
                try:
                    if socket_handle:socket_handle.shutdown(socket.SHUT_RDWR)
                    connection.close()
                except OSError: pass
            timer = threading.Timer(HTTP_SECONDS, expire); timer.daemon = True; timer.start()
            headers = {'Content-Type': 'application/json'}
            if target == 'mcp': headers['Authorization'] = 'Bearer ' + self.config['mcp']['token']
            # Capture the socket before HTTP/1.0 getresponse detaches it. The
            # retained handle can interrupt response.fp reads on timeout/cancel.
            connection.connect();socket_handle=connection.sock
            if socket_handle:socket_handle.settimeout(HTTP_SECONDS)
            with self.lock:
                self.connections[ident]['socket']=socket_handle
                if self.failure:raise RelayDenied(self.failure)
            # No caller-controlled headers, URL, proxy environment or redirects.
            connection.request('POST', request['path'], body=body, headers=headers)
            response = connection.getresponse()
            if response.status not in ((200, 202) if target == 'mcp' else (200,)): denied('HTTP_STATUS_DENIED')
            content_type = (response.getheader('Content-Type') or 'application/json').split(';', 1)[0].strip()
            if content_type not in ('application/json', 'text/event-stream'): denied('HTTP_CONTENT_TYPE_DENIED')
            self.writer.send('response_start', id=ident, status=response.status, content_type=content_type)
            total = 0
            while True:
                if self.failure:raise RelayDenied(self.failure)
                data = response.read(CHUNK)
                if not data: break
                total += len(data)
                if total > MAX_RESPONSE: denied('RESPONSE_LIMIT')
                with self.lock:
                    self.aggregate += len(data)
                    if self.aggregate > MAX_AGGREGATE: denied('AGGREGATE_LIMIT')
                for encoded in chunks(data): self.writer.send('response_body', id=ident, data=encoded)
            self.writer.send('response_end', id=ident)
        except Exception as error:
            self.fail(str(error) if isinstance(error, RelayDenied) else 'SDK_NETWORK_RELAY_HTTP_UNKNOWN')
        finally:
            if timer: timer.cancel()
            if connection: connection.close()
            with self.lock:
                self.pending.pop(ident, None);self.connections.pop(ident,None)
    def settle(self):
        _,unfinished=concurrent.futures.wait(self.futures,timeout=5)
        if unfinished:
            self.fail('SDK_NETWORK_RELAY_SHUTDOWN_UNKNOWN')
            concurrent.futures.wait(unfinished,timeout=5)
        self.pool.shutdown(wait=False,cancel_futures=True)
        if unfinished:raise RelayDenied('SDK_NETWORK_RELAY_SHUTDOWN_UNKNOWN')
    def run(self, prompt):
        if len(prompt) > MAX_INPUT: denied('INPUT_LIMIT')
        for data in chunks(prompt): self.writer.send('cli_input', data=data)
        self.writer.send('cli_eof')
        code = None
        try:
            while True:
                frame = read_frame(self.child.stdout)
                if frame is None: break
                if self.failure: raise RelayDenied(self.failure)
                kind = frame.get('kind')
                if kind == 'cli_output':
                    exact(frame, ('kind', 'data')); data = blob(frame['data']); self.output += len(data)
                    if self.output > MAX_OUTPUT: denied('OUTPUT_LIMIT')
                    self.stdout.write(data); self.stdout.flush()
                elif kind == 'request_start':
                    exact(frame, ('kind', 'id', 'target', 'method', 'path', 'content_type'))
                    ident = frame['id']
                    if type(ident) is not int or ident != self.calls + 1 or ident > MAX_CALLS: denied('REQUEST_ID_DENIED')
                    request_shape(frame['target'], frame['method'], frame['path'], frame['content_type'])
                    with self.lock:
                        if len(self.pending) >= MAX_INFLIGHT: denied('INFLIGHT_LIMIT')
                        self.pending[ident] = dict(target=frame['target'], path=frame['path'], body=bytearray(), ended=False)
                    self.calls += 1
                elif kind in ('request_body', 'request_end'):
                    exact(frame, ('kind', 'id', 'data') if kind == 'request_body' else ('kind', 'id'))
                    ident = frame['id']
                    if type(ident) is not int or ident not in self.pending: denied('REQUEST_ID_DENIED')
                    request = self.pending[ident]
                    if request['ended']: denied('REQUEST_ID_DENIED')
                    if kind == 'request_body':
                        data = blob(frame['data']); request['body'].extend(data)
                        with self.lock:
                            self.aggregate += len(data)
                            if len(request['body']) > body_limit(request['target']) or self.aggregate > MAX_AGGREGATE: denied('REQUEST_LIMIT')
                    else:
                        request['ended'] = True
                        self.futures.append(self.pool.submit(self.fetch, ident, request))
                elif kind == 'cli_exit':
                    exact(frame, ('kind', 'code'))
                    if type(frame['code']) is not int or not 0 <= frame['code'] <= 255: denied('EXIT_DENIED')
                    code = frame['code']; break
                else: denied('FRAME_DENIED')
            self.settle()
            if self.failure: raise RelayDenied(self.failure)
            if code is None: denied('DISCONNECTED')
            if self.pending: denied('REQUEST_UNFINISHED')
            return code
        except BaseException:
            self.fail('SDK_NETWORK_RELAY_FAILED'); raise
        finally:
            try: self.child.stdin.close()
            except OSError: pass
            self.settle()

class _Server(socketserver.ThreadingMixIn, http.server.HTTPServer):
    daemon_threads = True; block_on_close = False
    def __init__(self, relay, target, handler):
        self.relay = relay; self.target = target; self.slots = threading.BoundedSemaphore(MAX_INFLIGHT)
        super().__init__(('127.0.0.1', 0), handler)
    def process_request(self, request, address):
        if not self.slots.acquire(False): self.shutdown_request(request); return
        try: super().process_request(request, address)
        except Exception: self.slots.release(); raise
    def process_request_thread(self, request, address):
        try: super().process_request_thread(request, address)
        finally: self.slots.release()

class ContainerRelay:
    def __init__(self, stdin, stdout):
        self.stdin = stdin; self.writer = Writer(stdout); self.pending = {}; self.counter = 0
        self.lock = threading.Lock(); self.send_lock = threading.Lock(); self.child = None
        self.failure = None; self.input_bytes = 0; self.output_bytes = 0; self.eof = False
        self.servers = []; self.threads = []
        relay = self
        class Handler(http.server.BaseHTTPRequestHandler):
            protocol_version = 'HTTP/1.0'
            def log_message(self, *_): pass
            def setup(self):
                super().setup()
                self.connection.settimeout(5)
                def expire():
                    try:self.connection.shutdown(socket.SHUT_RDWR)
                    except OSError:pass
                self.timer=threading.Timer(5,expire);self.timer.daemon=True;self.timer.start()
            def finish(self):
                self.timer.cancel();super().finish()
            def do_POST(self):
                try:
                    self.connection.settimeout(HTTP_SECONDS)
                    lengths = self.headers.get_all('Content-Length') or []
                    if len(lengths) != 1 or not re.fullmatch(r'[0-9]{1,8}', lengths[0]) or self.headers.get('Transfer-Encoding'): denied('REQUEST_DENIED')
                    length = int(lengths[0])
                    if not 0 <= length <= body_limit(self.server.target): denied('REQUEST_LIMIT')
                    content_type = self.headers.get('Content-Type', '').split(';', 1)[0]
                    request_shape(self.server.target, 'POST', self.path, content_type)
                    body = self.rfile.read(length)
                    if len(body) != length: denied('DISCONNECTED')
                    self.timer.cancel()
                    ident, results = relay.request(self.server.target, self.path, body)
                    try:
                        while True:
                            frame = results.get(timeout=HTTP_SECONDS)
                            if frame['kind'] == 'response_start':
                                self.send_response(frame['status']); self.send_header('Content-Type', frame['content_type']); self.end_headers()
                            elif frame['kind'] == 'response_body': self.wfile.write(blob(frame['data'])); self.wfile.flush()
                            elif frame['kind'] == 'response_end': break
                            else: denied('FRAME_DENIED')
                    finally:
                        with relay.lock: relay.pending.pop(ident, None)
                except Exception:
                    relay.fail('SDK_NETWORK_RELAY_HTTP_FAILED')
                    self.close_connection = True
            def do_GET(self): self.send_error(405)
            def do_CONNECT(self): self.send_error(405)
        for target in ('mcp', 'provider'):
            server = _Server(self, target, Handler); self.servers.append(server)
            thread = threading.Thread(target=server.serve_forever, kwargs={'poll_interval': .1}, daemon=True)
            thread.start(); self.threads.append(thread)
    @property
    def ports(self): return {server.target: server.server_port for server in self.servers}
    def fail(self, reason):
        self.failure = reason
        if self.child and self.child.poll() is None: self.child.kill()
    def request(self, target, path, body):
        request_shape(target,'POST',path,'application/json')
        if len(body)>body_limit(target):denied('REQUEST_LIMIT')
        with self.send_lock:
            with self.lock:
                if len(self.pending) >= MAX_INFLIGHT or self.counter >= MAX_CALLS: denied('INFLIGHT_LIMIT')
                self.counter += 1; ident = self.counter; result = queue.Queue(maxsize=8); self.pending[ident] = result
            self.writer.send('request_start', id=ident, target=target, method='POST', path=path, content_type='application/json')
            for encoded in chunks(body): self.writer.send('request_body', id=ident, data=encoded)
            self.writer.send('request_end', id=ident)
        return ident, result
    def attach(self, child):
        self.child = child
        def read():
            try:
                while True:
                    frame = read_frame(self.stdin)
                    if frame is None:
                        if child.poll() is None: denied('DISCONNECTED')
                        return
                    kind = frame.get('kind')
                    if kind in ('cli_input', 'cli_eof'):
                        exact(frame, ('kind', 'data') if kind == 'cli_input' else ('kind',))
                        if self.eof: denied('INPUT_DENIED')
                        if kind == 'cli_eof': self.eof = True; child.stdin.close()
                        else:
                            data = blob(frame['data']); self.input_bytes += len(data)
                            if self.input_bytes > MAX_INPUT: denied('INPUT_LIMIT')
                            child.stdin.write(data); child.stdin.flush()
                    elif kind in ('response_start', 'response_body', 'response_end'):
                        exact(frame, {'response_start': ('kind', 'id', 'status', 'content_type'), 'response_body': ('kind', 'id', 'data'), 'response_end': ('kind', 'id')}[kind])
                        ident = frame['id']
                        if type(ident) is not int or ident not in self.pending: denied('RESPONSE_ID_DENIED')
                        if kind == 'response_start':
                            if type(frame['status']) is not int or frame['status'] not in (200, 202) or frame['content_type'] not in ('application/json', 'text/event-stream'): denied('RESPONSE_DENIED')
                        elif kind == 'response_body': blob(frame['data'])
                        self.pending[ident].put(frame, timeout=10)
                    else: denied('FRAME_DENIED')
            except Exception as error: self.fail(str(error))
        def output():
            try:
                while True:
                    data = child.stdout.read(CHUNK)
                    if not data: return
                    self.output_bytes += len(data)
                    if self.output_bytes > MAX_OUTPUT: denied('OUTPUT_LIMIT')
                    for encoded in chunks(data): self.writer.send('cli_output', data=encoded)
            except Exception as error: self.fail(str(error))
        for target in (read, output):
            thread = threading.Thread(target=target, daemon=True); thread.start(); self.threads.append(thread)
    def finish(self, code):
        # Join stdout reader before the terminal record, preserving event order.
        self.threads[-1].join(timeout=5)
        if self.threads[-1].is_alive():denied('OUTPUT_CLEANUP_UNKNOWN')
        if self.failure: raise RelayDenied(self.failure)
        self.writer.send('cli_exit', code=code if 0 <= code <= 255 else 1)
        for server in self.servers: server.shutdown(); server.server_close()
