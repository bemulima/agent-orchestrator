import json, os, pathlib, tempfile, time, unittest
from unittest.mock import patch
from cdo_runtime_coordinator import assert_lease, environment_lease, private_json, CoordinatorDenied, run_job

OWNER={"version":1,"controller_id":"a"*64,"boot_id":"7e07ad63-a833-41ca-a988-e0c9a2191204","owner_nonce":"b"*32}


class LeaseTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.root = pathlib.Path(self.tmp.name).resolve()
        self.file = self.root / 'lease.json'
        self.value = {'execution_id': 'execution-fixture', 'attempt': 1, 'expires_at': time.time()+60, 'status': 'ACTIVE'}
        self.save()
    def tearDown(self): self.tmp.cleanup()
    def save(self):
        self.file.write_text(json.dumps(self.value)); self.file.chmod(0o600)
    def test_active_lease(self):
        self.assertEqual(assert_lease(str(self.file), 'execution-fixture', 1), self.value)
    def test_identity_and_attempt_drift(self):
        for execution, attempt in [('other', 1), ('execution-fixture', 2)]:
            with self.assertRaisesRegex(CoordinatorDenied, 'IDENTITY_CHANGED'): assert_lease(str(self.file), execution, attempt)
    def test_expiry_invalid_status_and_unknown_fields(self):
        for changes in [{'expires_at': time.time()-1}, {'status':'CANCELLED'}, {'unknown':True}]:
            original = dict(self.value); self.value.update(changes); self.save()
            with self.assertRaises(CoordinatorDenied): assert_lease(str(self.file), 'execution-fixture', 1)
            self.value = original
    def test_links_permissions_and_missing_file(self):
        self.file.chmod(0o644)
        with self.assertRaises(CoordinatorDenied): assert_lease(str(self.file), 'execution-fixture', 1)
        self.file.chmod(0o600); link=self.root/'link'; link.symlink_to(self.file)
        with self.assertRaises(CoordinatorDenied): assert_lease(str(link), 'execution-fixture', 1)
        os.link(self.file, self.root/'hard')
        with self.assertRaises(CoordinatorDenied): assert_lease(str(self.file), 'execution-fixture', 1)
        with self.assertRaises(CoordinatorDenied): assert_lease(str(self.root/'missing'), 'execution-fixture', 1)
    def test_complete_transport_never_has_lease_fallback(self):
        with patch.dict(os.environ, {'CDO_RUNTIME_OWNED_CONFIG': '/private/owned.json'}, clear=True):
            with self.assertRaisesRegex(CoordinatorDenied, 'ABSENT'): environment_lease('execution-fixture')
    def test_duplicate_or_nonfinite_private_config_denied(self):
        for text in ['{"a":1,"a":2}','{"a":NaN}']:
            self.file.write_text(text)
            with self.assertRaises(CoordinatorDenied):private_json(str(self.file),4096)
    def test_job_unknown_environment_and_production_mode_denied_before_process(self):
        job={'process_owner':dict(OWNER),'version':1,'request':{},'environment':{'NODE_OPTIONS':'untrusted'},'lease_file':str(self.file),'owned_config':str(self.file),'execution_root':str(self.root)}
        file=self.root/'job.json';file.write_text(json.dumps(job));file.chmod(0o600)
        with patch('cdo_runtime_coordinator.subprocess.Popen',side_effect=AssertionError('process reached')),self.assertRaisesRegex(CoordinatorDenied,'JOB_INVALID'):run_job(str(file))
        job['environment']={'CDO_SANDBOX_EXECUTION_MODE':'production','CDO_CERTIFICATION_PROVIDER_URL':'http://127.0.0.1:1/v1'};file.write_text(json.dumps(job))
        with patch('cdo_runtime_coordinator.subprocess.Popen',side_effect=AssertionError('process reached')),self.assertRaisesRegex(CoordinatorDenied,'DETERMINISTIC_CERTIFICATION_REQUIRED'):run_job(str(file))
    def _live_owned_coordinator(self,file,marker):
        # LIVE-ONLY fixture. Never selected by the source-only focused commands.
        # Match gateway lifecycle: an owned coordinator session, durable READY
        # registration/ACK, then the actual Node session and group teardown.
        import subprocess,sys,cdo_runtime_coordinator as coordinator
        process=subprocess.Popen([str(pathlib.Path(sys.executable).resolve()),str(pathlib.Path(coordinator.__file__).resolve()),str(file)],stdin=subprocess.DEVNULL,stdout=subprocess.PIPE,stderr=subprocess.PIPE,start_new_session=True)
        stdout=stderr=b''
        try:
            coordinator.await_coordinator_registration(file,process)
            end=time.monotonic()+3
            while not marker.exists():
                if time.monotonic()>=end:raise AssertionError('actual owned child did not enter fixture execution')
                time.sleep(.025)
            # Expire only after actual child behavior is observable. Atomic lease
            # replacement avoids fixture JSON-write races masquerading as expiry.
            self.value['expires_at']=time.time()-1;coordinator.atomic_private(self.file,self.value)
            stdout,stderr=process.communicate(timeout=coordinator.COORDINATOR_CLEANUP_WAIT_SECONDS)
            self.assertNotEqual(process.returncode,0)
            self.assertIn(b'EXECUTION_LEASE_EXPIRED',stderr)
            _,_,binding=coordinator.job_binding(file)
            expected={'job_sha256':binding['job_sha256'],'owned_config_sha256':binding['owned_config_sha256'],'process_owner':binding['process_owner'],'coordinator_pid':process.pid,'coordinator_reaped':True}
            coordinator.validate_teardown(file,expected)
        finally:
            # No alternate kill, EPERM bypass, or missing-observation PASS. If the
            # coordinator cannot prove teardown this fixture remains a failure.
            if process.returncode is None:
                try:stdout,stderr=process.communicate(timeout=coordinator.COORDINATOR_CLEANUP_WAIT_SECONDS)
                except subprocess.TimeoutExpired:pass
            evidence={name:json.loads((self.root/name).read_text()) if (self.root/name).exists() else None for name in ('child-identity.json','host-signal-diagnostics.json','host-process-owner.json','host-teardown.json')}
            print('OWNED_SIGNAL_DIAGNOSTICS='+json.dumps({'evidence':evidence,'coordinator_pid':process.pid,'coordinator_exit_code':process.returncode,'stderr':stderr[:4096].decode(errors='replace')},sort_keys=True),file=sys.stderr)
    def test_expired_lease_forces_owned_term_ignoring_child_promptly(self):
        import sys
        identity=self.root/'child-identity.json'
        entry=self.root/'fixture.py';entry.write_text('import signal,time,sys,json,os,pathlib\nsignal.signal(signal.SIGTERM,signal.SIG_IGN)\np=pathlib.Path('+repr(str(identity))+');p.write_text(json.dumps({"pid":os.getpid(),"ppid":os.getppid(),"pgid":os.getpgrp(),"sid":os.getsid(0),"uid":os.getuid(),"euid":os.geteuid(),"ignores_sigterm":True}));p.chmod(0o600)\nsys.stdin.read()\nwhile True:time.sleep(.1)\n');entry.chmod(0o600)
        owned=self.root/'owned.json';owned.write_text('{}');owned.chmod(0o600)
        environment={'CDO_SANDBOX_EXECUTION_MODE':'hardened-certification','CDO_CERTIFICATION_PROVIDER_URL':'http://127.0.0.1:1/v1','CDO_RUNTIME_NODE_EXECUTABLE':str(pathlib.Path(sys.executable).resolve()),'CDO_RUNTIME_NODE_ENTRYPOINT':str(entry),'CDO_BROKER_PYTHON':str(pathlib.Path(sys.executable).resolve())}
        job={'process_owner':dict(OWNER),'version':1,'request':{'execution_id':'execution-fixture','attempt':1},'environment':environment,'lease_file':str(self.file),'owned_config':str(owned),'execution_root':str(self.root)}
        file=self.root/'job.json';file.write_text(json.dumps(job));file.chmod(0o600);started=time.monotonic()
        self._live_owned_coordinator(file,identity)
        self.assertLess(time.monotonic()-started,12)
    def test_job_authority_aliases_and_budget_cannot_change(self):
        environment={'CDO_SANDBOX_EXECUTION_MODE':'hardened-certification','CDO_CERTIFICATION_PROVIDER_URL':'http://127.0.0.1:1/v1','CODEX_SOL_MAX_RUNS_5H':'40'}
        job={'process_owner':dict(OWNER),'version':1,'request':{},'environment':environment,'lease_file':str(self.file),'owned_config':str(self.file),'execution_root':str(self.root)}
        file=self.root/'job.json';file.write_text(json.dumps(job));file.chmod(0o600)
        with patch('cdo_runtime_coordinator.subprocess.Popen',side_effect=AssertionError('process reached')),self.assertRaisesRegex(CoordinatorDenied,'AUTHORITY_BINDING_CHANGED'):run_job(str(file))
    def test_group_cleanup_after_leader_exits_preserves_unrelated_process(self):
        import subprocess,sys,cdo_runtime_coordinator as coordinator
        marker=self.root/'descendant.pid'
        descendant="import signal,time,os,pathlib; signal.signal(signal.SIGTERM,signal.SIG_IGN); pathlib.Path("+repr(str(marker))+").write_text(str(os.getpid()));\nwhile True:time.sleep(.1)\n"
        entry=self.root/'leader.py';entry.write_text("import subprocess,sys,time\nsubprocess.Popen([sys.executable,'-c',"+repr(descendant)+"],close_fds=True)\nsys.stdin.read()\nwhile True:time.sleep(.1)\n");entry.chmod(0o600)
        owned=self.root/'owned.json';owned.write_text('{}');owned.chmod(0o600)
        environment={'CDO_SANDBOX_EXECUTION_MODE':'hardened-certification','CDO_CERTIFICATION_PROVIDER_URL':'http://127.0.0.1:1/v1','CDO_RUNTIME_NODE_EXECUTABLE':str(pathlib.Path(sys.executable).resolve()),'CDO_RUNTIME_NODE_ENTRYPOINT':str(entry),'CDO_BROKER_PYTHON':str(pathlib.Path(sys.executable).resolve())}
        job={'process_owner':dict(OWNER),'version':1,'request':{'execution_id':'execution-fixture','attempt':1},'environment':environment,'lease_file':str(self.file),'owned_config':str(owned),'execution_root':str(self.root)};file=self.root/'job.json';file.write_text(json.dumps(job));file.chmod(0o600)
        unrelated=subprocess.Popen([sys.executable,'-c','import time;time.sleep(30)'],start_new_session=True)
        try:
            self._live_owned_coordinator(file,marker)
            self.assertTrue(marker.exists());pid=int(marker.read_text())
            # Absence comes from a complete validated original observer snapshot;
            # nonzero observer exit/EPERM is UNKNOWN and cannot satisfy the test.
            self.assertFalse(any(member['pid']==pid for member in coordinator.group_members(None)))
            self.assertIsNone(unrelated.poll())
        finally:
            unrelated.terminate();unrelated.wait(timeout=2)


