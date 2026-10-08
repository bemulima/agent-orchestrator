import pathlib, tempfile, unittest
from cdo_sdk_state import oci_command,ROOT,MAX_BYTES,MAX_FILES
from cdo_broker import Policy, Publisher, Denied
class SDKStateTests(unittest.TestCase):
 def test_storage_profile_is_kernel_bounded(self):
  args=oci_command('cdo-sdk-state-'+'a'*32,'trusted-image',[])
  self.assertIn(ROOT+':rw,noexec,nosuid,nodev,size=8388608,nr_inodes=256,uid=100,gid=101,mode=0700',args)
  for flag in ('--read-only','--cap-drop','--security-opt','--pids-limit','--log-driver'):self.assertIn(flag,args)
  self.assertNotIn('/workspace',str(args));self.assertNotIn('docker.sock',str(args));self.assertNotIn('--privileged',args)
  self.assertEqual(MAX_BYTES,8388608);self.assertEqual(MAX_FILES,256)
 def test_fallback_mounts_readonly(self):
  args=oci_command('name','image',[])
  for mount in ('/tmp','/dev/shm'):self.assertTrue(any(x.startswith(mount+':ro,') for x in args))
 def test_failed_attempt_cannot_publish_after_state_cleanup(self):
  with tempfile.TemporaryDirectory() as d:
   root=pathlib.Path(d).resolve();state=root/'state';state.mkdir();source=root/'source';source.mkdir()
   policy=Policy({'root':str(source),'role':'reviewer','phase':'probe','write_paths':[],'execution_id':'test','image':'trusted','evidence':str(root/'audit.json'),'state_dir':str(state)})
   publisher=Publisher(policy,{})
   publisher.invalidate();state.rmdir()
   self.assertIn('FAILED',policy.publication_journal.read_text())
   with self.assertRaisesRegex(Denied,'PUBLICATION_ATTEMPT_ALREADY_USED'):publisher.publish({})
if __name__=='__main__':unittest.main()
