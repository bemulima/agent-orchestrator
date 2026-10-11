import concurrent.futures,datetime,pathlib,tempfile,unittest,uuid,json,hashlib
from dataclasses import asdict
from unittest.mock import patch
import cdo_release_qualification as qualification
from dataclasses import replace
from cdo_release_qualification import create_grant,validate,consume,QualificationDenied,REQUIRED,IDENTITY_KEYS

class QualificationTests(unittest.TestCase):
 def setUp(self):
  self.tmp=tempfile.TemporaryDirectory();self.base=pathlib.Path(self.tmp.name).resolve()
  self.root=self.base/'.cdo-sandbox-certification-20261008'/'release-final-go';(self.root/'internal').mkdir(parents=True);(self.root/'internal/value.go').write_text('fixture')
  self.actual={k:('sha256:'+'a'*64 if k.endswith('_image') else 'a'*64) for k in IDENTITY_KEYS}
  self.actual.update(sdk_version='0.144.6',cli_version='codex-cli 0.144.6',docker_version='29.8.1',go_version='go1.25.1')
  self.store=self.base/'store';self.store.mkdir(mode=0o700)
  attempt=str(uuid.uuid4());workpackage=str(uuid.uuid4())
  receipts={}
  for condition in set(REQUIRED)-{'real_model_canary'}:
   file=self.base/(condition+'.json')
   receipt={'kind':'RELEASE_QUALIFICATION_PREREQUISITE','condition':condition,'result':'PASS','identity_sha256':qualification.digest(self.actual),'release_attempt_id':attempt,'workpackage_id':workpackage}
   receipts[condition]={'path':str(file),'sha256':self.write_pin(file,receipt)}
  self.policy_value={'kind':'RELEASE_QUALIFICATION_POLICY','schema_version':1,'owner_approval_reference':'synthetic-owner-decision:fixture-only','identity':self.actual,'fixture_root':str(self.root),'repository_kind':'disposable_certification','workpackage_id':workpackage,'release_attempt_id':attempt,'role':'layer','write_paths':['internal/value.go'],'model':'gpt-5.6-sol','reasoning':'low','max_model_runs':1,'budget':{'max_runs_5h':20,'mode':'enforce'},'expires_at':(datetime.datetime.now(datetime.timezone.utc)+datetime.timedelta(hours=1)).isoformat(),'consumption_store':str(self.store),'prerequisite_receipts':receipts}
  self.policy_file=self.base/'policy.json'
  self.policy=self.load_policy_value(self.policy_value)
  self.grant=create_grant(self.policy,self.policy_value['expires_at'])
  self.request={'purpose':'release-qualification','repository_kind':'disposable_certification','working_directory':str(self.root),'workpackage_id':self.grant.workpackage_id,'release_attempt_id':self.grant.release_attempt_id,'profile':'layer','write_paths':['internal/value.go'],'model':'gpt-5.6-sol','reasoning':'low','max_model_runs':1,'budget':{'max_runs_5h':20,'mode':'enforce'},'consumption_store':str(self.store)}
  self.required=list(REQUIRED);self.conditions={k:True for k in self.required};self.conditions['real_model_canary']=False
 def tearDown(self):self.tmp.cleanup()
 def load_policy_value(self,value):
  pin=self.write_pin(self.policy_file,value)
  return qualification.load_policy(self.policy_file,pin,pin)
 def write_pin(self,file,value):
  if file.exists():file.chmod(0o600)
  data=json.dumps(value,sort_keys=True).encode();file.write_bytes(data);file.chmod(0o444)
  return hashlib.sha256(data).hexdigest()
 def check(self,g=None,a=None,r=None,c=None):return validate(g or self.grant,a or self.actual,r or self.request,c or self.conditions,self.required,self.policy)
 def test_current_transport_identity_can_qualify(self):
  self.assertEqual(len(self.actual),25)
  self.check()
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
   try:consume(self.grant,self.actual,self.request,self.conditions,self.required,self.store,self.policy);return 'allowed'
   except QualificationDenied as e:return e.code
  with concurrent.futures.ThreadPoolExecutor(max_workers=8) as pool:result=list(pool.map(call,range(16)))
  self.assertEqual(result.count('allowed'),1);self.assertEqual(result.count('QUALIFICATION_CONSUMED'),15)
 def test_preexecution_failure_does_not_consume(self):
  with self.assertRaises(QualificationDenied):consume(self.grant,{**self.actual,'docker_version':'UNKNOWN'},self.request,self.conditions,self.required,self.store,self.policy)
  self.assertEqual(list(self.store.iterdir()),[])
 def test_same_owner_attempt_cannot_issue_second_model_execution(self):
  consume(self.grant,self.actual,self.request,self.conditions,self.required,self.store,self.policy)
  second=replace(self.grant,id=str(uuid.uuid4()),nonce=str(uuid.uuid4()))
  with self.assertRaisesRegex(QualificationDenied,'CONSUMED'):consume(second,self.actual,self.request,self.conditions,self.required,self.store,self.policy)
 def test_policy_cannot_omit_security_conditions(self):
  with self.assertRaisesRegex(QualificationDenied,'POLICY_MISMATCH'):validate(self.grant,self.actual,self.request,self.conditions,[],self.policy)
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
 def test_same_grant_different_consumption_store_denied(self):
  consume(self.grant,self.actual,self.request,self.conditions,self.required,self.store,self.policy)
  alternate=self.base/'alternate-store';alternate.mkdir(mode=0o700)
  with self.assertRaisesRegex(QualificationDenied,'STORE_DENIED'):
   consume(self.grant,self.actual,self.request,self.conditions,self.required,alternate,self.policy)
  self.assertEqual(list(alternate.iterdir()),[])
 def test_request_cannot_select_another_consumption_store(self):
  alternate=self.base/'alternate-store';alternate.mkdir(mode=0o700)
  with self.assertRaises(QualificationDenied):
   consume(self.grant,self.actual,{**self.request,'consumption_store':str(alternate)},self.conditions,self.required,alternate,self.policy)
  self.assertEqual(list(alternate.iterdir()),[])
 def test_approved_store_must_remain_private(self):
  self.store.chmod(0o755)
  with self.assertRaisesRegex(QualificationDenied,'STORE_DENIED'):self.check()
 def test_grant_cannot_extend_owner_approved_expiry(self):
  with self.assertRaisesRegex(QualificationDenied,"POLICY_MISMATCH"):create_grant(self.policy,"2099-01-01T00:00:00+00:00")
 def test_explicit_external_approval_pin_required(self):
  with self.assertRaisesRegex(QualificationDenied,"OWNER_APPROVAL_MISMATCH"):qualification.load_policy(self.policy_file,self.policy.sha256,"b"*64)
  with self.assertRaises(TypeError):qualification.load_policy(self.policy_file,self.policy.sha256)
 def test_plain_dictionary_cannot_establish_owner_authority(self):
  with self.assertRaisesRegex(QualificationDenied,'TRUSTED_POLICY_REQUIRED'):create_grant(self.policy_value,self.grant.expires_at)
 def test_no_historical_identity_fallback(self):
  legacy={key:value for key,value in self.actual.items() if key not in ('runtime_controller_sha256','runtime_protocol_sha256','runtime_transport_sha256','agent_runtime_controller_sha256','agent_runtime_protocol_sha256','agent_runtime_transport_sha256','active_host_attestation_sha256')}
  value={**self.policy_value,'identity':legacy}
  with self.assertRaisesRegex(QualificationDenied,'IDENTITY_INCOMPLETE'):self.load_policy_value(value)
 def test_policy_requires_owner_reference_and_fixed_model_budget(self):
  for update in ({'owner_approval_reference':''},{'model':'gpt-6-sol'},{'reasoning':'high'},{'max_model_runs':2},{'max_model_runs':True},{'budget':{'max_runs_5h':21,'mode':'enforce'}},{'repository_kind':'product'}):
   with self.assertRaises(QualificationDenied):self.load_policy_value({**self.policy_value,**update})
 def test_request_cannot_change_model_or_budget(self):
  for update in ({'model':'gpt-6-sol'},{'reasoning':'high'},{'max_model_runs':2},{'max_model_runs':True},{'budget':{'max_runs_5h':20,'mode':'observe'}}):
   with self.assertRaises(QualificationDenied):self.check(r={**self.request,**update})
 def test_receipts_required_and_bound_to_candidate_attempt(self):
  condition=next(iter(self.policy_value['prerequisite_receipts']))
  ref=self.policy_value['prerequisite_receipts'][condition];file=pathlib.Path(ref['path']);receipt=json.loads(file.read_text())
  for update in ({'result':'UNKNOWN'},{'condition':'unrelated'},{'identity_sha256':'b'*64},{'release_attempt_id':str(uuid.uuid4())},{'workpackage_id':str(uuid.uuid4())}):
   changed={**receipt,**update};pin=self.write_pin(file,changed)
   refs={**self.policy_value['prerequisite_receipts'],condition:{'path':str(file),'sha256':pin}}
   value={**self.policy_value,'prerequisite_receipts':refs}
   with self.assertRaisesRegex(QualificationDenied,'EVIDENCE_MISMATCH'):self.load_policy_value(value)
 def test_stale_policy_and_receipt_do_not_consume(self):
  condition=next(iter(self.policy_value['prerequisite_receipts']));file=pathlib.Path(self.policy_value['prerequisite_receipts'][condition]['path'])
  self.write_pin(file,{'changed':True})
  with self.assertRaisesRegex(QualificationDenied,'EVIDENCE_STALE'):consume(self.grant,self.actual,self.request,self.conditions,self.required,self.store,self.policy)
  self.assertEqual(list(self.store.iterdir()),[])
 def test_missing_receipt_and_writable_policy_deny(self):
  refs=dict(self.policy_value['prerequisite_receipts']);refs.pop(next(iter(refs)))
  with self.assertRaisesRegex(QualificationDenied,'EVIDENCE_INCOMPLETE'):self.load_policy_value({**self.policy_value,'prerequisite_receipts':refs})
  self.write_pin(self.policy_file,self.policy_value);self.policy_file.chmod(0o600)
  with self.assertRaisesRegex(QualificationDenied,'POLICY_UNTRUSTED'):self.check()
 def test_consumption_write_failure_poisons_attempt(self):
  with patch.object(qualification.json,'dump',side_effect=OSError('synthetic write failure')):
   with self.assertRaises(OSError):consume(self.grant,self.actual,self.request,self.conditions,self.required,self.store,self.policy)
  with self.assertRaisesRegex(QualificationDenied,'CONSUMED'):consume(self.grant,self.actual,self.request,self.conditions,self.required,self.store,self.policy)
if __name__=='__main__':unittest.main()
