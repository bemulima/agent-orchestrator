import unittest,tempfile,pathlib,json,os
from cdo_go_dependencies import validate,inventory,sha,prepare,DependencyDenied
from unittest.mock import patch
class DependencyTests(unittest.TestCase):
 def setUp(self):
  self.tmp=tempfile.TemporaryDirectory();self.root=pathlib.Path(self.tmp.name).resolve();self.bundle=self.root/'bundle';self.bundle.mkdir();(self.bundle/'modules').mkdir();(self.bundle/'modules/file').write_text('trusted');self.source=self.root/'source';self.source.mkdir()
  for n in ['go.mod','go.sum']:(self.source/n).write_text('approved')
  files=inventory(self.bundle/'modules');self.value={'schema_version':1,'files':files,'cache_identity':sha(json.dumps(files,sort_keys=True,separators=(',',':')).encode()),'inputs':{n:sha(b'approved') for n in ['go.mod','go.sum']},'toolchain_image':'sha256:trusted'}
  (self.bundle/'manifest.json').write_text(json.dumps(self.value));self.hash=sha((self.bundle/'manifest.json').read_bytes())
 def tearDown(self):self.tmp.cleanup()
 def check(self):return validate(self.bundle,self.hash,self.source,'sha256:trusted')
 def test_valid(self):self.check()
 def test_manifest_tamper(self):
  (self.bundle/'manifest.json').write_text('{}')
  with self.assertRaisesRegex(DependencyDenied,'MANIFEST_MISMATCH'):self.check()
 def test_content_tamper(self):
  (self.bundle/'modules/file').write_text('evil')
  with self.assertRaisesRegex(DependencyDenied,'CONTENT_MISMATCH'):self.check()
 def test_missing(self):
  (self.bundle/'modules/file').unlink()
  with self.assertRaisesRegex(DependencyDenied,'CONTENT_MISMATCH'):self.check()
 def test_sibling_root_link(self):
  (self.bundle/'modules').rename(self.root/'sibling');(self.bundle/'modules').symlink_to(self.root/'sibling')
  with self.assertRaisesRegex(DependencyDenied,'LINK_DENIED'):self.check()
 def test_symlink_hardlink_extra(self):
  for kind in ['sym','hard']:
   p=self.bundle/'modules/escape'
   if kind=='sym':p.symlink_to(self.source/'go.mod')
   else:os.link(self.source/'go.mod',p)
   with self.assertRaisesRegex(DependencyDenied,'LINK_DENIED'):self.check()
   p.unlink()
 def test_reprepare(self):
  (self.source/'go.mod').write_text('new dependency')
  with self.assertRaisesRegex(DependencyDenied,'REPREPARATION_REQUIRED'):self.check()
 def test_toolchain(self):
  with self.assertRaisesRegex(DependencyDenied,'TOOLCHAIN_MISMATCH'):validate(self.bundle,self.hash,self.source,'another')
 def test_root_traversal(self):
  with self.assertRaisesRegex(DependencyDenied,'ROOT_DENIED'):validate(self.bundle/'../bundle',self.hash)
 def test_mount_path_injection(self):
  with self.assertRaisesRegex(DependencyDenied,'ROOT_DENIED'):validate(pathlib.Path('/tmp/cache,readonly=false'),self.hash)
 def test_preparer_rejects_unapproved_inputs_before_network(self):
  with patch('cdo_go_dependencies.subprocess.check_output') as docker:
   with self.assertRaisesRegex(DependencyDenied,'APPROVED_INPUT_MISMATCH'):prepare(self.source,self.root/'output','image','wrong',sha(b'approved'),'execution')
   docker.assert_not_called()
 def test_preparer_rejects_workspace_before_network(self):
  (self.source/'go.work').write_text('use /host')
  with patch('cdo_go_dependencies.subprocess.check_output') as docker:
   with self.assertRaisesRegex(DependencyDenied,'WORKSPACE_UNSUPPORTED'):prepare(self.source,self.root/'output','image',sha(b'approved'),sha(b'approved'),'execution')
   docker.assert_not_called()
 def test_preparer_failure_cleans_staging(self):
  with patch('cdo_go_dependencies.subprocess.check_output',return_value='sha256:trusted'),patch('cdo_go_dependencies.subprocess.run') as run:
   run.return_value.returncode=1;run.return_value.stdout='';run.return_value.stderr=''
   with self.assertRaisesRegex(DependencyDenied,'RESOLUTION_INCOMPLETE'):prepare(self.source,self.root/'output','image',sha(b'approved'),sha(b'approved'),'execution')
   self.assertFalse((self.root/'output').exists());self.assertFalse(list(self.root.glob('go-dependency-preparing-*')))
if __name__=='__main__':unittest.main()
