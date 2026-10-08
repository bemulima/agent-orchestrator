//go:build unix

package codex

import (
	"os/exec"
	"sync"
	"syscall"
	"time"
)

// Allow trusted runner teardown before escalation. The model remains in OCI.
func configureRunnerProcess(command *exec.Cmd) func() {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	var mu sync.Mutex
	var escalation *time.Timer
	command.Cancel = func() error {
		mu.Lock()
		defer mu.Unlock()
		if command.Process == nil {
			return nil
		}
		err := command.Process.Signal(syscall.SIGTERM)
		if err == nil {
			escalation = time.AfterFunc(45*time.Second, func() { _ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL) })
		}
		return err
	}
	command.WaitDelay = 50 * time.Second
	return func() {
		mu.Lock()
		defer mu.Unlock()
		if escalation != nil {
			escalation.Stop()
		}
	}
}
