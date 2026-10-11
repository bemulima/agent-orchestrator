import base64,hashlib,json,pathlib,tempfile,unittest
from unittest.mock import patch
import subprocess
from cdo_broker import container_absent
from cdo_broker import Policy,Publisher,Broker,Denied,LimitExceeded,relative,verify_staged

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
 def test_lease_expiry_before_publication_fails_existing_journal(self):
  import os,time
  lease=self.root/'lease.json';lease.write_text(json.dumps({'execution_id':'test','attempt':1,'expires_at':time.time()-1,'status':'ACTIVE'}));lease.chmod(0o600)
  with patch.dict(os.environ,{'CDO_RUNTIME_LEASE_FILE':str(lease)},clear=True),self.assertRaisesRegex(LimitExceeded,'LEASE_EXPIRED'):
   Publisher(self.p,self.orig).publish({'internal/a.go':self.item(b'after')})
  self.assertEqual(json.loads(self.p.publication_journal.read_text())['status'],'FAILED')
  self.assertEqual((self.root/'internal/a.go').read_text(),'before')
 def test_lease_loss_between_publication_steps_invalidates_partial_attempt(self):
  import os,time
  lease=self.root/'lease.json';value={'execution_id':'test','attempt':1,'expires_at':time.time()+60,'status':'ACTIVE'};lease.write_text(json.dumps(value));lease.chmod(0o600)
  def expire(phase,policy):
   if phase=='publication-applied-1':value['status']='CANCELLED';lease.write_text(json.dumps(value))
  with patch.dict(os.environ,{'CDO_RUNTIME_LEASE_FILE':str(lease)},clear=True),patch('cdo_broker.lifecycle_checkpoint',side_effect=expire),self.assertRaisesRegex(LimitExceeded,'LEASE_EXPIRED'):
   Publisher(self.p,self.orig).publish({'internal/a.go':self.item(b'after'),'internal/new.go':self.item(b'new')})
  self.assertEqual(json.loads(self.p.publication_journal.read_text())['status'],'FAILED');self.assertFalse((self.root/'internal/new.go').exists())
 def test_only_canonical_approved_command_reaches_runtime(self):
  self.config['approved_commands']=[['go','test','./...']];self.p=Policy(self.config)
  broker=object.__new__(Broker);broker.p=self.p;broker.failure=None;broker.started=__import__('time').monotonic()
  with patch('cdo_broker.trusted_docker_popen',side_effect=AssertionError('Docker reached')):
   for argv in [['sh','-c','id'],['git','worktree','add','x'],['curl','http://host.docker.internal'],['go','test','-mod=mod','./...'],['go','test','./...','-run','x']]:
    with self.subTest(argv=argv),self.assertRaisesRegex(Denied,'COMMAND_POLICY_DENIED'):broker.call('command',{'argv':argv})
 def test_cumulative_rpc_budgets_include_unauthenticated_attempts(self):
  broker=object.__new__(Broker);broker.p=self.p;broker.failure=None;broker.started=__import__('time').monotonic();broker.rpc_count=0;broker.rpc_input=0;broker.rpc_output=0
  self.p.rpc_requests=2;broker.charge_rpc();broker.charge_rpc()
  with self.assertRaisesRegex(LimitExceeded,'RPC_COUNT_LIMIT'):broker.charge_rpc()
  broker.rpc_count=0;self.p.rpc_input_bytes=2
  with self.assertRaisesRegex(LimitExceeded,'CUMULATIVE_INPUT'):broker.charge_rpc(size=3)
  broker.rpc_input=0;self.p.rpc_output_bytes=2
  with self.assertRaisesRegex(LimitExceeded,'CUMULATIVE_OUTPUT'):broker.charge_rpc(output=3,count=False)
 def test_independent_staged_verification_preserves_primary_state(self):
  state={'original':self.orig,'proposals':{'internal/a.go':self.item(b'after')},'failure':None}
  file=self.p.state_dir/'state.json';file.write_text(json.dumps(state));file.chmod(0o600);before=file.read_bytes();calls=[]
  class Independent:
   def __init__(self,p):self.original=dict(state['original'])
   def launch(self):calls.append('launch')
   def call(self,tool,args):
    calls.append((tool,args));return {'exit_code':0,'output':'ok'}
   def close(self):calls.append('close')
  with patch('cdo_broker.Broker',Independent):audit=verify_staged(self.p)
  self.assertEqual(file.read_bytes(),before);self.assertEqual((self.root/'internal/a.go').read_text(),'before')
  self.assertEqual(audit['verification']['exit_code'],0)
  self.assertIn(('command',{'argv':['go','test','./...']}),calls);self.assertEqual(calls[-1],'close')
 def test_independent_red_verification_poison_publication(self):
  state={'original':self.orig,'proposals':{},'failure':None};file=self.p.state_dir/'state.json';file.write_text(json.dumps(state));file.chmod(0o600)
  class Independent:
   def __init__(self,p):self.original=dict(state['original'])
   def launch(self):pass
   def call(self,tool,args):return {'exit_code':1,'output':'behavior is wrong'}
   def close(self):pass
  with patch('cdo_broker.Broker',Independent),self.assertRaisesRegex(Denied,'INDEPENDENT_GREEN_FAILED'):verify_staged(self.p)
  self.assertEqual(json.loads(self.p.publication_journal.read_text())['status'],'FAILED');self.assertEqual(json.loads(file.read_text()),state)
