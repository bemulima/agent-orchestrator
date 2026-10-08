import unittest
from unittest.mock import patch
import cdo_release_identity as release
class RuntimeUnknownTests(unittest.TestCase):
 def test_docker_unavailable_cannot_produce_identity(self):
  with patch.object(release.pathlib.Path,'read_text',side_effect=OSError('unavailable')):
   with self.assertRaises(OSError):release.observe()
 def test_hash_is_exact_bytes(self):
  with patch.object(release.pathlib.Path,'read_bytes',return_value=b'actual'):
   self.assertEqual(release.sha('unused'),'e5c6fde86910ded72db5cc7afc32f850440d4ef7caa5dbb69f5bdc0d3e39cb3b')
if __name__=='__main__':unittest.main()
