//go:build linux

package goservice

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

var (
	rootOnce sync.Once
	root     bool
)

// CheckRoot reports whether the current process is running as root.
var osGetuid = os.Getuid

func CheckRoot() bool {
	rootOnce.Do(func() { root = osGetuid() == 0 })
	return root
}

var (
	sudoOnce sync.Once
	sudo     bool
)

// CanSudo reports whether the current user can run commands with sudo.
var (
	execLookPath = exec.LookPath
	execCommand  = exec.Command
)

func CanSudo() bool {
	sudoOnce.Do(func() {
		if _, err := execLookPath("sudo"); err != nil {
			sudo = false
			return
		}
		sudo = execCommand("sudo", "-n", "true").Run() == nil
	})
	return sudo
}

func withSudo(name string, args ...string) ([]string, error) {
	if CheckRoot() {
		return append([]string{name}, args...), nil
	}
	if !CanSudo() {
		return nil, fmt.Errorf("administrative privileges required, but sudo is not available")
	}
	return append([]string{"sudo", name}, args...), nil
}

func runCommand(name string, args ...string) error {
	cmd := execCommand(name, args...)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("command %s %v failed: %w (output: %s)", name, args, err, string(output))
	}
	return nil
}

func _runPrivilegedCommand(name string, args ...string) error {
	full, err := withSudo(name, args...)
	if err != nil {
		return err
	}
	return runCommand(full[0], full[1:]...)
}

func _runPrivilegedCommandWithStdin(name string, args []string, stdin string) error {
	full, err := withSudo(name, args...)
	if err != nil {
		return err
	}
	cmd := execCommand(full[0], full[1:]...)
	cmd.Stdin = strings.NewReader(stdin)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("command %s %v failed: %w (output: %s)", full[0], full[1:], err, string(output))
	}
	return nil
}

func _writePrivilegedFile(path string, content []byte, mode os.FileMode) error {
	cleanPath := filepath.Clean(path)
	if fi, err := os.Lstat(cleanPath); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing to write symlink %s", cleanPath)
	}
	if err := runPrivilegedCommandWithStdin("tee", []string{cleanPath}, string(content)); err != nil {
		return err
	}
	return runPrivilegedCommand("chmod", fmt.Sprintf("%04o", mode), cleanPath)
}

func _removePrivilegedFile(path string) error {
	return runPrivilegedCommand("rm", "-f", path)
}

// Exported package variables let linux.go use them and let tests replace them
// with fakes so that unit tests never call sudo or touch real system paths.
var (
	runPrivilegedCommand          = _runPrivilegedCommand
	runPrivilegedCommandWithStdin = _runPrivilegedCommandWithStdin
	writePrivilegedFile           = _writePrivilegedFile
	removePrivilegedFile          = _removePrivilegedFile
)
