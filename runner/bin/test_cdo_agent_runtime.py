"""Host lifecycle unit checks: fake processes and fake owned cleanup only."""
from __future__ import annotations
import concurrent.futures
from copy import deepcopy
from dataclasses import replace
import datetime
import email.message
import hashlib
import io
import json
import os
from pathlib import Path
import tempfile
import threading
import time
import unittest
from unittest.mock import Mock, patch
import uuid

import cdo_agent_runtime as agent
import cdo_runtime_backend as backend
import cdo_runtime_transport as transport
import cdo_runtime_attestation as attest


class FakeProcess:
    def __init__(self, events=(), code=0):
        self.pid = 424242
        self.stdout = io.BytesIO(b''.join(agent.canonical(v) + b'\n' for v in events))
        self.stderr = io.BytesIO()
        self.code = code
        self.terminated = False
    def poll(self): return self.code
    def wait(self, timeout=None): return self.code
    def terminate(self): self.terminated = True; self.code = -15
    def kill(self): self.terminated = True; self.code = -9


class AgentRuntimeTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.root = Path(self.temp.name).resolve()
        self.state = self.root / 'state'; self.state.mkdir(mode=0o700)
        self.python = self.root / 'python'; self.python.write_text('trusted executable'); self.python.chmod(0o500)
        self.coordinator = self.root / 'coordinator.py'; self.coordinator.write_text('trusted coordinator'); self.coordinator.chmod(0o400)
        self.now = 2000000000
        def file(name):
            return {'path': '/trusted/' + name, 'sha256': 'a' * 64, 'bytes': 1, 'mode': 0o500, 'uid': 0, 'gid': 0}
        self.manifest = {'schema': attest.SCHEMA, 'controller': file('controller.py'), 'transport': file('transport.py'),
            'implementation': [file('implementation.py')], 'protocol_sha256': agent.agent_protocol_sha256(),
            'python': {'executable': file('python'), 'version': '3.9', 'implementation': 'CPython', 'cache_tag': 'cpython-39',
                'openssl_version': 'OpenSSL unit', 'modules': [{'name': n, 'kind': 'built-in'} for n in sorted(attest.REQUIRED_MODULES)]},
            'node': {'executable': file('node'), 'version': 'v22.0.0', 'versions': {'node': '22.0.0'},
                'dependencies': [file('hardened.js')], 'native_dependencies': {'files': [file('node-native')], 'system_shared_cache_paths': []}},
            'runtime_dependencies': [file('runtime.py')], 'native_runtime': {'files': [file('native')],
                'system_shared_cache_paths': [], 'system_shared_cache': None, 'kernel': 'unit-kernel'},
            'signing_policy_sha256': 'b' * 64, 'security_resource_profiles_sha256': 'c' * 64,
            'docker': {'executable': file('docker'), 'endpoint': 'unix:///trusted/socket', 'daemon_id': 'trusted-daemon-id',
                'server_version': '29.8.1', 'images': {key: 'sha256:' + v * 64 for key, v in [('runner', '1'), ('sdk', '2'), ('command', '3')]}},
            'controller_configuration_sha256': hashlib.sha256(agent.canonical({'unit': True})).hexdigest()}
        self.manifest['attestation_sha256'] = hashlib.sha256(agent.canonical(self.manifest)).hexdigest()
        self.attest_digest = hashlib.sha256(agent.canonical(self.manifest)).hexdigest()
        self.request = {'role': 'coder', 'working_directory': '/backend/approved-alias', 'prompt': 'approved work',
                        'output_schema': {'type': 'object'}, 'sandbox_scope': {'profile': 'layer', 'write_paths': ['src/value.go']},
                        'execution_id': str(uuid.uuid4()), 'attempt': 1, 'runtime_context': {},
                        'execution_deadline': datetime.datetime.fromtimestamp(self.now + 300, datetime.timezone.utc).isoformat().replace('+00:00', 'Z')}
        self.template = agent.AgentTemplate(str(uuid.uuid4()), '1' * 64, 'layer', 'sha256:' + '2' * 64,
             hashlib.sha256(agent.canonical(self.request['sandbox_scope'])).hexdigest(), 'agent-v1',
             deepcopy(self.request), b's' * 32, environment={'CDO_SANDBOX_EXECUTION_MODE': 'hardened-certification',
             'CDO_CERTIFICATION_EVIDENCE': '${EXECUTION_ROOT}/evidence.json'})
        self.cleanup_result = {'resources_absent': True, 'status': 'CLEANED'}
        self.cleanup_calls = []
        self.process = FakeProcess()
        self.controllers = []
        self.controller = self.new_controller()
        self.controller.start_watchdog()
        self.credentials = {'version': 1, 'url': 'http://127.0.0.1:12345', 'controller_id': self.controller.config.controller_id,
             'protocol_sha256': agent.agent_protocol_sha256(), 'attestation_sha256': self.attest_digest,
             'secret_hex': self.template.secret.hex(), 'template': {key: getattr(self.template, key) for key in backend.TEMPLATE_FIELDS}}
    def tearDown(self):
        for controller in self.controllers: controller.close()
        self.temp.cleanup()
    def clean(self, file):
        self.cleanup_calls.append(file)
        return deepcopy(self.cleanup_result)
    def new_controller(self, **kwargs):
        def registration(file,process):
            state=value._load(file.parent)
            return value._process_binding(state)
        def teardown(file,expected):
            import cdo_runtime_coordinator as coordinator
            _,root,binding=coordinator.job_binding(file)
            proof={'version':1,'binding':binding,'coordinator':{'pid':424242,'pgid':424242,'sid':424242,'uid':os.geteuid()},
                   'node':{'pid':424243,'pgid':424243,'sid':424243,'uid':os.geteuid()},
                   'node_group_absent':True,'node_reaped':True,'no_unknown':True,
                   'node_exit_code':self.process.code,'terminal_cause':None,'proof_kind':'anchored-teardown'}
            value._atomic(root/'host-teardown.json',proof)
            return proof
        def host_cleanup(file,expected,oci_absent=False):
            if not expected.get('never_started') and not (file.parent/'host-teardown.json').exists():teardown(file,expected)
            return {'coordinator_absent':True,'node_groups_absent':True,'private_storage_absent':True,'no_unknown':True}
        kwargs.setdefault('registration',registration);kwargs.setdefault('teardown',teardown);kwargs.setdefault('host_cleanup',host_cleanup)
        value = agent.AgentController(self.state, [self.template], [str(self.python), str(self.coordinator)],
              lambda: {'attestation': deepcopy(self.manifest), 'controller_configuration': {'unit': True}},
              controller_id='3' * 64, attestation_sha256=self.attest_digest, cleanup=self.clean,
              process_factory=lambda *a, **k: self.process, clock=lambda: self.now,
              controller_configuration={'unit': True}, **kwargs)
        self.controllers.append(value); return value
    def req(self, op, **overrides):
        value = backend.agent_signed_request(self.credentials, self.request, op, now=self.now)
        value.update(overrides)
        return value
    def resign(self, value):
        value['signature'] = agent.request_signature(backend.request_secret(self.template.secret, value), value)
        return value
    def call(self, op, **overrides): return self.controller.handle(self.req(op, **overrides))
    def state_json(self): return json.loads((self.state / self.request['execution_id'] / 'agent-state.json').read_bytes())
    def create(self): return self.call('create')
    def run_events(self, events, code=0):
        self.process = FakeProcess(events, code); self.create()
        root=self.state / self.request['execution_id'];fd=self.controller._lock(root)
        try:
            journal,identity=self.controller._publication_journal(root,self.controller._load(root))
            self.controller._atomic(journal,{**identity,'status':'COMPLETE','at':self.now})
        finally:os.close(fd)
        self.call('start')
        self.controller._threads[self.request['execution_id']].join(3)
        self.assertFalse(self.controller._threads[self.request['execution_id']].is_alive())
        return self.call('inspect')
    def success_events(self):
        return [{'type': 'thread_started', 'thread_id': 'thread1'}, {'type': 'result',
            'execution_id': self.request['execution_id'], 'attempt': 1, 'thread_id': 'thread1', 'result': {'ok': True}, 'usage': {}}]

    def test_shared_protocol_is_exact_backend_export(self):
        self.assertIs(agent.AGENT_PROTOCOL, backend.PROTOCOL_DESCRIPTOR)
        self.assertEqual(agent.agent_protocol_sha256(), backend.protocol_sha256())
    def test_passive_attestation_full_manifest_without_execution_slot(self):
        result = self.call('inspect')
        self.assertEqual(result['attestation'], self.manifest)
        self.assertEqual(result['controller_configuration'], {'unit': True})
        self.assertEqual(result['state'], 'UNCREATED')
        self.assertFalse((self.state / self.request['execution_id']).exists())
        self.assertFalse(self.cleanup_calls)
    def test_many_observers_do_not_exhaust_execution_slots(self):
        for _ in range(agent.MAX_RECORDS + 1):
            self.request['execution_id'] = str(uuid.uuid4()); self.call('inspect')
        self.assertEqual(self.create()['state'], 'CREATED')
    def test_observer_replay_is_durable_across_restart(self):
        request = self.req('inspect'); self.controller.handle(request)
        other = self.new_controller()
        with self.assertRaisesRegex(agent.ControllerDenied, 'REPLAYED'): other.handle(request)
    def test_observer_nonce_cannot_reappear_after_create(self):
        request = self.req('inspect'); self.controller.handle(request)
        self.create()
        with self.assertRaisesRegex(agent.ControllerDenied, 'REPLAYED'): self.controller.handle(request)
    def test_atomic_concurrent_observer_nonce(self):
        request = self.req('inspect')
        def invoke(_):
            try: self.controller.handle(request); return 'ok'
            except agent.ControllerDenied as error: return error.code
        with concurrent.futures.ThreadPoolExecutor(8) as pool: results = list(pool.map(invoke, range(16)))
        self.assertEqual(results.count('ok'), 1)
        self.assertEqual(results.count('AGENT_REQUEST_REPLAYED'), 15)
    def test_atomic_concurrent_create_nonce(self):
        request = self.req('create')
        def invoke(_):
            try: self.controller.handle(request); return 'ok'
            except agent.ControllerDenied as error: return error.code
        with concurrent.futures.ThreadPoolExecutor(8) as pool: results = list(pool.map(invoke, range(16)))
        self.assertEqual(results.count('ok'), 1)
        self.assertEqual(results.count('AGENT_REQUEST_REPLAYED'), 15)
    def test_exact_fields_and_no_docker_rpc(self):
        for op in ('build', 'exec', 'prune', 'image-inspect'):
            with self.assertRaises(agent.ControllerDenied): self.controller.handle(self.resign(self.req('create', operation=op)))
        for field in ('image', 'flags', 'command', 'mounts', 'labels', 'payload'):
            with self.assertRaisesRegex(agent.ControllerDenied, 'FIELDS'): self.controller.handle(self.req('create', **{field: []}))
    def test_identity_spoof_all_public_bindings(self):
        for field in ('release_id', 'scope_sha256', 'request_sha256', 'controller_id', 'protocol_sha256', 'attestation_sha256'):
            with self.subTest(field=field), self.assertRaisesRegex(agent.ControllerDenied, 'IDENTITY'):
                self.controller.handle(self.resign(self.req('create', **{field: 'f' * 64})))
        for field, value in [('role', 'reviewer'), ('image_id', 'sha256:' + 'f' * 64), ('resource_profile', 'unbounded')]:
            with self.assertRaisesRegex(agent.ControllerDenied, 'IDENTITY'): self.controller.handle(self.resign(self.req('create', **{field: value})))
    def test_signature_domain_and_key_are_required(self):
        value = self.req('inspect'); value['signature'] = 'a' * 64
        with self.assertRaisesRegex(agent.ControllerDenied, 'SIGNATURE'): self.controller.handle(value)
        value = self.req('inspect'); value['signature'] = backend.request_signature(self.template.secret, value)
        with self.assertRaisesRegex(agent.ControllerDenied, 'SIGNATURE'): self.controller.handle(value)
    def test_cross_execution_secret_denied(self):
        value = self.req('inspect'); value['execution_id'] = str(uuid.uuid4())
        with self.assertRaisesRegex(agent.ControllerDenied, 'SIGNATURE'): self.controller.handle(value)
    def test_request_expiry_and_execution_cap(self):
        for field, value in [('request_expires_at', self.now), ('request_expires_at', self.now + 61), ('execution_expires_at', self.now + 1201), ('execution_expires_at', self.now)]:
            with self.assertRaisesRegex(agent.ControllerDenied, 'EXPIRED'): self.controller.handle(self.resign(self.req('create', **{field: value})))
    def test_bad_typed_fields_context_and_uuid(self):
        for field, value in [('version', True), ('attempt', True), ('cursor', True), ('execution_id', 'notuuid'), ('workpackage_id', []), ('runtime_context', {'activity_attempt': 1})]:
            with self.assertRaises(agent.ControllerDenied): self.controller.handle(self.req('create', **{field: value}))
    def test_cursor_zero_outside_diagnostics(self):
        with self.assertRaises(agent.ControllerDenied): self.controller.handle(self.resign(self.req('inspect', cursor=1)))
    def test_cancel_and_cleanup_allowed_after_execution_expiry(self):
        self.create(); self.now += 301
        self.assertEqual(self.call('cancel')['invalid_reason'], 'EXECUTION_CANCELLED')
        result = self.call('cleanup'); self.assertEqual(result['state'], 'CLEANED'); self.assertTrue(result['cleanup'])
    def test_cancel_before_create_permanently_claims_attempt(self):
        self.call('cancel'); self.request['execution_id'] = str(uuid.uuid4())
        with self.assertRaisesRegex(agent.ControllerDenied, 'ATTEMPT'): self.create()
    def test_inspect_renews_lease_diagnostics_does_not(self):
        self.create(); self.now += 5
        self.call('diagnostics'); self.assertEqual(self.state_json()['lease_expires_at'], self.now + 5)
        self.call('inspect'); self.assertEqual(self.state_json()['lease_expires_at'], self.now + 10)
    def test_invalid_signature_never_renews(self):
        self.create(); self.now += 5; request = self.req('inspect'); request['signature'] = 'f' * 64
        with self.assertRaises(agent.ControllerDenied): self.controller.handle(request)
        self.assertEqual(self.state_json()['lease_expires_at'], self.now + 5)
    def test_expired_lease_never_revives(self):
        self.create(); self.now += 11
        result = self.call('inspect'); self.assertEqual(result['state'], 'INVALIDATED')
        self.assertEqual(result['invalid_reason'], 'EXECUTION_LEASE_EXPIRED')
        self.call('inspect'); self.assertEqual(self.state_json()['lease_expires_at'], 0)
        with self.assertRaises(agent.ControllerDenied): self.call('start')
    def test_watchdog_expires_without_backend_poll(self):
        self.create(); self.now += 11
        for _ in range(50):
            if self.cleanup_calls: break
            time.sleep(.01)
        self.assertEqual(self.state_json()['invalid_reason'], 'EXECUTION_LEASE_EXPIRED')
        self.assertTrue(self.cleanup_calls)
    def test_restart_invalidates_and_cleans_never_resumes(self):
        self.create(); self.controller.close(cleanup_active=False); other = self.new_controller()
        results = other.startup_reconcile(); self.assertEqual(results[0]['state'], 'INVALIDATED')
        self.assertEqual(results[0]['invalid_reason'], 'CONTROLLER_RESTARTED')
        with self.assertRaises(agent.ControllerDenied): other.handle(self.req('start'))
    def test_unknown_cleanup_latches_and_hides_final_result(self):
        self.cleanup_result = {'resources_absent': False, 'status': 'UNKNOWN'}
        result = self.run_events(self.success_events()); self.assertEqual(result['state'], 'UNKNOWN')
        self.assertFalse(result['cleanup']); self.assertEqual(len(self.call('diagnostics')['events']), 1)
    def test_positive_absence_cannot_unlatch_unknown(self):
        self.create(); self.cleanup_result = {'resources_absent': True, 'status': 'UNKNOWN'}
        with self.assertRaises(agent.RuntimeUnknown): self.call('cleanup')
        self.cleanup_result = {'resources_absent': True, 'status': 'CLEANED'}
        with self.assertRaises(agent.RuntimeUnknown): self.call('cleanup')
        self.assertEqual(self.state_json()['state'], 'UNKNOWN')
    def test_success_requires_positive_cleanup_before_result(self):
        result = self.run_events(self.success_events()); self.assertEqual(result['state'], 'SUCCEEDED'); self.assertTrue(result['cleanup'])
        self.assertEqual(len(self.call('diagnostics')['events']), 2)
        result = self.call('cleanup'); self.assertEqual(result['state'], 'CLEANED'); self.assertEqual(result['outcome'], 'SUCCEEDED')
        self.assertIsNone(result['invalid_reason'])
    def test_cleanup_cannot_release_result_after_lease_expiry(self):
        def slow_clean(file):
            self.now += 11
            return {'resources_absent': True, 'status': 'CLEANED'}
        self.controller.cleanup = slow_clean
        result = self.run_events(self.success_events())
        self.assertEqual(result['state'], 'INVALIDATED')
        self.assertEqual(result['invalid_reason'], 'EXECUTION_LEASE_EXPIRED')
        self.assertEqual(len(self.call('diagnostics')['events']), 1)
    def test_stale_result_and_duplicate_thread_fail(self):
        events = self.success_events(); events[1]['execution_id'] = str(uuid.uuid4())
        self.assertEqual(self.run_events(events)['state'], 'FAILED')
    def test_result_before_thread_fails(self): self.assertEqual(self.run_events(self.success_events()[1:])['state'], 'FAILED')
    def test_duplicate_thread_fails(self): self.assertEqual(self.run_events([self.success_events()[0]] * 2)['state'], 'FAILED')
    def test_result_extra_fields_and_negative_usage_fail(self):
        events = self.success_events(); events[1]['usage'] = {'output_tokens': -1}
        self.assertEqual(self.run_events(events)['state'], 'FAILED')
    def test_failure_exit_hides_pending_result(self):
        result = self.run_events(self.success_events(), 1); self.assertEqual(result['state'], 'FAILED')
        self.assertEqual(len(self.call('diagnostics')['events']), 1)
    def test_output_bound_without_newline(self):
        self.process.stdout = io.BytesIO(b'a' * (agent.MAX_EVENTS_BYTES + 1))
        self.create(); self.call('start'); self.controller._threads[self.request['execution_id']].join(3)
        self.assertEqual(self.call('inspect')['invalid_reason'], 'AGENT_OUTPUT_LIMIT')
    def test_durable_state_mac_rejects_tampering(self):
        self.create(); file = self.state / self.request['execution_id'] / 'agent-state.json'; value = self.state_json()
        value['lease_expires_at'] += 100; file.write_text(json.dumps(value)); file.chmod(0o600)
        with self.assertRaisesRegex(agent.ControllerDenied, 'IDENTITY_CHANGED'): self.call('inspect')
    def test_actual_attestation_mismatch_creates_no_execution(self):
        self.manifest['changed'] = True
        with self.assertRaises(agent.RuntimeUnknown): self.create()
        self.assertFalse((self.state / self.request['execution_id']).exists())
    def test_partial_attestation_is_rejected_even_when_hash_pinned(self):
        self.manifest = {'schema': 'unit'}
        self.controller.config.attestation_sha256 = hashlib.sha256(agent.canonical(self.manifest)).hexdigest()
        self.credentials['attestation_sha256'] = self.controller.config.attestation_sha256
        with self.assertRaisesRegex(agent.RuntimeUnknown, 'INCOMPLETE'): self.create()
    def test_missing_owned_config_never_proves_cleanup(self):
        self.create(); (self.state / self.request['execution_id'] / 'owned-config.json').unlink()
        with self.assertRaisesRegex(agent.RuntimeUnknown, 'CONFIGURATION_UNKNOWN'): self.call('cancel')
        self.assertFalse(self.state_json()['cleanup_proven']); self.assertEqual(self.state_json()['state'], 'UNKNOWN')
    def test_missing_owned_config_on_restart_stays_unknown(self):
        self.create(); self.controller.close(cleanup_active=False)
        (self.state / self.request['execution_id'] / 'owned-config.json').unlink()
        self.assertEqual(self.new_controller().startup_reconcile()[0]['state'], 'UNKNOWN')
    def test_job_and_owned_config_changes_deny_start(self):
        self.create(); file = self.state / self.request['execution_id'] / 'job.json'
        value = json.loads(file.read_bytes()); value['request']['prompt'] = 'tampered'; file.write_text(json.dumps(value)); file.chmod(0o600)
        with self.assertRaisesRegex(agent.RuntimeUnknown, 'JOB_IDENTITY'): self.call('start')
        self.assertFalse(self.controller._processes)
    def test_one_workpackage_has_one_active_execution(self):
        self.create(); self.request['execution_id'] = str(uuid.uuid4()); self.request['attempt'] = 2
        with self.assertRaisesRegex(agent.ControllerDenied, 'WORKPACKAGE_ACTIVE'): self.create()
    def test_observer_creation_transition_cannot_double_consume_nonce(self):
        request = self.req('inspect'); entered = threading.Event(); release = threading.Event()
        original = self.controller._observer
        def paused(*args):
            entered.set(); self.assertTrue(release.wait(3)); return original(*args)
        with patch.object(self.controller, '_observer', paused), concurrent.futures.ThreadPoolExecutor(3) as pool:
            first = pool.submit(self.controller.handle, request); self.assertTrue(entered.wait(3))
            create = pool.submit(self.create); replay = pool.submit(self.controller.handle, request)
            release.set(); self.assertEqual(first.result(3)['state'], 'UNCREATED')
            self.assertEqual(create.result(3)['state'], 'CREATED')
            with self.assertRaisesRegex(agent.ControllerDenied, 'REPLAYED'): replay.result(3)
    def test_process_reaping_serializes_with_cancel(self):
        entered = threading.Event(); release = threading.Event()
        self.process = FakeProcess(self.success_events())
        def wait(timeout=None):
            entered.set(); self.assertTrue(release.wait(3)); return 0
        self.process.wait = wait
        self.create()
        root=self.state/self.request['execution_id'];fd=self.controller._lock(root)
        try:
            journal,identity=self.controller._publication_journal(root,self.controller._load(root))
            self.controller._atomic(journal,{**identity,'status':'COMPLETE','at':self.now})
        finally:os.close(fd)
        self.call('start'); self.assertTrue(entered.wait(3))
        with concurrent.futures.ThreadPoolExecutor(1) as pool:
            cancelled = pool.submit(self.call, 'cancel'); self.assertFalse(cancelled.done())
            release.set(); cancelled.result(3)
        self.controller._threads[self.request['execution_id']].join(3)
        self.assertFalse(self.process.terminated)
    def test_response_all_bindings_and_nonce_are_authenticated(self):
        request = self.req('inspect'); response = self.controller.response(request); signature = response.pop('signature')
        self.assertEqual(signature, backend.response_signature(backend.request_secret(self.template.secret, request), response))
        for key, value in backend.response_binding(request).items(): self.assertEqual(response[key], value)
    def test_template_nested_copy_cannot_be_widened(self):
        self.template.request['prompt'] = 'mutated'; self.template.environment['DOCKER_HOST'] = 'evil'
        self.assertEqual(self.controller.templates[self.template.workpackage_id].request['prompt'], 'approved work')
        self.create(); job = json.loads((self.state / self.request['execution_id'] / 'job.json').read_bytes())
        self.assertNotIn('DOCKER_HOST', job['environment']); self.assertEqual(job['environment']['CODEX_BUDGET_MODE'], 'enforce')
        self.assertNotIn(self.template.secret.hex(), json.dumps(job))
    def test_scope_registration_must_match_request(self):
        self.template = replace(self.template, scope_sha256='f' * 64)
        with self.assertRaisesRegex(agent.ControllerDenied, 'SCOPE'): self.new_controller()
    def test_http_handler_delegates_only_exact_authenticated_endpoint(self):
        handler_type = transport.agent_handler_for(self.controller); handler = handler_type.__new__(handler_type)
        request = self.req('inspect'); body = agent.canonical(request)
        handler.path = '/v1/agent-executions'; handler.headers = email.message.Message()
        handler.headers['Content-Type'] = 'application/json'; handler.headers['Content-Length'] = str(len(body))
        handler.rfile = io.BytesIO(body); handler.wfile = io.BytesIO(); handler.read_timer = Mock()
        handler.send_error = Mock(); handler.send_response = Mock(); handler.send_header = Mock(); handler.end_headers = Mock()
        handler.do_POST(); handler.send_response.assert_called_once_with(200)
        self.assertEqual(json.loads(handler.wfile.getvalue())['result']['state'], 'UNCREATED')
    def test_http_rejects_untrusted_listener_address(self):
        with self.assertRaises(agent.ControllerDenied): transport.agent_listener(self.controller, ('example.com', 8080))