class SignalHandlerRegressionTest(unittest.TestCase):
    def run_case(self,expected_error='COORDINATOR_INTERRUPTED',state='S',deny=False,wrong_group=False,preflight_denied=False,registration_denied=False,setup_write_denied=False,kill_delay=0):
        import io,sys,threading,types,signal
        with tempfile.TemporaryDirectory() as directory:
            root=pathlib.Path(directory).resolve();root.chmod(0o700)
            lease=root/'lease.json';lease.write_text(json.dumps({'execution_id':'signal-fixture','attempt':1,'expires_at':time.time()+60,'status':'ACTIVE'}));lease.chmod(0o600)
            owned=root/'owned.json';owned.write_text('{}');owned.chmod(0o600)
            entry=root/'entry.py';entry.write_text('fixture');entry.chmod(0o600)
            environment={'CDO_SANDBOX_EXECUTION_MODE':'hardened-certification','CDO_CERTIFICATION_PROVIDER_URL':'http://127.0.0.1:1/v1','CDO_RUNTIME_NODE_EXECUTABLE':str(pathlib.Path(sys.executable).resolve()),'CDO_RUNTIME_NODE_ENTRYPOINT':str(entry),'CDO_BROKER_PYTHON':str(pathlib.Path(sys.executable).resolve())}
            job=root/'job.json';job.write_text(json.dumps({'process_owner':dict(OWNER),'version':1,'request':{'execution_id':'signal-fixture','attempt':1},'environment':environment,'lease_file':str(lease),'owned_config':str(owned),'execution_root':str(root)}));job.chmod(0o600)
            handlers={};calls=[];injected=[False];write_denied=[False];delay=[kill_delay]
            import cdo_runtime_coordinator as coordinator
            real_atomic=coordinator.atomic_private
            def atomic(file,value):
                if setup_write_denied and value.get('phase')=='NODE_RUNNING' and not write_denied[0]:
                    write_denied[0]=True;raise OSError('fixture durable write failure')
                return real_atomic(file,value)
            class AuditLock:
                def __init__(self):self.lock=threading.Lock();self.owner=None
                def __enter__(self):
                    if self.owner==threading.get_ident():raise RuntimeError('SIGNAL_HANDLER_ACQUIRED_CLEANUP_LOCK')
                    self.lock.acquire();self.owner=threading.get_ident();return self
                def __exit__(self,*_):self.owner=None;self.lock.release()
            class ImmediateThread:
                def __init__(self,target,**_):self.target=target;self.actual=None
                def start(self):
                    if self.target.__name__=='force':self.actual=threading.Thread(target=self.target);self.actual.start()
                    elif self.target.__name__!='watch':self.target()
                def join(self,**options):
                    if self.actual:self.actual.join(**options)
                def is_alive(self):return self.actual is not None and self.actual.is_alive()
            class Child:
                pid=424242
                stdin=io.BytesIO()
                def wait(self,**_):states[0]='ABSENT';return 0
            def install(signum,handler):handlers[signum]=handler
            def killpg(pid,signum):
                calls.append((pid,signum))
                if deny:raise PermissionError(1,'Operation not permitted')
                if signum==signal.SIGKILL:states[0]='KILLING' if delay[0] else 'Z'
                if not injected[0]:
                    injected[0]=True
                    for signum in (signal.SIGTERM,signal.SIGINT,signal.SIGTERM):handlers[signum](signum,None)
            runtime_threads=types.SimpleNamespace(Lock=AuditLock,Event=threading.Event,Thread=ImmediateThread)
            states=[state]
            def members(group):
                if group==os.getpgrp():
                    if preflight_denied:raise CoordinatorDenied('COORDINATOR_GROUP_OBSERVATION_UNKNOWN')
                    return [{'pid':os.getpid(),'ppid':os.getppid(),'pgid':group,'uid':os.geteuid(),'state':'S'}]
                if states[0]=='KILLING':
                    delay[0]-=1
                    if delay[0]<=0:states[0]='Z'
                if group is None and states[0]=='ABSENT':return []
                return [{'pid':424242,'ppid':os.getpid(),'pgid':424242,'uid':os.geteuid(),'state':states[0]}]
            with patch('cdo_runtime_coordinator.coordinator_identity',return_value={'pid':434343,'pgid':434343,'sid':434343,'uid':os.geteuid()}),patch('cdo_runtime_coordinator.await_registration_ack',side_effect=CoordinatorDenied('COORDINATOR_REGISTRATION_TIMEOUT') if registration_denied else None),patch('cdo_runtime_coordinator.atomic_private',side_effect=atomic),patch('cdo_runtime_coordinator.threading',runtime_threads),patch('cdo_runtime_coordinator.subprocess.Popen',return_value=Child()) as process_factory,patch('cdo_runtime_coordinator.signal.signal',side_effect=install),patch('cdo_runtime_coordinator.os.killpg',side_effect=killpg),patch('cdo_runtime_coordinator.wait_unreaped',return_value=None),patch('cdo_runtime_coordinator.time.sleep',return_value=None),patch('cdo_runtime_coordinator.group_members',side_effect=members),patch('cdo_runtime_coordinator.os.getpgid',return_value=424243 if wrong_group else 424242),patch('cdo_runtime_coordinator.os.getsid',return_value=424242):
                try:
                    if expected_error:
                        with self.assertRaisesRegex(CoordinatorDenied,expected_error):run_job(str(job))
                    else:self.assertEqual(run_job(str(job)),0)
                except RuntimeError as error:self.fail(str(error))
            file=root/'host-signal-diagnostics.json';diagnostics=json.loads(file.read_text()) if file.exists() else {'events':[]}
            diagnostics['execution_popen_calls']=process_factory.call_count
            diagnostics['teardown_receipt_present']=(root/'host-teardown.json').is_file()
            return calls,diagnostics
    def test_cleanup_proof_registration_timeout_cannot_launch_node(self):
        calls,diagnostics=self.run_case(expected_error='REGISTRATION_TIMEOUT',registration_denied=True)
        self.assertEqual(calls,[]);self.assertEqual(diagnostics['execution_popen_calls'],0)
        self.assertFalse(diagnostics['teardown_receipt_present'])
    def test_cleanup_proof_delayed_group_exit_needs_positive_receipt(self):
        calls,diagnostics=self.run_case(kill_delay=3)
        self.assertEqual(len(calls),2);self.assertTrue(diagnostics['teardown_receipt_present'])
    def test_cleanup_proof_setup_failure_after_spawn_still_tears_down(self):
        calls,diagnostics=self.run_case(expected_error='HOST_IO_FAILED',setup_write_denied=True)
        self.assertEqual(len(calls),2);self.assertTrue(diagnostics['teardown_receipt_present'])
    def test_cleanup_proof_requires_durable_node_receipt(self):
        _,diagnostics=self.run_case()
        self.assertTrue(diagnostics['teardown_receipt_present'],'successful owned group teardown must leave durable proof before coordinator exits')
    def test_signal_handler_only_notifies_when_cleanup_lock_is_held(self):
        calls,_=self.run_case()
        self.assertEqual(len(calls),2,'one TERM and one KILL; repeated notification cannot reschedule teardown')
    def test_signal_handler_eperm_is_unknown_without_retry_or_fallback(self):
        calls,diagnostics=self.run_case(expected_error='COORDINATOR_PROCESS_CLEANUP_UNKNOWN',deny=True)
        self.assertEqual(len(calls),1)
        self.assertTrue(any(event.get('errno')==1 and event['phase']=='signal-denied' for event in diagnostics['events']))
    def test_signal_handler_all_zombie_group_requires_positive_observation_and_no_signal(self):
        calls,diagnostics=self.run_case(expected_error=None,state='Z')
        self.assertEqual(calls,[])
        self.assertTrue(any(event['phase']=='cleanup-observed' for event in diagnostics['events']))
    def test_signal_handler_wrong_group_never_receives_signal(self):
        calls,_=self.run_case(expected_error='COORDINATOR_PROCESS_CLEANUP_UNKNOWN',wrong_group=True)
        self.assertEqual(calls,[])
    def test_signal_handler_unavailable_preflight_never_launches_execution(self):
        calls,diagnostics=self.run_case(expected_error='GROUP_OBSERVATION_UNKNOWN',preflight_denied=True)
        self.assertEqual(calls,[]);self.assertEqual(diagnostics['execution_popen_calls'],0)


