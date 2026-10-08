import sys
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).parent))

from cdo_bwrap import _write_state, is_proc_mount_denial, probe_command, without_proc_mount


class ProcFallbackTests(unittest.TestCase):
    def test_recognizes_only_known_proc_mount_denials(self):
        self.assertTrue(
            is_proc_mount_denial(
                b"bwrap: Can't mount proc on /proc: Operation not permitted\n"
            )
        )
        self.assertTrue(
            is_proc_mount_denial(
                b"bwrap: Can't mount proc on /newroot/proc: Permission denied\n"
            )
        )
        self.assertFalse(is_proc_mount_denial(b"bwrap: setting up uid map: EPERM\n"))
        self.assertFalse(is_proc_mount_denial(b"agent: Operation not permitted\n"))

    def test_fallback_removes_only_proc_and_keeps_isolation(self):
        args = [
            "--unshare-user",
            "--unshare-pid",
            "--unshare-net",
            "--ro-bind",
            "/workspace",
            "/workspace",
            "--proc",
            "/proc",
            "--",
            "/bin/true",
        ]
        self.assertEqual(
            without_proc_mount(args),
            [
                "--unshare-user",
                "--unshare-pid",
                "--unshare-net",
                "--ro-bind",
                "/workspace",
                "/workspace",
                "--",
                "/bin/true",
            ],
        )

    def test_fallback_fails_closed_without_user_and_pid_namespaces(self):
        with self.assertRaisesRegex(ValueError, "user, PID and network namespaces"):
            without_proc_mount(["--unshare-user", "--proc", "/proc", "/bin/true"])
        with self.assertRaisesRegex(ValueError, "user, PID and network namespaces"):
            without_proc_mount(["--unshare-pid", "--proc", "/proc", "/bin/true"])

    def test_fallback_fails_closed_for_unexpected_proc_arguments(self):
        with self.assertRaisesRegex(ValueError, "exactly one"):
            without_proc_mount(
                ["--unshare-user", "--unshare-pid", "--proc", "/proc", "--proc", "/sys"]
            )
        with self.assertRaisesRegex(ValueError, "exact --proc /proc"):
            without_proc_mount(
                ["--unshare-user", "--unshare-pid", "--proc", "/newroot/proc"]
            )

    def test_fallback_rejects_missing_network_namespace(self):
        with self.assertRaisesRegex(ValueError, "network namespaces"):
            without_proc_mount(["--unshare-user", "--unshare-pid", "--proc", "/proc", "/bin/true"])

    def test_probe_keeps_user_and_pid_namespaces(self):
        command = probe_command()
        self.assertIn("--unshare-user", command)
        self.assertIn("--unshare-pid", command)
        self.assertIn("--unshare-net", command)
        self.assertEqual(command[command.index("--proc") + 1], "/proc")
        self.assertEqual(command[-1], "/bin/true")

    def test_state_replacement_stays_on_codex_home_filesystem(self):
        with tempfile.TemporaryDirectory() as temp_dir:
            home = Path(temp_dir)
            state_file = home / ".cdo-bwrap-proc-state-v1"
            with patch("cdo_bwrap.CODEX_HOME", home), patch(
                "cdo_bwrap.STATE_FILE", state_file
            ):
                _write_state("no-proc")
            self.assertEqual(state_file.read_text(encoding="ascii"), "no-proc\n")
            self.assertEqual(list(home.iterdir()), [state_file])


if __name__ == "__main__":
    unittest.main()
