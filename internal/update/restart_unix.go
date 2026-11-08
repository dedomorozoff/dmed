//go:build linux || darwin || freebsd

package update

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

// restart launches a new dmed with the same arguments, detached from this
// process group so the terminal sends its signals to the new process, then
// exits the current one. On POSIX the rename Apply performed leaves the old
// binary on disk as the running image, so the relaunch is the only way the
// user sees the new version.
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
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	os.Exit(0)
	return nil
}