class AgentCapacityRegressionTests(unittest.TestCase):
    setUp = AgentRuntimeTests.setUp
    tearDown = AgentRuntimeTests.tearDown
    clean = AgentRuntimeTests.clean
    new_controller = AgentRuntimeTests.new_controller
    req = AgentRuntimeTests.req
    call = AgentRuntimeTests.call
    state_json = AgentRuntimeTests.state_json

    def allocation(self, identity=None, attempt=1):
        value = self.req('cancel', execution_id=identity or str(uuid.uuid4()), attempt=attempt)
        value['signature'] = agent.request_signature(backend.request_secret(self.template.secret, value), value)
        return value

    def records(self):
        return [p for p in self.state.iterdir() if agent.finite._uuid(p.name)]

    def test_limit_rejects_before_any_execution_directory(self):
        with patch.object(agent, 'MAX_RECORDS', 1):
            self.controller.handle(self.allocation(attempt=1))
            rejected = self.allocation(attempt=2)
            with self.assertRaisesRegex(agent.ControllerDenied, 'COUNT_EXCEEDED'):
                self.controller.handle(rejected)
            self.assertFalse((self.state / rejected['execution_id']).exists())
            self.assertEqual(len(self.records()), 1)

    def test_concurrent_capacity_reservation_is_atomic(self):
        requests = [self.allocation(attempt=i + 1) for i in range(12)]
        def invoke(value):
            try: self.controller.handle(value); return ('accepted', value)
            except agent.ControllerDenied as error: return (error.code, value)
        with patch.object(agent, 'MAX_RECORDS', 3), concurrent.futures.ThreadPoolExecutor(12) as pool:
            results = list(pool.map(invoke, requests))
        self.assertEqual(sum(kind == 'accepted' for kind, _ in results), 3)
        self.assertEqual(len(self.records()), 3)
        for kind, value in results:
            if kind != 'accepted': self.assertFalse((self.state / value['execution_id']).exists())

    def test_restart_enforces_committed_record_capacity(self):
        with patch.object(agent, 'MAX_RECORDS', 1):
            self.controller.handle(self.allocation(attempt=1))
            self.controller.close(cleanup_active=False)
            other = self.new_controller(); other.startup_reconcile()
            request = self.allocation(attempt=2)
            with self.assertRaisesRegex(agent.ControllerDenied, 'COUNT_EXCEEDED'): other.handle(request)
            self.assertFalse((self.state / request['execution_id']).exists())

    def test_crash_before_state_binding_releases_only_empty_reservation(self):
        abandoned = self.state / str(uuid.uuid4()); abandoned.mkdir(mode=0o700)
        self.controller.close(cleanup_active=False)
        other = self.new_controller()
        with patch.object(agent, 'MAX_RECORDS', 1):
            other.startup_reconcile()
            self.assertFalse(abandoned.exists())
            self.assertEqual(other.handle(self.allocation())['state'], 'INVALIDATED')
            self.assertEqual(len(self.records()), 1)

    def test_unbound_reservation_recovery_does_not_leak_capacity(self):
        request = self.allocation(); root = self.state / request['execution_id']
        fd = self.controller._lock(root)
        try: self.controller._load(root, request)
        finally: os.close(fd)
        self.controller.close(cleanup_active=False); other = self.new_controller()
        with patch.object(agent, 'MAX_RECORDS', 1):
            other.startup_reconcile(); self.assertFalse(root.exists())
            self.assertEqual(other.handle(self.allocation(attempt=2))['state'], 'INVALIDATED')

    def test_consumed_nonce_reservation_is_not_freed_on_restart(self):
        request = self.allocation(); root = self.state / request['execution_id']; fd = self.controller._lock(root)
        try:
            state = self.controller._load(root, request); self.controller._consume(root, request, state)
        finally: os.close(fd)
        self.controller.close(cleanup_active=False); other = self.new_controller()
        with patch.object(agent, 'MAX_RECORDS', 1):
            other.startup_reconcile(); self.assertTrue(root.exists())
            with self.assertRaisesRegex(agent.ControllerDenied, 'COUNT_EXCEEDED'): other.handle(self.allocation(attempt=2))
            with self.assertRaisesRegex(agent.ControllerDenied, 'REPLAYED'): other.handle(request)

    def test_suspicious_reservation_is_never_silently_freed(self):
        abandoned = self.state / str(uuid.uuid4()); abandoned.mkdir(mode=0o700)
        (abandoned / 'owned-config.json').write_text('{}'); (abandoned / 'owned-config.json').chmod(0o600)
        self.controller.close(cleanup_active=False); other = self.new_controller()
        result = other.startup_reconcile()
        self.assertTrue(abandoned.exists()); self.assertEqual(result[0]['state'], 'UNKNOWN')


