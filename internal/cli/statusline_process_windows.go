//go:build windows

package cli

import (
	"os/exec"
	"syscall"
)

// configureDetachedProcess gives the refresh its own Windows process group;
// prompt stdio is already redirected to the null device by the caller.
func configureDetachedProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
}
