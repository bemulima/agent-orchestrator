"""Darwin waitid regressions; every process/observer/signal is synthetic."""
from contextlib import contextmanager
import ctypes
import json
import struct
import unittest
from unittest import mock
import test_owned_signal_diagnosis as support


class WaitidValidationTests(unittest.TestCase):
    @contextmanager
    def darwin(self, *, raw_uid=0, code=2, status=15):
        receipt={'pid':98765,'uid':raw_uid,'code':code,'status':status,
                 'uid_available':False,'backend':'darwin-libSystem-waitid','wnowait':True}
        with mock.patch.object(support.coordinator.os,'getuid',return_value=501),mock.patch.object(support.coordinator.os,'geteuid',return_value=501),support.OwnedSignalTests().fixture(receipt=receipt,platform='darwin') as fixture:
            yield fixture

    def test_actual_darwin_zero_uid_with_positive_zombie_identity_passes(self):
        with self.darwin() as (m,c,process,kill,output):
            result=m.signal_preflight(output)
            self.assertEqual(result['status'],'PASS')
            self.assertEqual(result['waitid_receipt']['uid'],0)
            self.assertEqual(result['waitid_validation']['uid_source'],'trusted-unreaped-zombie-effective-uid')
            self.assertTrue(result['waitid_validation']['zombie_identity_validated'])
            self.assertEqual(result['cleanup_state'],'PROVEN_ABSENT')

    def test_ctypes_darwin_receipt_explicitly_marks_uid_unavailable(self):
        import builtins
        c=support.coordinator;library=mock.Mock()
        def waitid(kind,pid,info,options):
            self.assertEqual((kind,pid,options),(1,98765,4|0x20))
            ctypes.memmove(info,struct.pack('=iiiIii',20,0,2,98765,0,15),24)
            return 0
        library.waitid.side_effect=waitid
        with mock.patch.object(c,'hasattr',side_effect=lambda obj,name:False if obj is c.os and name in ('waitid','WNOWAIT') else builtins.hasattr(obj,name),create=True),mock.patch.object(c.sys,'platform','darwin'),mock.patch.object(ctypes,'CDLL',return_value=library):
            receipt=c.wait_unreaped(98765)
        self.assertEqual(receipt['uid'],0)
        self.assertIs(receipt['uid_available'],False)
        self.assertEqual(receipt['backend'],'darwin-libSystem-waitid')
        self.assertIs(receipt['wnowait'],True)

    def test_darwin_natural_exit_or_other_signal_is_still_unknown(self):
        for code,status in ((1,0),(2,9),(3,15)):
            with self.subTest(code=code,status=status),self.darwin(code=code,status=status) as (m,c,process,kill,output):
                with self.assertRaises(m.Failure) as raised:m.signal_preflight(output)
                self.assertEqual(raised.exception.detail['phase'],'waitid-validation')
                self.assertFalse(json.loads((output/'owned-signal-diagnostics.json').read_text())['waitid_validated'])

    def test_darwin_unexpected_raw_uid_is_not_an_accept_either_rule(self):
        for uid in (501,1234):
            with self.subTest(uid=uid),self.darwin(raw_uid=uid) as (m,c,process,kill,output):
                with self.assertRaises(m.Failure) as raised:m.signal_preflight(output)
                self.assertFalse(raised.exception.detail['waitid_validation']['checks']['uid_semantics_valid'])

    def test_positive_zombie_identity_is_required_before_reap(self):
        for field,value in (('pid',12345),('ppid',12345),('pgid',12345),('uid',0),('state','S')):
            with self.subTest(field=field),self.darwin() as (m,c,process,kill,output):
                row={'pid':process.pid,'ppid':m.os.getpid(),'pgid':process.pid,'uid':501,'state':'S'}
                c.group_members.side_effect=[[row],[row],[{**row,'state':'Z',field:value}],[]]
                with self.assertRaises(m.Failure) as raised:m.signal_preflight(output)
                self.assertEqual(raised.exception.detail['phase'],'termination-observation')
                self.assertFalse(raised.exception.detail['waitid_validation']['zombie_identity_validated'])
                process.wait.assert_called_once_with(timeout=6)
                self.assertEqual(kill.call_count,1)

    def test_empty_or_multiple_zombies_cannot_validate_identity(self):
        with self.darwin() as (m,c,process,kill,output):
            row={'pid':process.pid,'ppid':m.os.getpid(),'pgid':process.pid,'uid':501,'state':'S'}
            c.group_members.side_effect=[[row],[row],[],[]]
            with self.assertRaises(m.Failure) as raised:m.signal_preflight(output)
            self.assertFalse(raised.exception.detail['waitid_validation']['zombie_checks']['single_member'])

    def test_zombie_observation_denial_remains_unknown_without_retry(self):
        with self.darwin() as (m,c,process,kill,output):
            row={'pid':process.pid,'ppid':m.os.getpid(),'pgid':process.pid,'uid':501,'state':'S'}
            c.group_members.side_effect=[[row],[row],support.coordinator.CoordinatorDenied('COORDINATOR_GROUP_OBSERVATION_UNKNOWN',{'observer_errno':1})]
            with self.assertRaises(m.Failure) as raised:m.signal_preflight(output)
            self.assertEqual(raised.exception.detail['cleanup_state'],'UNKNOWN')
            self.assertEqual(c.group_members.call_count,3)
            self.assertEqual(raised.exception.detail['os_errno'],1)

    def test_no_zombie_getpgid_getsid_or_reap_before_observation(self):
        with self.darwin() as (m,c,process,kill,output):
            events=[]
            row={'pid':process.pid,'ppid':m.os.getpid(),'pgid':process.pid,'uid':501,'state':'S'}
            snapshots=iter([[row],[row],[{**row,'state':'Z'}],[]])
            def observe(group):events.append('observe-group' if group else 'observe-absence');return next(snapshots)
            def waitid(pid):events.append('wnowait');return {'pid':pid,'uid':0,'code':2,'status':15,'uid_available':False,'backend':'darwin-libSystem-waitid','wnowait':True}
            def reap(**kwargs):events.append('reap');return -15
            c.group_members.side_effect=observe;c.wait_unreaped.side_effect=waitid;process.wait.side_effect=reap
            m.signal_preflight(output)
            self.assertEqual(events,['observe-group','observe-group','wnowait','observe-group','reap','observe-absence'])
            self.assertEqual(m.os.getpgid.call_count,2);self.assertEqual(m.os.getsid.call_count,2)

    def test_reap_status_conflict_is_unknown_even_after_signal_receipt(self):
        with self.darwin() as (m,c,process,kill,output):
            process.wait.return_value=0
            with self.assertRaises(m.Failure) as raised:m.signal_preflight(output)
            self.assertEqual(raised.exception.detail['phase'],'reap')
            self.assertEqual(raised.exception.detail['original_error_code'],'FRESH_CHILD_REAP_STATUS_UNKNOWN')
            self.assertEqual(raised.exception.detail['cleanup_state'],'PROVEN_ABSENT')
            self.assertEqual(json.loads((output/'owned-signal-diagnostics.json').read_text())['status'],'UNKNOWN')

    def test_linux_zero_or_wrong_uid_still_rejected(self):
        for uid in (0,1234):
            with self.subTest(uid=uid),mock.patch.object(support.coordinator.os,'getuid',return_value=501),mock.patch.object(support.coordinator.os,'geteuid',return_value=501),support.OwnedSignalTests().fixture(receipt={'uid':uid}) as (m,c,process,kill,output):
                with self.assertRaises(m.Failure) as raised:m.signal_preflight(output)
                self.assertFalse(raised.exception.detail['waitid_validation']['checks']['uid_semantics_valid'])

    def test_native_darwin_binding_also_marks_uid_unavailable(self):
        c=support.coordinator
        receipt=mock.Mock(si_pid=98765,si_uid=0,si_code=2,si_status=15)
        with mock.patch.object(c.sys,'platform','darwin'),mock.patch.object(c.os,'waitid',return_value=receipt,create=True) as waitid,mock.patch.object(c.os,'WNOWAIT',0x20,create=True),mock.patch.object(c.os,'P_PID',1,create=True),mock.patch.object(c.os,'WEXITED',4,create=True):
            value=c.wait_unreaped(98765)
            self.assertIs(value['uid_available'],False);self.assertEqual(value['backend'],'os-waitid')
            waitid.assert_called_once_with(1,98765,4|0x20)

    def test_missing_backend_wnowait_or_uid_semantics_fails_closed(self):
        for field,value in (('backend',None),('uid_available',None),('wnowait',False),('pid',12345)):
            with self.subTest(field=field),self.darwin() as (m,c,process,kill,output):
                c.wait_unreaped.return_value[field]=value
                with self.assertRaises(m.Failure):m.signal_preflight(output)
                raw=(output/'owned-signal-diagnostics.json').read_text()
                self.assertNotIn('12345',raw)

    def test_boolean_status_is_not_numeric_exit_evidence(self):
        with self.darwin() as (m,c,process,kill,output):
            c.wait_unreaped.return_value['uid']=False
            with self.assertRaises(m.Failure) as raised:m.signal_preflight(output)
            self.assertFalse(raised.exception.detail['waitid_validation']['checks']['fields_valid'])


if __name__=='__main__':unittest.main()