class PublicationFenceRegressionTests(unittest.TestCase):
 setUp=PublicationTest.setUp
 tearDown=PublicationTest.tearDown
 item=PublicationTest.item

 def concurrent_invalidation(self,phase):
  import concurrent.futures,threading
  entered=threading.Event();release=threading.Event()
  def gate(current,policy):
   if current==phase:
    entered.set();self.assertTrue(release.wait(3))
  with patch('cdo_broker.lifecycle_checkpoint',side_effect=gate),concurrent.futures.ThreadPoolExecutor(2) as pool:
   future=pool.submit(Publisher(self.p,self.orig).publish,{'internal/a.go':self.item(b'after'),'internal/new.go':self.item(b'new')})
   self.assertTrue(entered.wait(3))
   try:Publisher(self.p,{}).invalidate()
   finally:release.set()
   with self.assertRaises(Denied):future.result(3)
  self.assertEqual(json.loads(self.p.publication_journal.read_text())['status'],'FAILED')

 def test_concurrent_cancel_prevents_first_replacement(self):
  self.concurrent_invalidation('publication-validation')
  self.assertEqual((self.root/'internal/a.go').read_text(),'before')
  self.assertFalse((self.root/'internal/new.go').exists())

 def test_cancel_after_first_replacement_fences_remaining_files(self):
  self.concurrent_invalidation('publication-applied-1')
  self.assertEqual((self.root/'internal/a.go').read_text(),'after')
  self.assertFalse((self.root/'internal/new.go').exists())

 def test_late_success_cannot_replace_terminal_failure(self):
  publisher=Publisher(self.p,self.orig)
  def late(_):
   Publisher(self.p,{}).invalidate();return []
  with patch.object(publisher,'_publish',side_effect=late),self.assertRaises(Denied):publisher.publish({})
  self.assertEqual(json.loads(self.p.publication_journal.read_text())['status'],'FAILED')

 def test_cancelled_journal_remains_terminal_during_late_success(self):
  publisher=Publisher(self.p,self.orig)
  def late(_):
   value=json.loads(self.p.publication_journal.read_text());value['status']='CANCELLED'
   self.p.publication_journal.write_text(json.dumps(value));self.p.publication_journal.chmod(0o600)
   return []
  with patch.object(publisher,'_publish',side_effect=late),self.assertRaises(Denied):publisher.publish({})
  self.assertEqual(json.loads(self.p.publication_journal.read_text())['status'],'CANCELLED')

 def test_stale_identity_cannot_invalidate_another_journal(self):
  self.p.publication_journal.write_text(json.dumps({'execution_id':'other','attempt':9,'source_root':str(self.root),'status':'COMPLETE','at':1}))
  self.p.publication_journal.chmod(0o600);before=self.p.publication_journal.read_bytes()
  with self.assertRaisesRegex(Denied,'IDENTITY'):Publisher(self.p,{}).invalidate()
  self.assertEqual(self.p.publication_journal.read_bytes(),before)

 def test_pending_crash_recovery_revokes_journal_forever(self):
  publisher=Publisher(self.p,self.orig)
  class Crash(BaseException):pass
  with patch.object(publisher,'_publish',side_effect=Crash):
   # Leave the persisted PENDING at the exact pre-completion crash boundary.
   with patch('cdo_broker.os.replace',side_effect=lambda *a,**k: (_ for _ in ()).throw(Crash()) if pathlib.Path(a[0]).exists() and 'FAILED' in pathlib.Path(a[0]).read_text() else __import__('os').rename(*a,**k)):
    with self.assertRaises(Crash):publisher.publish({})
  self.assertEqual(json.loads(self.p.publication_journal.read_text())['status'],'PENDING')
  Publisher(Policy(self.config),{}).invalidate()
  self.assertEqual(json.loads(self.p.publication_journal.read_text())['status'],'FAILED')
  with self.assertRaises(Denied):Publisher(Policy(self.config),self.orig).publish({})


if __name__=='__main__':unittest.main()
