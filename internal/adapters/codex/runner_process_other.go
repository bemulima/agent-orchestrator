//go:build !unix

package codex

import "os/exec"

func configureRunnerProcess(command *exec.Cmd) func() { return func() {} }
