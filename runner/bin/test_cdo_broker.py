import base64,hashlib,json,pathlib,tempfile,unittest
from unittest.mock import patch
import subprocess
from cdo_broker import container_absent
from cdo_broker import Policy,Publisher,Denied,LimitExceeded,relative

class PublicationTest(unittest.TestCase):
 def setUp(self):
  self.tmp=tempfile.TemporaryDirectory();self.root=pathlib.Path(self.tmp.name).resolve();(self.root/'internal').mkdir();(self.root/'internal/a.go').write_text('before');(self.root/'state').mkdir()
  self.config={'root':str(self.root),'role':'layer','phase':'implementation','write_paths':['internal/a.go','internal/new.go'],'execution_id':'test','image':'trusted:v1','state_dir':str(self.root/'state'),'evidence':str(self.root/'evidence.json')}
  self.p=Policy(self.config);self.orig={'internal/a.go':hashlib.sha256(b'before').hexdigest()}
 def tearDown(self):self.tmp.cleanup()
 def item(self,s):return {'op':'write','data':base64.b64encode(s).decode()}
 def test_existing_atomic_new(self):
  inode=(self.root/'internal/a.go').stat().st_ino
  audit=Publisher(self.p,self.orig).publish({'internal/a.go':self.item(b'after'),'internal/new.go':self.item(b'new')})
  self.assertEqual((self.root/'internal/a.go').read_bytes(),b'after');self.assertNotEqual(inode,(self.root/'internal/a.go').stat().st_ino)
  self.assertEqual((self.root/'internal/new.go').read_bytes(),b'new');self.assertEqual([a['path'] for a in audit],['internal/a.go','internal/new.go']);self.assertEqual(audit[0]['proposed'],audit[0]['published'])
 def test_delete_requires_explicit_capability(self):
  with self.assertRaises(Denied):Publisher(self.p,self.orig).publish({'internal/a.go':{'op':'delete'}})
  self.p.publication_journal.unlink();self.config['delete_paths']=['internal/a.go'];audit=Publisher(Policy(self.config),self.orig).publish({'internal/a.go':{'op':'delete'}});self.assertFalse((self.root/'internal/a.go').exists());self.assertIsNone(audit[0]['published'])
 def test_scope_frozen_composition_and_escapes(self):
  for n in ['internal/sibling.go','contracts/frozen.go','cmd/main.go','../out','/outside','.git/config','internal/../../escape']:
   self.p.publication_journal.unlink(missing_ok=True)
   with self.subTest(n=n),self.assertRaises(Denied):Publisher(self.p,self.orig).publish({n:self.item(b'bad')})
  self.assertEqual((self.root/'internal/a.go').read_text(),'before')
 def test_links_special_types_and_precondition(self):
  for kind in ['symlink','hardlink','fifo']:
   p=self.root/'internal/new.go'
   if kind=='symlink':p.symlink_to(self.root/'internal/a.go')
   elif kind=='hardlink':__import__('os').link(self.root/'internal/a.go',p)
   else:__import__('os').mkfifo(p)
   with self.assertRaises(Denied):Publisher(self.p,self.orig).publish({'internal/new.go':self.item(b'bad')})
   p.unlink();self.p.publication_journal.unlink()
  (self.root/'internal/a.go').write_text('concurrent')
  with self.assertRaises(Denied):Publisher(self.p,self.orig).publish({'internal/a.go':self.item(b'bad')})
 def test_bounds(self):
  with self.assertRaises(LimitExceeded):Publisher(self.p,self.orig).publish({'internal/a.go':self.item(b'x'*262145)})
  self.p.publication_journal.unlink();self.p.aggregate_bytes=2
  with self.assertRaises(LimitExceeded):Publisher(self.p,self.orig).publish({'internal/a.go':self.item(b'xxx')})
 def test_reviewer_and_role_paths(self):
  self.config['role']='reviewer'
  with self.assertRaises(Denied):Policy(self.config)
  self.config['write_paths']=[];Policy(self.config)
  self.config['role']='composition';self.config['write_paths']=['internal/a.go']
  with self.assertRaises(Denied):Policy(self.config)
  self.config['role']='layer';self.config['write_paths']=['cmd/main.go']
  with self.assertRaises(Denied):Policy(self.config)
 def test_partial_attempt_is_permanently_invalid(self):
  import os
  original_replace=os.replace; operations=[]
  def failing(src,dst,*args,**kwargs):
   if str(src).startswith('.cdo-publish-'):
    operations.append(dst)
    if len(operations)==2:raise OSError('injected second publication failure')
   return original_replace(src,dst,*args,**kwargs)
  with patch('cdo_broker.os.replace',side_effect=failing),self.assertRaises(OSError):
   Publisher(self.p,self.orig).publish({'internal/a.go':self.item(b'after'),'internal/new.go':self.item(b'new')})
  self.assertEqual((self.root/'internal/a.go').read_bytes(),b'after')
  self.assertFalse((self.root/'internal/new.go').exists())
  self.assertEqual(json.loads(self.p.publication_journal.read_text())['status'],'FAILED')
  with self.assertRaisesRegex(Denied,'ALREADY_USED'):Publisher(self.p,self.orig).publish({})
 def test_invalidation_survives_private_state_removal(self):
  import shutil
  self.p.publication_journal.write_text('{"status":"FAILED"}')
  shutil.rmtree(self.p.state_dir);self.p.state_dir.mkdir()
  with self.assertRaisesRegex(Denied,'ALREADY_USED'):Publisher(Policy(self.config),self.orig).publish({})
 def test_crash_pending_attempt_denied(self):
  self.p.publication_journal.write_text('{"status":"PENDING"}')
  with self.assertRaisesRegex(Denied,'ALREADY_USED'):Publisher(self.p,self.orig).publish({})
 def test_daemon_unknown_never_means_absent(self):
  with patch('cdo_broker.subprocess.run',side_effect=subprocess.TimeoutExpired('docker',15)),self.assertRaisesRegex(Denied,'STATE_UNKNOWN'):container_absent('fixture')
  for stderr in ['Cannot connect to the Docker daemon','permission denied','']:
   with patch('cdo_broker.subprocess.run',return_value=subprocess.CompletedProcess([],1,'',stderr)),self.assertRaisesRegex(Denied,'STATE_UNKNOWN'):container_absent('fixture')
  with patch('cdo_broker.subprocess.run',return_value=subprocess.CompletedProcess([],1,'','Error: No such object: fixture')):self.assertTrue(container_absent('fixture'))
if __name__=='__main__':unittest.main()
