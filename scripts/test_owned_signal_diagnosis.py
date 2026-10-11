"""Owned signal diagnostics with all process/observer/signal operations mocked."""
from contextlib import contextmanager
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest
from unittest import mock
import sys

ROOT=Path(__file__).resolve().parents[1]
sys.path.insert(0,str(ROOT/'runner/bin'))
import cdo_runtime_coordinator as coordinator


def module():
    spec=importlib.util.spec_from_file_location('owned_signal_host',ROOT/'scripts/certify-runtime-host.py')
    value=importlib.util.module_from_spec(spec);spec.loader.exec_module(value)
    return value


class OwnedSignalTests(unittest.TestCase):
    @contextmanager
    def fixture(self, *, kill_error=None, receipt=None, groups=None, platform='linux'):
        m=module();c=mock.Mock();process=mock.Mock(pid=98765)
        process.wait.return_value=-m.signal.SIGTERM
        member={'pid':process.pid,'ppid':m.os.getpid(),'pgid':process.pid,'uid':m.os.geteuid(),'state':'S'}
        c.group_members.side_effect=groups or [[member],[member],[{**member,'state':'Z'}],[]]
        c.wait_unreaped.return_value={'pid':process.pid,'uid':m.os.getuid(),'code':2,'status':int(m.signal.SIGTERM),'uid_available':True,'backend':'os-waitid','wnowait':True,**(receipt or {})}
        with tempfile.TemporaryDirectory() as temporary, mock.patch.object(m.sys,'platform',platform), mock.patch.object(m,'coordinator',return_value=c), mock.patch.object(m.subprocess,'Popen',return_value=process), mock.patch.object(m.os,'getpgid',return_value=process.pid), mock.patch.object(m.os,'getsid',return_value=process.pid), mock.patch.object(m.os,'killpg',side_effect=kill_error) as kill:
            yield m,c,process,kill,Path(temporary)

    def test_killpg_errno_and_phase_are_preserved(self):
        with self.fixture(kill_error=PermissionError(1,'denied')) as (m,c,process,kill,output), self.assertRaises(m.Failure) as raised:
            m.signal_preflight(output)
        self.assertIsInstance(raised.exception.detail,dict)
        self.assertEqual(raised.exception.detail['phase'],'killpg')
        self.assertEqual(raised.exception.detail['original_error_type'],'PermissionError')
        self.assertEqual(raised.exception.detail['os_errno'],1)
        self.assertTrue(raised.exception.detail['signal_attempted'])
        self.assertEqual(kill.call_count,1)

    def test_natural_exit_cannot_prove_sigterm_effect(self):
        with self.fixture(receipt={'pid':98765,'uid':__import__('os').getuid(),'code':1,'status':0}) as (m,c,process,kill,output), self.assertRaises(m.Failure) as raised:
            m.signal_preflight(output)
        self.assertEqual(raised.exception.detail['phase'],'waitid-validation')

    def test_primary_failure_has_durable_cleanup_disposition(self):
        with self.fixture(kill_error=PermissionError(1,'denied'),groups=None) as (m,c,process,kill,output):
            with self.assertRaises(m.Failure):m.signal_preflight(output)
            self.assertTrue((output/'owned-signal-diagnostics.json').is_file())
            detail=json.loads((output/'owned-signal-diagnostics.json').read_text())
            self.assertEqual(detail['status'],'UNKNOWN')
            self.assertIn('cleanup_state',detail)

    def test_darwin_waitid_preserves_errno(self):
        import ctypes,builtins
        library=mock.Mock();library.waitid.return_value=-1
        with mock.patch.object(coordinator,'hasattr',side_effect=lambda obj,name:False if obj is coordinator.os and name in ('waitid','WNOWAIT') else builtins.hasattr(obj,name),create=True),mock.patch.object(coordinator.sys,'platform','darwin'),mock.patch.object(ctypes,'CDLL',return_value=library),mock.patch.object(ctypes,'get_errno',return_value=1),self.assertRaises(coordinator.CoordinatorDenied) as raised:
            coordinator.wait_unreaped(98765)
        self.assertIsInstance(raised.exception.detail,dict)
        self.assertEqual(raised.exception.detail['observer_errno'],1)
        self.assertEqual(raised.exception.detail['operation'],'waitid')

    def test_success_requires_signal_receipt_reap_and_complete_absence(self):
        with self.fixture() as (m,c,process,kill,output):
            result=m.signal_preflight(output)
            self.assertEqual(result['status'],'PASS')
            self.assertEqual(result['cleanup_state'],'PROVEN_ABSENT')
            self.assertTrue(result['waitid_validated'])
            self.assertEqual(result['identity_snapshots_validated'],2)
            self.assertEqual(json.loads((output/'owned-signal-diagnostics.json').read_text()),result)
            process.wait.assert_called_once_with(timeout=5)
            kill.assert_called_once_with(process.pid,m.signal.SIGTERM)

    def test_creation_failure_is_not_created_and_preserves_errno(self):
        with self.fixture() as (m,c,process,kill,output),mock.patch.object(m.subprocess,'Popen',side_effect=OSError(11,'sensitive')):
            with self.assertRaises(m.Failure) as raised:m.signal_preflight(output)
            self.assertEqual(raised.exception.detail['phase'],'process-creation')
            self.assertEqual(raised.exception.detail['os_errno'],11)
            self.assertEqual(raised.exception.detail['cleanup_state'],'NOT_CREATED')
            kill.assert_not_called();process.wait.assert_not_called();c.group_members.assert_not_called()

    def test_observer_denial_latches_no_signal_and_no_observer_retry(self):
        with self.fixture() as (m,c,process,kill,output):
            c.group_members.side_effect=coordinator.CoordinatorDenied('COORDINATOR_GROUP_OBSERVATION_UNKNOWN',{'reason':'OBSERVER_SPAWN_FAILED','observer_errno':1})
            with self.assertRaises(m.Failure) as raised:m.signal_preflight(output)
            self.assertEqual(raised.exception.detail['phase'],'identity-observation-1')
            self.assertEqual(raised.exception.detail['os_errno'],1)
            self.assertEqual(raised.exception.detail['cleanup_state'],'UNKNOWN')
            c.group_members.assert_called_once_with(process.pid)
            process.wait.assert_called_once_with(timeout=6);kill.assert_not_called()

    def test_each_owned_identity_mismatch_prevents_signal(self):
        for field,value in [('pid',42),('ppid',42),('pgid',42),('uid',-1),('state','Z')]:
            with self.subTest(field=field),self.fixture() as (m,c,process,kill,output):
                row={'pid':process.pid,'ppid':m.os.getpid(),'pgid':process.pid,'uid':m.os.geteuid(),'state':'S',field:value}
                c.group_members.side_effect=[[row],[]]
                with self.assertRaises(m.Failure) as raised:m.signal_preflight(output)
                self.assertEqual(raised.exception.detail['phase'],'identity-validation-1')
                self.assertFalse(raised.exception.detail['identity_validated']);kill.assert_not_called()

    def test_pgid_and_sid_operations_report_exact_phase(self):
        for operation in ('getpgid','getsid'):
            with self.subTest(operation=operation),self.fixture() as (m,c,process,kill,output),mock.patch.object(m.os,operation,side_effect=PermissionError(1,'sensitive')):
                with self.assertRaises(m.Failure) as raised:m.signal_preflight(output)
                self.assertEqual(raised.exception.detail['phase'],operation+'-1')
                self.assertEqual(raised.exception.detail['os_errno'],1);kill.assert_not_called()

    def test_wrong_pgid_or_sid_prevents_signal(self):
        for operation in ('getpgid','getsid'):
            with self.subTest(operation=operation),self.fixture() as (m,c,process,kill,output),mock.patch.object(m.os,operation,return_value=42):
                with self.assertRaises(m.Failure):m.signal_preflight(output)
                kill.assert_not_called()

    def test_second_snapshot_must_also_validate(self):
        with self.fixture() as (m,c,process,kill,output):
            row={'pid':process.pid,'ppid':m.os.getpid(),'pgid':process.pid,'uid':m.os.geteuid(),'state':'S'}
            c.group_members.side_effect=[[row],[],[]]
            with self.assertRaises(m.Failure) as raised:m.signal_preflight(output)
            self.assertEqual(raised.exception.detail['phase'],'identity-validation-2');kill.assert_not_called()

    def test_waitid_errno_survives_wrapper_and_cleanup_does_not_convert_pass(self):
        with self.fixture() as (m,c,process,kill,output):
            c.wait_unreaped.side_effect=coordinator.CoordinatorDenied('COORDINATOR_PROCESS_OBSERVATION_UNKNOWN',{'observer_errno':10,'operation':'waitid'})
            with self.assertRaises(m.Failure) as raised:m.signal_preflight(output)
            self.assertEqual(raised.exception.detail['phase'],'waitid')
            self.assertEqual(raised.exception.detail['os_errno'],10)
            self.assertTrue(raised.exception.detail['signal_accepted'])
            self.assertEqual(json.loads((output/'owned-signal-diagnostics.json').read_text())['status'],'UNKNOWN')

    def test_wrong_receipt_pid_uid_signal_or_cause_never_passes(self):
        for field,value in [('pid',42),('uid',-1),('status',9),('code',1),('code',3)]:
            receipt={'pid':98765,'uid':__import__('os').getuid(),'code':2,'status':15,field:value}
            with self.subTest(field=field,value=value),self.fixture(receipt=receipt) as (m,c,process,kill,output):
                with self.assertRaises(m.Failure) as raised:m.signal_preflight(output)
                self.assertEqual(raised.exception.detail['phase'],'waitid-validation')

    def test_unequal_real_effective_uid_denied_before_creation(self):
        with self.fixture() as (m,c,process,kill,output),mock.patch.object(m.os,'getuid',return_value=444),mock.patch.object(m.os,'geteuid',return_value=445):
            with self.assertRaises(m.Failure) as raised:m.signal_preflight(output)
            self.assertEqual(raised.exception.detail['phase'],'caller-identity')
            m.subprocess.Popen.assert_not_called();kill.assert_not_called()

    def test_live_group_after_receipt_is_unknown(self):
        with self.fixture() as (m,c,process,kill,output):
            row={'pid':process.pid,'ppid':m.os.getpid(),'pgid':process.pid,'uid':m.os.geteuid(),'state':'S'}
            c.group_members.side_effect=[[row],[row],[row],[]]
            with self.assertRaises(m.Failure) as raised:m.signal_preflight(output)
            self.assertEqual(raised.exception.detail['phase'],'termination-observation')
            self.assertEqual(raised.exception.detail['cleanup_state'],'PROVEN_ABSENT')

    def test_cleanup_timeout_keeps_primary_error_no_alternate_signal(self):
        with self.fixture(kill_error=PermissionError(1,'sensitive')) as (m,c,process,kill,output):
            process.wait.side_effect=m.subprocess.TimeoutExpired('private command',6)
            with self.assertRaises(m.Failure) as raised:m.signal_preflight(output)
            self.assertEqual(raised.exception.detail['phase'],'killpg')
            self.assertEqual(raised.exception.detail['os_errno'],1)
            self.assertEqual(raised.exception.detail['cleanup_state'],'UNKNOWN')
            detail=json.loads((output/'owned-signal-diagnostics.json').read_text())
            self.assertEqual(detail['cleanup_error']['original_error_type'],'TimeoutExpired')
            self.assertNotIn('private command',json.dumps(detail));self.assertEqual(kill.call_count,1)

    def test_cleanup_observer_denial_keeps_original_error(self):
        with self.fixture(kill_error=PermissionError(1,'sensitive')) as (m,c,process,kill,output):
            row={'pid':process.pid,'ppid':m.os.getpid(),'pgid':process.pid,'uid':m.os.geteuid(),'state':'S'}
            c.group_members.side_effect=[[row],[row],coordinator.CoordinatorDenied('COORDINATOR_GROUP_OBSERVATION_UNKNOWN',{'observer_errno':13})]
            with self.assertRaises(m.Failure) as raised:m.signal_preflight(output)
            self.assertEqual(raised.exception.detail['os_errno'],1)
            self.assertEqual(raised.exception.detail['cleanup_state'],'UNKNOWN')
            self.assertEqual(c.group_members.call_count,3)
            self.assertEqual(json.loads((output/'owned-signal-diagnostics.json').read_text())['cleanup_error']['os_errno'],13)

    def test_save_failure_after_creation_still_waits_exact_child(self):
        with self.fixture() as (m,c,process,kill,output):
            real_save=m.save
            def save(path,value):
                if path.name=='owned-signal-process.json':raise OSError(28,'sensitive path')
                return real_save(path,value)
            c.group_members.side_effect=[[]]
            with mock.patch.object(m,'save',side_effect=save),self.assertRaises(m.Failure) as raised:m.signal_preflight(output)
            self.assertEqual(raised.exception.detail['phase'],'ownership-ledger')
            self.assertEqual(raised.exception.detail['cleanup_state'],'PROVEN_ABSENT')
            process.wait.assert_called_once_with(timeout=6);kill.assert_not_called()

    def test_exception_messages_and_unrelated_rows_not_in_diagnostics(self):
        with self.fixture(kill_error=PermissionError(1,'secret environment')) as (m,c,process,kill,output):
            row={'pid':process.pid,'ppid':m.os.getpid(),'pgid':process.pid,'uid':m.os.geteuid(),'state':'S'}
            c.group_members.side_effect=[[row],[row],[{'pid':87654,'pgid':87654,'uid':1234,'state':'S','private':'foreign command'}]]
            with self.assertRaises(m.Failure):m.signal_preflight(output)
            raw=(output/'owned-signal-diagnostics.json').read_text()
            for forbidden in ('secret environment','foreign command','87654','1234'):self.assertNotIn(forbidden,raw)

    def test_darwin_waitid_binding_failure_has_bounded_detail(self):
        import ctypes,builtins
        with mock.patch.object(coordinator,'hasattr',side_effect=lambda obj,name:False if obj is coordinator.os and name in ('waitid','WNOWAIT') else builtins.hasattr(obj,name),create=True),mock.patch.object(coordinator.sys,'platform','darwin'),mock.patch.object(ctypes,'CDLL',side_effect=OSError(2,'sensitive library error')),self.assertRaises(coordinator.CoordinatorDenied) as raised:
            coordinator.wait_unreaped(98765)
        self.assertEqual(raised.exception.detail['operation'],'waitid-load')
        self.assertEqual(raised.exception.detail['observer_errno'],2)
        self.assertNotIn('sensitive',str(raised.exception.detail))


