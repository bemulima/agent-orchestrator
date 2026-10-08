import tempfile, unittest, sys, os
from pathlib import Path
sys.path.insert(0, str(Path(__file__).parent))
from cdo_oci_profile import docker_command, writable_files

class ProfileTests(unittest.TestCase):
    def setUp(self):
        self.temp=tempfile.TemporaryDirectory(); self.root=Path(self.temp.name).resolve();(self.root/'owned').write_text('fixture')
    def tearDown(self): self.temp.cleanup()
    def test_flags_and_mounts_are_deterministic(self):
        args=docker_command('cdo-sandbox:fixture',self.root,'layer',['owned'],['true'],'cdo-sandbox-fixture')
        for flag,value in [('--network','none'),('--cap-drop','ALL'),('--pids-limit','128'),('--memory','2g'),('--log-driver','none')]: self.assertEqual(args[args.index(flag)+1],value)
        self.assertIn('seccomp=builtin',args);self.assertIn('no-new-privileges=true',args);self.assertIn('--read-only',args)
        self.assertNotIn('unconfined',str(args));self.assertNotIn('SYS_ADMIN',str(args));self.assertEqual(args,docker_command('cdo-sandbox:fixture',self.root,'layer',['owned'],['true'],'cdo-sandbox-fixture'))
    def test_bad_scope_is_denied(self):
        for p in ['/outside','../outside','a/../b','.git/config','.env','a*','a,b','a\\b']:
            with self.subTest(p=p),self.assertRaises(ValueError): writable_files(self.root,'layer',[p])
    def test_mount_source_delimiter_is_denied(self):
        unsafe=self.root/"comma,source";unsafe.mkdir();(unsafe/"owned").write_text("fixture")
        with self.assertRaises(ValueError):writable_files(unsafe,"layer",["owned"])

    def test_link_escape_is_denied(self):
        (self.root/'link').symlink_to(self.root/'owned');os.link(self.root/'owned',self.root/'hard')
        for p in ['link','hard']:
            with self.assertRaises(ValueError): writable_files(self.root,'layer',[p])
    def test_roles_and_injected_flags_are_denied(self):
        with self.assertRaises(ValueError):writable_files(self.root,'reviewer',['owned'])
        with self.assertRaises(ValueError):writable_files(self.root,'composition',['owned'])
        with self.assertRaises(ValueError):docker_command('image --privileged',self.root,'layer',['owned'],['true'],'cdo-sandbox-fixture')
        args=docker_command('cdo-sandbox:fixture',self.root,'reviewer',[],['true'],'cdo-sandbox-reviewer')
        self.assertEqual(sum(x=='--mount' for x in args),1)

if __name__=='__main__':unittest.main()
