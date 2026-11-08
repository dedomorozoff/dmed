//go:build windows

package update

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

// restart launches a new dmed with the same arguments, then exits the current
// one. On Windows the rename Apply performed leaves the old process holding
// the .old file open, so the relaunch is the only way the user sees the new
// version and frees the backup. The new process is started in its own
// process group so the terminal does not kill it when the old editor exits.
func restart(args []string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	cmd := exec.Command(exe, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Dir, _ = os.Getwd()
	if cmd.Dir == "" {
		cmd.Dir = "."
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
	if err := cmd.Start(); err != nil {
		return err
	}
	os.Exit(0)
	return nil
}