class ReconciliationTests(unittest.TestCase):
    @contextmanager
    def fixture(self):
        m=module();c=mock.Mock();c.group_members.return_value=[]
        with tempfile.TemporaryDirectory() as temporary:
            root=Path(temporary).resolve();parent=root/'.cache/sandbox-certification';parent.mkdir(parents=True)
            old=parent/'old';old.mkdir();new=parent/'fresh'
            attempt={'attempt_id':'exact-old-attempt','stages':{'owned-group-signals':{'status':'UNKNOWN'}}}
            ledger={'pid':98765,'pgid':98765,'sid':98765,'parent_pid':23456,'uid':m.os.geteuid()}
            (old/'runtime-host-result.json').write_text(json.dumps(attempt))
            (old/'owned-signal-process.json').write_text(json.dumps(ledger))
            args=m.parser().parse_args(['--owned-signal-only','--output',str(new),'--reconcile-attempt',str(old),'--reconcile-attempt-id','exact-old-attempt'])
            with mock.patch.object(m,'ROOT',root),mock.patch.object(m.sys,'platform','darwin'),mock.patch.object(m,'coordinator',return_value=c),mock.patch.object(m,'signal_preflight',return_value={'status':'PASS','cleanup_state':'PROVEN_ABSENT'}) as fresh,mock.patch.object(m,'execute',side_effect=AssertionError('full certification')),mock.patch.object(m.os,'killpg',side_effect=AssertionError('old signal')) as kill,mock.patch('sys.stdout'):
                yield m,c,fresh,kill,args,old,new

    def test_reconcile_absence_is_read_only_and_uses_complete_observer(self):
        with self.fixture() as (m,c,fresh,kill,args,old,new):
            original={p.name:p.read_bytes() for p in old.iterdir()}
            result=m.reconcile_signal_absence(old,args.reconcile_attempt_id)
            self.assertEqual(result['cleanup_state'],'PROVEN_ABSENT')
            self.assertEqual(original,{p.name:p.read_bytes() for p in old.iterdir()})
            c.group_members.assert_called_once_with(None);kill.assert_not_called();fresh.assert_not_called()

    def test_present_saved_pid_or_group_prevents_creation_and_no_signal(self):
        for row in ({'pid':98765,'pgid':42},{'pid':42,'pgid':98765}):
            with self.subTest(row=row),self.fixture() as (m,c,fresh,kill,args,old,new):
                c.group_members.return_value=[row]
                self.assertEqual(m.owned_signal_only(args),1)
                fresh.assert_not_called();kill.assert_not_called()
                result=json.loads((new/'owned-signal-only-result.json').read_text())
                self.assertEqual(result['status'],'UNKNOWN');self.assertFalse(result['pass'])

    def test_observation_unknown_prevents_creation_no_retry(self):
        with self.fixture() as (m,c,fresh,kill,args,old,new):
            c.group_members.side_effect=coordinator.CoordinatorDenied('COORDINATOR_GROUP_OBSERVATION_UNKNOWN',{'observer_errno':1})
            self.assertEqual(m.owned_signal_only(args),1)
            c.group_members.assert_called_once_with(None);fresh.assert_not_called();kill.assert_not_called()
            result=json.loads((new/'owned-signal-only-result.json').read_text())
            self.assertEqual(result['error']['detail']['os_errno'],1)

    def test_signal_only_fresh_probe_requires_positive_reconciliation(self):
        with self.fixture() as (m,c,fresh,kill,args,old,new):
            self.assertEqual(m.owned_signal_only(args),0)
            fresh.assert_called_once_with(new);kill.assert_not_called()
            result=json.loads((new/'owned-signal-only-result.json').read_text())
            self.assertTrue(result['pass']);self.assertEqual(result['production_admission'],'DENIED')

    def test_existing_output_never_executes_probe(self):
        with self.fixture() as (m,c,fresh,kill,args,old,new):
            new.mkdir()
            with self.assertRaises(FileExistsError):m.owned_signal_only(args)
            fresh.assert_not_called();c.group_members.assert_not_called()

    def test_wrong_attempt_or_ledger_fails_before_observation(self):
        with self.fixture() as (m,c,fresh,kill,args,old,new):
            args.reconcile_attempt_id='wrong'
            self.assertEqual(m.owned_signal_only(args),1)
            fresh.assert_not_called();c.group_members.assert_not_called()

    def test_changed_evidence_during_observation_fails_before_creation(self):
        with self.fixture() as (m,c,fresh,kill,args,old,new):
            def observe(group):
                (old/'owned-signal-process.json').write_text(json.dumps({'changed':True}))
                return []
            c.group_members.side_effect=observe
            self.assertEqual(m.owned_signal_only(args),1);fresh.assert_not_called()
            result=json.loads((new/'owned-signal-only-result.json').read_text())
            self.assertEqual(result['error']['code'],'OLD_SIGNAL_EVIDENCE_CHANGED')

    def test_symlink_or_hardlinked_evidence_denied_before_observation(self):
        for kind in ('symlink','hardlink'):
            with self.subTest(kind=kind),self.fixture() as (m,c,fresh,kill,args,old,new):
                file=old/'owned-signal-process.json';target=old/'copy';file.rename(target)
                if kind=='symlink':file.symlink_to(target)
                else:m.os.link(target,file)
                self.assertEqual(m.owned_signal_only(args),1)
                fresh.assert_not_called();c.group_members.assert_not_called()

    def test_modes_are_mutually_exclusive(self):
        m=module()
        with mock.patch('sys.stderr'),self.assertRaises(SystemExit):m.parser().parse_args(['--owned-signal-only','--execute'])


if __name__=='__main__':unittest.main()