class AgentPublicationFenceRegressionTests(unittest.TestCase):
    setUp=AgentRuntimeTests.setUp
    tearDown=AgentRuntimeTests.tearDown
    clean=AgentRuntimeTests.clean
    new_controller=AgentRuntimeTests.new_controller
    req=AgentRuntimeTests.req
    call=AgentRuntimeTests.call
    state_json=AgentRuntimeTests.state_json
    create=AgentRuntimeTests.create
    run_events=AgentRuntimeTests.run_events
    success_events=AgentRuntimeTests.success_events

    def prepare_publication(self):
        import cdo_broker as broker
        (self.root/'internal').mkdir();(self.root/'internal/a.go').write_text('before')
        self.controller.close(cleanup_active=False)
        self.template=replace(self.template,environment={**self.template.environment,'CDO_CERTIFICATION_ROOT':str(self.root)})
        self.controller=self.new_controller();self.controller.start_watchdog();self.create()
        root=self.state / self.request['execution_id'];fd=self.controller._lock(root)
        try:
            state=self.controller._load(root);state['state']='RUNNING';self.controller._atomic(root/'agent-state.json',state)
        finally:os.close(fd)
        environment={'CDO_RUNTIME_CONTROL_ROOT':str(root),'CDO_RUNTIME_LEASE_FILE':str(root/'lease.json'),
                     'CDO_RUNTIME_OWNED_CONFIG':str(root/'owned-config.json')}
        config={'root':str(self.root),'role':'layer','phase':'implementation','write_paths':['internal/a.go'],
                'execution_id':self.request['execution_id'],'attempt':1,'image':'fixture',
                'state_dir':str(root),'evidence':str(root/'evidence.json'),'approved_commands':[['go','test','./...']]}
        return broker,root,environment,config

    def test_gateway_cancel_and_publisher_share_permanent_fence(self):
        broker,root,environment,config=self.prepare_publication()
        entered=threading.Event();release=threading.Event()
        def checkpoint(phase,policy):
            if phase=='publication-validation':entered.set();self.assertTrue(release.wait(3))
        with patch.dict(os.environ,environment,clear=True),patch.object(broker,'lifecycle_checkpoint',side_effect=checkpoint),concurrent.futures.ThreadPoolExecutor(1) as pool:
            policy=broker.Policy(config);publisher=broker.Publisher(policy,{'internal/a.go':hashlib.sha256(b'before').hexdigest()})
            future=pool.submit(publisher.publish,{'internal/a.go':{'op':'write','data':'YWZ0ZXI='}})
            self.assertTrue(entered.wait(3))
            try:self.assertEqual(self.call('cancel')['invalid_reason'],'EXECUTION_CANCELLED')
            finally:release.set()
            with self.assertRaises((broker.Denied,broker.LimitExceeded)):future.result(3)
            self.assertEqual(json.loads(policy.publication_journal.read_bytes())['status'],'FAILED')
            self.assertEqual((self.root/'internal/a.go').read_text(),'before')

    def test_crash_after_lease_fence_before_state_write_never_revives(self):
        broker,root,environment,config=self.prepare_publication()
        class Crash(BaseException):pass
        original=self.controller._atomic
        def interrupted(file,value):
            if file.name=='agent-state.json' and value.get('invalid_reason')=='EXECUTION_CANCELLED':raise Crash()
            return original(file,value)
        with patch.object(self.controller,'_atomic',side_effect=interrupted),self.assertRaises(Crash):self.call('cancel')
        self.assertEqual(json.loads((root/'lease.json').read_bytes())['status'],'INVALIDATED')
        with patch.dict(os.environ,environment,clear=True):
            with self.assertRaises(broker.LimitExceeded):broker.Policy(config)
        with self.assertRaises(agent.RuntimeUnknown):self.call('inspect')
        self.assertEqual(self.state_json()['state'],'UNKNOWN')

    def test_late_journal_invalidation_hides_coordinator_success(self):
        def late_invalidation(file):
            import cdo_broker as broker
            root=self.state/self.request['execution_id']
            state=self.controller._load(root);journal,identity=self.controller._publication_journal(root,state)
            with broker.publication_lock(root):broker.invalidate_publication_journal(journal,identity)
            return {'resources_absent':True,'status':'CLEANED'}
        self.controller.cleanup=late_invalidation
        result=self.run_events(self.success_events())
        self.assertEqual(result['state'],'UNKNOWN');self.assertFalse(result['cleanup'])
        self.assertIsNone(result['cleanup_evidence'])
        self.assertFalse(result['reconciliation_evidence']['no_unknown'])
        self.assertEqual(len(self.call('diagnostics')['events']),1)

    def test_publication_lock_wait_cannot_outlive_execution_lease(self):
        from contextlib import contextmanager
        import cdo_broker as broker
        original=broker.publication_lock
        @contextmanager
        def delayed(root):
            with original(root):
                self.now+=11
                yield
        with patch.object(broker,'publication_lock',delayed):result=self.run_events(self.success_events())
        self.assertNotEqual(result['state'],'SUCCEEDED')
        self.assertEqual(len(self.call('diagnostics')['events']),1)


