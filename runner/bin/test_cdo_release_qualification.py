import concurrent.futures,datetime,pathlib,tempfile,unittest,uuid,json,hashlib
from dataclasses import asdict
from unittest.mock import patch
import cdo_release_qualification as qualification
from dataclasses import replace
from cdo_release_qualification import create_grant,validate,consume,QualificationDenied,IMAGES,SOURCE,REQUIRED,IDENTITY_KEYS

class QualificationTests(unittest.TestCase):
 def setUp(self):
  self.tmp=tempfile.TemporaryDirectory();self.base=pathlib.Path(self.tmp.name).resolve()
  self.root=self.base/'.cdo-sandbox-certification-20261008'/'release-final-go';(self.root/'internal').mkdir(parents=True);(self.root/'internal/value.go').write_text('fixture')
  self.root_patch=patch.object(qualification,'FIXTURE_ROOT',str(self.root));self.root_patch.start()
  self.actual={**{k:'a'*64 for k in IDENTITY_KEYS},'source_manifest_sha256':SOURCE,**IMAGES,'sdk_version':'0.144.6','cli_version':'codex-cli 0.144.6','docker_version':'29.8.1'}
  self.grant=create_grant(self.actual,self.root,str(uuid.uuid4()),str(uuid.uuid4()),(datetime.datetime.now(datetime.timezone.utc)+datetime.timedelta(hours=1)).isoformat())
  self.request={'purpose':'release-qualification','repository_kind':'disposable_certification','working_directory':str(self.root),'workpackage_id':self.grant.workpackage_id,'release_attempt_id':self.grant.release_attempt_id,'profile':'layer','write_paths':['internal/value.go']}
  self.required=list(REQUIRED);self.conditions={k:True for k in self.required};self.conditions['real_model_canary']=False
  self.store=self.base/'store';self.store.mkdir(mode=0o700)
 def tearDown(self):self.root_patch.stop();self.tmp.cleanup()
 def check(self,g=None,a=None,r=None,c=None):return validate(g or self.grant,a or self.actual,r or self.request,c or self.conditions,self.required)
 def test_only_previous_real_model_evidence_deferred(self):
  self.check()
  for key in [k for k in self.required if k!='real_model_canary']:
   for value in (False,None,'PASS','UNKNOWN',1):
    with self.assertRaises(QualificationDenied):self.check(c={**self.conditions,key:value})
 def test_all_identity_changes_deny(self):
  for k in self.actual:
   with self.assertRaises(QualificationDenied):self.check(a={**self.actual,k:'wrong'})
  with self.assertRaises(QualificationDenied):self.check(g=replace(self.grant,source_manifest_sha256='wrong'))
 def test_product_arbitrary_workpackage_and_scope_deny(self):
  for update in [{'repository_kind':'product'},{'workpackage_id':str(uuid.uuid4())},{'release_attempt_id':str(uuid.uuid4())},{'working_directory':'/projects/product'},{'profile':'contract'},{'write_paths':['sibling.go']},{'resume_thread':'old'}]:
   with self.assertRaises(QualificationDenied):self.check(r={**self.request,**update})
 def test_expired_deny(self):
  with self.assertRaisesRegex(QualificationDenied,'EXPIRED'):self.check(g=replace(self.grant,expires_at='2020-01-01T00:00:00+00:00'))
 def test_atomic_one_use_and_replay(self):
  def call(_):
   try:consume(self.grant,self.actual,self.request,self.conditions,self.required,self.store);return 'allowed'
   except QualificationDenied as e:return e.code
  with concurrent.futures.ThreadPoolExecutor(max_workers=8) as pool:result=list(pool.map(call,range(16)))
  self.assertEqual(result.count('allowed'),1);self.assertEqual(result.count('QUALIFICATION_CONSUMED'),15)
 def test_preexecution_failure_does_not_consume(self):
  with self.assertRaises(QualificationDenied):consume(self.grant,{**self.actual,'docker_version':'UNKNOWN'},self.request,self.conditions,self.required,self.store)
  self.assertEqual(list(self.store.iterdir()),[])
 def test_same_owner_attempt_cannot_issue_second_model_execution(self):
  consume(self.grant,self.actual,self.request,self.conditions,self.required,self.store)
  second=replace(self.grant,id=str(uuid.uuid4()),nonce=str(uuid.uuid4()))
  with self.assertRaisesRegex(QualificationDenied,'CONSUMED'):consume(second,self.actual,self.request,self.conditions,self.required,self.store)
 def test_policy_cannot_omit_security_conditions(self):
  with self.assertRaisesRegex(QualificationDenied,'POLICY_MISMATCH'):validate(self.grant,self.actual,self.request,self.conditions,[])
 def test_grant_cannot_expand_role_or_writes(self):
  for update in [{'role':'contract'},{'write_paths':('internal/value.go','internal/sibling.go')},{'product_repositories_allowed':True},{'real_pilot_allowed':True}]:
   with self.assertRaisesRegex(QualificationDenied,'SCOPE_INVALID'):self.check(g=replace(self.grant,**update))
 def test_symlink_parent_denied(self):
  import shutil
  shutil.rmtree(self.root/'internal');outside=self.base/'outside';outside.mkdir();(outside/'value.go').write_text('outside');(self.root/'internal').symlink_to(outside,target_is_directory=True)
  with self.assertRaisesRegex(QualificationDenied,'SCOPE_MISMATCH'):self.check()
 def test_unknown_product_permission_not_false(self):
  for value in (None,'UNKNOWN',0):
   with self.assertRaisesRegex(QualificationDenied,'SCOPE_INVALID'):self.check(g=replace(self.grant,product_repositories_allowed=value))
 def test_readonly_grant_pin_and_controller_binding(self):
  file=self.base/'grant.json';value={**asdict(self.grant),'controller_sha256':hashlib.sha256(pathlib.Path(qualification.__file__).read_bytes()).hexdigest()};data=json.dumps(value).encode();file.write_bytes(data);file.chmod(0o444);pin=hashlib.sha256(data).hexdigest()
  loaded=qualification.load_grant(file,pin);self.assertEqual(loaded,self.grant)
  with self.assertRaisesRegex(QualificationDenied,'GRANT_STALE'):qualification.load_grant(file,'0'*64)
  file.chmod(0o600)
  with self.assertRaisesRegex(QualificationDenied,'GRANT_UNTRUSTED'):qualification.load_grant(file,pin)
  value['controller_sha256']='0'*64;data=json.dumps(value).encode();file.write_bytes(data);file.chmod(0o444)
  with self.assertRaisesRegex(QualificationDenied,'CONTROLLER_STALE'):qualification.load_grant(file,hashlib.sha256(data).hexdigest())
if __name__=='__main__':unittest.main()