class CleanupProofRegressionTest(unittest.TestCase):
    """Source-only process observations/signals are mocked in every case."""
    def setUp(self):
        import cdo_runtime_coordinator as coordinator
        self.c=coordinator;self.tmp=tempfile.TemporaryDirectory()
        self.root=pathlib.Path(self.tmp.name).resolve();self.root.chmod(0o700)
        self.lease=self.root/'lease.json';self.owned=self.root/'owned.json';self.file=self.root/'job.json'
        self.c.atomic_private(self.lease,{'execution_id':'cleanup-fixture','attempt':1,'status':'ACTIVE','expires_at':time.time()+60})
        self.c.atomic_private(self.owned,{})
        self.job={'version':1,'request':{'execution_id':'cleanup-fixture','attempt':1,'runtime_context':'fixture-context'},'environment':{},'lease_file':str(self.lease),'owned_config':str(self.owned),'execution_root':str(self.root),'process_owner':dict(OWNER)}
        self.c.atomic_private(self.file,self.job);_,_,self.binding=self.c.job_binding(self.file)
        self.expected={'job_sha256':self.binding['job_sha256'],'owned_config_sha256':self.binding['owned_config_sha256'],'process_owner':dict(OWNER),'coordinator_pid':434343,'coordinator_reaped':True}
        self.coordinator={'pid':434343,'pgid':434343,'sid':434343,'uid':os.geteuid()}
        self.node={'pid':424242,'pgid':424242,'sid':424242,'uid':os.geteuid()}
        self.owner={'version':1,'binding':self.binding,'coordinator':self.coordinator,'node':self.node,'phase':'TEARDOWN','no_unknown':True}
        self.proof={'version':1,'binding':self.binding,'coordinator':self.coordinator,'node':self.node,'node_group_absent':True,'node_reaped':True,'no_unknown':True,'node_exit_code':0,'terminal_cause':None,'proof_kind':'anchored-teardown'}
        self.c.atomic_private(self.root/self.c.OWNER_FILE,self.owner);self.c.atomic_private(self.root/self.c.TEARDOWN_FILE,self.proof)
        self.work=self.c.private_work_root(self.root,OWNER);self.work.mkdir(mode=0o700)
        (self.work/'private-token').write_text('private fixture');(self.work/'sub').mkdir();(self.work/'sub'/'state').write_text('private state')
    def tearDown(self):self.tmp.cleanup()
    def observe(self,members=None):return patch('cdo_runtime_coordinator.group_members',return_value=[] if members is None else members)
    def test_cleanup_proof_positive_receipt_and_storage_absence_are_idempotent(self):
        with self.observe(),patch('cdo_runtime_coordinator.os.killpg',side_effect=AssertionError('no signal on reconcile')):
            first=self.c.reconcile_cleanup(self.file,self.expected,oci_absent=True)
            second=self.c.reconcile_cleanup(self.file,self.expected,oci_absent=True)
        self.assertEqual(first,second);self.assertTrue(all(value is True for value in first.values()));self.assertFalse(os.path.lexists(self.work))
    def test_cleanup_proof_docker_unknown_never_deletes_private_storage(self):
        for value in (False,None,'true'):
            with self.observe(),self.assertRaisesRegex(CoordinatorDenied,'OCI_CLEANUP_UNPROVEN'):
                self.c.reconcile_cleanup(self.file,self.expected,oci_absent=value)
            self.assertTrue(self.work.exists())
    def test_cleanup_proof_missing_receipt_and_launch_race_remain_unknown(self):
        (self.root/self.c.TEARDOWN_FILE).unlink();self.owner.update(phase='NODE_LAUNCHING',node=None);self.c.atomic_private(self.root/self.c.OWNER_FILE,self.owner)
        with self.observe(),self.assertRaisesRegex(CoordinatorDenied,'TEARDOWN_UNPROVEN'):
            self.c.reconcile_cleanup(self.file,self.expected,oci_absent=True)
        self.assertTrue(self.work.exists())
    def test_cleanup_proof_crash_restart_recovers_only_positive_registered_absence(self):
        (self.root/self.c.TEARDOWN_FILE).unlink();self.owner['phase']='NODE_RUNNING';self.c.atomic_private(self.root/self.c.OWNER_FILE,self.owner)
        self.expected['coordinator_reaped']=False
        with self.observe(),patch('cdo_runtime_coordinator.os.killpg',side_effect=AssertionError('restart must not signal saved PID')):
            result=self.c.reconcile_cleanup(self.file,self.expected,oci_absent=True)
        self.assertTrue(all(value is True for value in result.values()));self.assertFalse(self.work.exists())
        proof=self.c.private_json(str(self.root/self.c.TEARDOWN_FILE),65536)
        self.assertEqual(proof['proof_kind'],'observed-recovery');self.assertIs(proof['node_reaped'],False)
    def test_cleanup_proof_crash_between_owner_and_receipt_write_is_recoverable(self):
        (self.root/self.c.TEARDOWN_FILE).unlink();self.expected['coordinator_reaped']=False
        with self.observe():self.assertTrue(all(value is True for value in self.c.reconcile_cleanup(self.file,self.expected,oci_absent=True).values()))
        self.assertFalse(self.work.exists())
    def test_cleanup_proof_reused_pid_or_group_never_receives_signal(self):
        for member in ({'pid':424242,'pgid':999999},{'pid':999998,'pgid':434343},{'pid':434343,'pgid':999999}):
            with self.observe([member]),patch('cdo_runtime_coordinator.os.killpg',side_effect=AssertionError('no stored PID signal')),self.assertRaises(CoordinatorDenied):
                self.c.reconcile_cleanup(self.file,self.expected,oci_absent=True)
            self.assertTrue(self.work.exists())
    def test_cleanup_proof_observer_unknown_never_becomes_absence(self):
        with patch('cdo_runtime_coordinator.group_members',side_effect=CoordinatorDenied('COORDINATOR_GROUP_OBSERVATION_UNKNOWN')),self.assertRaisesRegex(CoordinatorDenied,'OBSERVATION_UNKNOWN'):
            self.c.reconcile_cleanup(self.file,self.expected,oci_absent=True)
        self.assertTrue(self.work.exists())
    def test_cleanup_proof_stale_cross_attempt_and_false_receipt_are_denied(self):
        for key in ('no_unknown','node_group_absent'):
            proof=dict(self.proof);proof[key]=False;self.c.atomic_private(self.root/self.c.TEARDOWN_FILE,proof)
            with self.observe(),self.assertRaisesRegex(CoordinatorDenied,'TEARDOWN_UNPROVEN'):self.c.reconcile_cleanup(self.file,self.expected,oci_absent=True)
        self.c.atomic_private(self.root/self.c.TEARDOWN_FILE,self.proof)
        expected=dict(self.expected);expected['process_owner']={**OWNER,'owner_nonce':'c'*32}
        with self.observe(),self.assertRaisesRegex(CoordinatorDenied,'BINDING_CHANGED'):self.c.reconcile_cleanup(self.file,expected,oci_absent=True)
    def test_cleanup_proof_symlink_cannot_delete_sibling_storage(self):
        import shutil
        shutil.rmtree(self.work);sibling=self.root/'sibling';sibling.mkdir();(sibling/'sentinel').write_text('preserved');self.work.symlink_to(sibling)
        with self.observe(),self.assertRaisesRegex(CoordinatorDenied,'PRIVATE_STORAGE_UNKNOWN'):self.c.reconcile_cleanup(self.file,self.expected,oci_absent=True)
        self.assertEqual((sibling/'sentinel').read_text(),'preserved')
    def test_cleanup_proof_never_started_uses_durable_prelaunch_authority(self):
        import shutil
        shutil.rmtree(self.work);(self.root/self.c.OWNER_FILE).unlink();(self.root/self.c.TEARDOWN_FILE).unlink()
        expected={**self.expected,'coordinator_pid':None,'coordinator_reaped':False,'never_started':True}
        with patch('cdo_runtime_coordinator.group_members',side_effect=AssertionError('no process was launched')):
            self.assertTrue(all(v is True for v in self.c.reconcile_cleanup(self.file,expected).values()))
        (self.root/self.c.OWNER_FILE).write_text('unexpected')
        with self.assertRaisesRegex(CoordinatorDenied,'NEVER_STARTED_PROOF_CHANGED'):self.c.reconcile_cleanup(self.file,expected)
    def test_cleanup_proof_registration_ack_binds_live_child_and_fresh_owner(self):
        from types import SimpleNamespace
        self.owner.update(node=None,phase='REGISTERED');self.c.atomic_private(self.root/self.c.OWNER_FILE,self.owner)
        process=SimpleNamespace(pid=434343,poll=lambda: self.fail('registration must retain child anchor'))
        expected=self.c.await_coordinator_registration(self.file,process)
        self.assertEqual(expected['coordinator_pid'],434343)
        ack=self.c.private_json(str(self.root/self.c.ACK_FILE),65536);self.assertEqual(ack['binding'],self.binding)
        with self.assertRaisesRegex(CoordinatorDenied,'ADMISSION_REPLAY'):self.c.await_coordinator_registration(self.file,process)
    def test_cleanup_proof_typed_node_outcome_is_bound_and_has_no_cleanup_authority(self):
        outcome={'version':1,'binding':self.binding,'node':self.node,'terminal_cause':'SDK_PRIVATE_STATE_BYTES_EXCEEDED','node_exit_code':1}
        file=self.root/self.c.NODE_OUTCOME_FILE;self.c.atomic_private(file,outcome)
        self.assertEqual(self.c.node_terminal_cause(self.root,self.binding,self.node,1),'SDK_PRIVATE_STATE_BYTES_EXCEEDED')
        for changed in ({'terminal_cause':'untrusted arbitrary stderr'},{'node':{**self.node,'pid':999999}},{'cleanup':True},{'node_exit_code':0}):
            self.c.atomic_private(file,{**outcome,**changed})
            with self.assertRaisesRegex(CoordinatorDenied,'HOST_NODE_OUTCOME_UNKNOWN'):self.c.node_terminal_cause(self.root,self.binding,self.node,1)
    def test_cleanup_proof_missing_node_outcome_cannot_turn_failure_into_success(self):
        self.assertEqual(self.c.node_terminal_cause(self.root,self.binding,self.node,1),'AGENT_PROCESS_FAILED')
        self.assertIsNone(self.c.node_terminal_cause(self.root,self.binding,self.node,0))
    def test_cleanup_proof_empty_truncated_or_duplicate_snapshot_is_unknown(self):
        uid=os.geteuid()
        valid=f'111 1 111 {uid} S\n222 111 111 {uid} R\n'.encode()
        snapshot=self.c.parse_process_snapshot(valid,222,111,uid)
        self.assertEqual(len(snapshot),2)
        for invalid in (b'',valid.splitlines()[0]+b'\n',valid+valid.splitlines()[0]+b'\n',f'0 1 111 {uid} S\n222 111 111 {uid} R\n'.encode(),valid+b'333 1 0 0 S\n',valid+b'not a complete row\n'):
            with patch('cdo_runtime_coordinator.sys.platform','linux'),self.assertRaisesRegex(CoordinatorDenied,'GROUP_OBSERVATION_UNKNOWN'):self.c.parse_process_snapshot(invalid,222,111,uid)
    def test_cleanup_proof_gateway_interruption_before_admission_is_denied(self):
        ack={'version':1,'binding':self.binding,'coordinator':self.coordinator,'controller_pid':999999}
        self.c.atomic_private(self.root/self.c.ACK_FILE,ack)
        with patch('cdo_runtime_coordinator.os.getppid',return_value=999998),self.assertRaisesRegex(CoordinatorDenied,'ADMISSION_IDENTITY_CHANGED'):
            self.c.await_registration_ack(self.root,self.binding,self.coordinator,str(self.lease))
    def test_cleanup_proof_registration_wrong_live_child_denies_ack(self):
        from types import SimpleNamespace
        self.owner.update(node=None,phase='REGISTERED');self.c.atomic_private(self.root/self.c.OWNER_FILE,self.owner)
        with self.assertRaisesRegex(CoordinatorDenied,'REGISTRATION_IDENTITY_CHANGED'):self.c.await_coordinator_registration(self.file,SimpleNamespace(pid=999999))
        self.assertFalse((self.root/self.c.ACK_FILE).exists())


if __name__ == '__main__': unittest.main()