class AgentWholeCleanupRegressionTests(unittest.TestCase):
    setUp=AgentRuntimeTests.setUp
    tearDown=AgentRuntimeTests.tearDown
    clean=AgentRuntimeTests.clean
    new_controller=AgentRuntimeTests.new_controller
    req=AgentRuntimeTests.req
    call=AgentRuntimeTests.call
    state_json=AgentRuntimeTests.state_json
    create=AgentRuntimeTests.create
    run_events=AgentRuntimeTests.run_events
    success_events=AgentRuntimeTests.success_events

    def activate(self,process=None):
        self.create();self.process=process or FakeProcess();self.process.pid=424242
        root=self.state/self.request['execution_id'];fd=self.controller._lock(root)
        try:
            state=self.controller._load(root);state.update(state='RUNNING',launch_attempted=True)
            if 'coordinator_pid' in state:state['coordinator_pid']=self.process.pid
            self.controller._atomic(root/'agent-state.json',state)
        finally:os.close(fd)
        self.controller._processes[self.request['execution_id']]=self.process
        return root

    def positive_helpers(self):
        self.controller.host_cleanup=lambda *a,**k:{'coordinator_absent':True,'node_groups_absent':True,'private_storage_absent':True,'no_unknown':True}

    def test_owned_oci_absence_is_not_missing_node_teardown_proof(self):
        self.activate()
        self.controller.teardown=lambda *a,**k:(_ for _ in ()).throw(agent.RuntimeUnknown('HOST_TEARDOWN_MISSING'))
        with self.assertRaises(agent.RuntimeUnknown):self.call('cleanup')
        self.assertFalse(self.state_json()['cleanup_proven'])

    def test_missing_handle_and_missing_durable_authority_never_prove_absence(self):
        self.activate();self.controller._processes.clear()
        self.controller.host_cleanup=lambda *a,**k:(_ for _ in ()).throw(agent.RuntimeUnknown('HOST_PROCESS_OWNER_MISSING'))
        with self.assertRaises(agent.RuntimeUnknown):self.call('cleanup')
        self.assertFalse(self.state_json()['cleanup_proven'])

    def test_coordinator_timeout_does_not_destroy_teardown_producer(self):
        process=FakeProcess(code=None);actions=[]
        process.terminate=lambda:actions.append('TERM');process.kill=lambda:actions.append('KILL')
        def wait(timeout=None):raise agent.subprocess.TimeoutExpired('owned-coordinator',timeout)
        process.wait=wait;self.activate(process);self.positive_helpers()
        try:self.call('cleanup')
        except Exception:pass
        self.assertEqual(actions,[],'gateway must not kill the coordinator before its own teardown protocol')
        self.assertFalse(self.state_json()['cleanup_proven'])

    def test_graceful_and_delayed_exit_use_whole_cleanup_budget(self):
        process=FakeProcess(code=None);actions=[]
        process.terminate=lambda:actions.append('TERM');process.kill=lambda:actions.append('KILL')
        def wait(timeout=None):actions.append(('wait',timeout));return 0
        process.wait=wait;self.activate(process);self.positive_helpers()
        result=self.call('cleanup');self.assertTrue(result['cleanup_proven'])
        self.assertEqual(actions,[('wait',15)])

    def test_private_storage_presence_cannot_be_hidden_by_positive_flags(self):
        root=self.activate();self.positive_helpers()
        job=json.loads((root/'job.json').read_bytes());nonce=job.get('process_owner',{}).get('owner_nonce','0'*32)
        private=root/('private-execution-'+nonce);private.mkdir(mode=0o700);(private/'secret-state').write_text('disposable')
        with self.assertRaises(agent.RuntimeUnknown):self.call('cleanup')
        self.assertTrue(private.exists());self.assertFalse(self.state_json()['cleanup_proven'])

    def test_docker_unknown_never_removes_private_state_or_releases_success(self):
        self.activate();self.positive_helpers();called=[]
        self.cleanup_result={'resources_absent':False,'status':'UNKNOWN'}
        self.controller.host_cleanup=lambda *a,**k:called.append('storage') or {'coordinator_absent':True,'node_groups_absent':True,'private_storage_absent':True,'no_unknown':True}
        with self.assertRaises(agent.RuntimeUnknown):self.call('cleanup')
        self.assertEqual(called,[]);self.assertFalse(self.state_json()['cleanup_proven'])

    def test_gateway_interruption_latches_unknown_without_signaling(self):
        process=FakeProcess(code=None);actions=[]
        process.terminate=lambda:actions.append('TERM');process.kill=lambda:actions.append('KILL')
        process.wait=lambda **k:(_ for _ in ()).throw(KeyboardInterrupt())
        self.activate(process);self.positive_helpers()
        try:self.call('cleanup')
        except BaseException as error:self.assertIsInstance(error,agent.RuntimeUnknown)
        else:self.fail('interrupted observation cannot prove cleanup')
        self.assertFalse(self.state_json()['cleanup_proven']);self.assertEqual(actions,[])

    def test_crash_restart_missing_positive_host_evidence_stays_unknown(self):
        self.activate();self.controller.close(cleanup_active=False);other=self.new_controller()
        other.host_cleanup=lambda *a,**k:(_ for _ in ()).throw(agent.RuntimeUnknown('HOST_PROCESS_OBSERVATION_UNKNOWN'))
        results=other.startup_reconcile();self.assertEqual(results[0]['state'],'UNKNOWN')
        self.assertFalse(self.state_json()['cleanup_proven'])

    def test_registration_denial_revokes_lease_and_latches_unknown(self):
        from cdo_runtime_coordinator import CoordinatorDenied
        self.create()
        self.controller.registration=lambda *a,**k:(_ for _ in ()).throw(CoordinatorDenied('COORDINATOR_REGISTRATION_TIMEOUT'))
        try:self.call('start')
        except Exception as error:self.assertIsInstance(error,agent.RuntimeUnknown)
        else:self.fail('registration denial must stop admission')
        state=self.state_json();self.assertEqual(state['state'],'UNKNOWN');self.assertFalse(state['cleanup_proven'])
        lease=json.loads((self.state/self.request['execution_id']/'lease.json').read_bytes())
        self.assertEqual(lease['status'],'INVALIDATED');self.assertFalse(self.controller._threads)

    def test_ack_is_after_durable_exact_process_binding(self):
        self.create();observed=[]
        def registration(file,process):
            state=self.controller._load(file.parent)
            self.assertTrue(state['launch_attempted']);self.assertEqual(state['coordinator_pid'],process.pid)
            self.assertFalse(state['coordinator_reaped']);self.assertEqual(state['process_owner'],json.loads(file.read_bytes())['process_owner'])
            observed.append('ACK');return self.controller._process_binding(state)
        self.controller.registration=registration;self.call('start')
        self.controller._threads[self.request['execution_id']].join(3)
        self.assertEqual(observed,['ACK'])

    def test_delayed_node_teardown_precedes_oci_then_private_cleanup(self):
        process=FakeProcess(code=None);root=self.activate(process);actions=[]
        original_teardown=self.controller.teardown
        process.wait=lambda timeout=None:actions.append(('wait',timeout)) or 0
        self.controller.teardown=lambda *a,**k:actions.append('Node absent') or original_teardown(*a,**k)
        self.controller.cleanup=lambda *a,**k:actions.append('OCI absent') or {'resources_absent':True,'status':'CLEANED'}
        self.controller.host_cleanup=lambda *a,**k:actions.append('private absent') or {'coordinator_absent':True,'node_groups_absent':True,'private_storage_absent':True,'no_unknown':True}
        result=self.call('cleanup');self.assertTrue(result['cleanup'])
        self.assertEqual(actions,[('wait',15),'Node absent','OCI absent','private absent'])
        self.assertTrue(result['cleanup_evidence']['coordinator_reaped'])
        self.assertEqual(result['cleanup_evidence']['teardown_sha256'],hashlib.sha256(agent.canonical(json.loads((root/'host-teardown.json').read_bytes()))).hexdigest())

    def test_forced_node_receipt_allows_cleanup_but_never_success(self):
        original=self.controller.teardown
        def forced(file,expected):
            proof=original(file,expected);proof['node_exit_code']=-9
            proof['terminal_cause']='COORDINATOR_NODE_FORCED';self.controller._atomic(file.parent/'host-teardown.json',proof)
            return proof
        self.controller.teardown=forced
        result=self.run_events(self.success_events(),1)
        self.assertEqual(result['state'],'FAILED');self.assertTrue(result['cleanup'])
        self.assertEqual(result['invalid_reason'],'COORDINATOR_NODE_FORCED')
        self.assertEqual(len(self.call('diagnostics')['events']),1)

    def test_restart_positive_observation_does_not_fabricate_reaping(self):
        root=self.activate();self.controller.close(cleanup_active=False);other=self.new_controller()
        original=other.host_cleanup
        def recovered(file,expected,**kwargs):
            self.assertFalse(expected['coordinator_reaped']);self.assertTrue(kwargs['oci_absent'])
            result=original(file,expected,**kwargs)
            proof=json.loads((root/'host-teardown.json').read_bytes());proof.update(node_reaped=False,proof_kind='observed-recovery')
            other._atomic(root/'host-teardown.json',proof);return result
        other.host_cleanup=recovered
        result=other.startup_reconcile()[0];self.assertTrue(result['cleanup'])
        self.assertFalse(result['cleanup_evidence']['coordinator_reaped'])
        self.assertTrue(result['cleanup_evidence']['coordinator_absence_observed'])
        self.assertEqual(result['invalid_reason'],'CONTROLLER_RESTARTED')

    def test_unknown_reconciliation_records_physical_proof_without_admission(self):
        self.activate();self.positive_helpers();self.cleanup_result={'resources_absent':False,'status':'UNKNOWN'}
        with self.assertRaises(agent.RuntimeUnknown):self.call('cleanup')
        self.cleanup_result={'resources_absent':True,'status':'CLEANED'}
        with self.assertRaises(agent.RuntimeUnknown):self.call('cleanup')
        result=self.call('inspect');self.assertEqual(result['state'],'UNKNOWN');self.assertFalse(result['cleanup'])
        self.assertIsNone(result['cleanup_evidence']);self.assertTrue(result['reconciliation_evidence']['owned_oci_absent'])
        self.assertFalse(result['reconciliation_evidence']['no_unknown'])

    def test_partial_host_proof_never_admits_cleanup(self):
        self.activate()
        self.controller.host_cleanup=lambda *a,**k:{'coordinator_absent':True,'node_groups_absent':True,'private_storage_absent':False,'no_unknown':True}
        with self.assertRaises(agent.RuntimeUnknown):self.call('cleanup')
        self.assertFalse(self.state_json()['cleanup_proven'])

    def test_postspawn_monitor_failure_has_bounded_cleanup_owner(self):
        self.create()
        with patch.object(agent.threading,'Thread') as thread:
            thread.return_value.start.side_effect=RuntimeError('unit monitor admission failure')
            try:self.call('start')
            except Exception as error:self.assertIsInstance(error,agent.RuntimeUnknown)
            else:self.fail('monitor failure must stop admission')
        self.assertEqual(self.state_json()['state'],'UNKNOWN')
        self.assertTrue(self.cleanup_calls);self.assertIsNotNone(self.state_json()['reconciliation_evidence'])
        self.assertFalse(self.state_json()['cleanup_proven'])

    def test_postspawn_registration_mismatch_still_reconciles(self):
        self.create()
        self.controller.registration=lambda file,process:{**self.controller._process_binding(self.controller._load(file.parent)),'coordinator_pid':process.pid+1}
        with self.assertRaises(agent.RuntimeUnknown):self.call('start')
        self.assertTrue(self.cleanup_calls);self.assertIsNotNone(self.state_json()['reconciliation_evidence'])
        self.assertEqual(self.state_json()['state'],'UNKNOWN')

    def test_postspawn_unresolved_cleanup_latches_host_health(self):
        self.create();self.process.wait=lambda timeout=None:(_ for _ in ()).throw(agent.subprocess.TimeoutExpired('fixture',timeout))
        with patch.object(agent.threading,'Thread') as thread:
            thread.return_value.start.side_effect=RuntimeError('unit monitor admission failure')
            try:self.call('start')
            except Exception as error:self.assertIsInstance(error,agent.RuntimeUnknown)
            else:self.fail('monitor failure must stop admission')
        self.assertIsNotNone(self.controller._health);self.assertFalse(self.state_json()['cleanup_proven'])

    def test_never_admitted_stale_node_outcome_is_not_absence(self):
        root=self.state/self.request['execution_id'];root.mkdir(mode=0o700)
        self.controller._load(root,self.req('cancel'))
        self.controller._atomic(root/'host-node-outcome.json',{'stale':'fixture'})
        with self.assertRaises(agent.RuntimeUnknown):self.call('cancel')
        self.assertFalse(self.state_json()['cleanup_proven'])


if __name__ == '__main__': unittest.main()
