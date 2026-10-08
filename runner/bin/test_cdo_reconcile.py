import json,pathlib,tempfile,unittest
from cdo_reconcile import reconcile
from cdo_broker import Denied
class ReconciliationAdmissionTest(unittest.TestCase):
 def test_completed_attempt_cannot_reconcile_into_success(self):
  with tempfile.TemporaryDirectory() as directory:
   f=pathlib.Path(directory)/'audit.json';f.write_text(json.dumps({'execution_id':'11111111-1111-4111-8111-111111111111','status':'completed','private_root':directory}))
   with self.assertRaisesRegex(Denied,'IDENTITY_DENIED'):reconcile(f)
 def test_foreign_root_denied_before_docker(self):
  with tempfile.TemporaryDirectory() as directory:
   f=pathlib.Path(directory)/'audit.json';f.write_text(json.dumps({'execution_id':'11111111-1111-4111-8111-111111111111','status':'infrastructure_unknown','private_root':'/'}))
   with self.assertRaisesRegex(Denied,'ROOT_DENIED'):reconcile(f)
