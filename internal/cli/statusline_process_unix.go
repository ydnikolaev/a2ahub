//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package cli

import (
	"os/exec"
	"syscall"
)

// configureDetachedProcess starts the refresh in its own session. Releasing
// os.Process only releases the Go handle; it does not detach a child from the
// short-lived shell/statusline process's session or process group.
func configureDetachedProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
