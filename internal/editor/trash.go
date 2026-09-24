package editor

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// moveToTrash moves a file or directory to the operating system's trash (the
// recycle bin on Windows). On platforms without a clean trash API it falls
// back to moving the item into the user's trash directory; if even that is
// not possible it returns an error and the caller keeps the item in place.
func moveToTrash(full string) error {
	switch runtime.GOOS {
	case "windows":
		return winTrash(full)
	case "darwin":
		return macTrash(full)
	default:
		return xdgTrash(full)
	}
}

// winTrash sends the path to the Recycle Bin via the shell FileSystem API.
func winTrash(full string) error {
	st, err := os.Stat(full)
	if err != nil {
		return err
	}
	var script string
	if st.IsDir() {
		script = fmt.Sprintf(
			"Add-Type -AssemblyName Microsoft.VisualBasic; "+
				"[Microsoft.VisualBasic.FileIO.FileSystem]::DeleteDirectory(%q,'OnlyErrorDialogs','SendToRecycleBin')",
			full,
		)
	} else {
		script = fmt.Sprintf(
			"Add-Type -AssemblyName Microsoft.VisualBasic; "+
				"[Microsoft.VisualBasic.FileIO.FileSystem]::DeleteFile(%q,'OnlyErrorDialogs','SendToRecycleBin')",
			full,
		)
	}
	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", script)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("recycle bin: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

// macTrash moves the path into the user's Trash folder.
func macTrash(full string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	trash := filepath.Join(home, ".Trash")
	if err := os.MkdirAll(trash, 0o755); err != nil {
		return err
	}
	return os.Rename(full, filepath.Join(trash, filepath.Base(full)))
}

// xdgTrash moves the path into the freedesktop trash directory
// (~/.local/share/Trash/files) with a companion .trashinfo metadata file.
func xdgTrash(full string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	infoDir := filepath.Join(home, ".local", "share", "Trash", "info")
	filesDir := filepath.Join(home, ".local", "share", "Trash", "files")
	if err := os.MkdirAll(filesDir, 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(infoDir, 0o755); err != nil {
		return err
	}
	base := filepath.Base(full)
	if err := os.Rename(full, filepath.Join(filesDir, base)); err != nil {
		return err
	}
	info := fmt.Sprintf("[Trash Info]\nPath=%s\nDeletionDate=%s\n", absSlash(full), time.Now().Format(time.RFC3339))
	_ = os.WriteFile(filepath.Join(infoDir, base+".trashinfo"), []byte(info), 0o644)
	return nil
}

// absSlash returns an absolute path with forward slashes (the form the
// freedesktop trash spec stores in .trashinfo).
func absSlash(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		abs = p
	}
	return filepath.ToSlash(abs)
}