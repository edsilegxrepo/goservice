//go:build linux

package goservice

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
)

func resetRootSudo(t *testing.T) {
	t.Helper()
	rootOnce = sync.Once{}
	sudoOnce = sync.Once{}
	root = false
	sudo = false
}

func TestCheckRoot(t *testing.T) {
	resetRootSudo(t)
	old := osGetuid
	t.Cleanup(func() { osGetuid = old })
	osGetuid = func() int { return 0 }
	if !CheckRoot() {
		t.Error("CheckRoot false for uid 0")
	}
}

func TestCheckNotRoot(t *testing.T) {
	resetRootSudo(t)
	old := osGetuid
	t.Cleanup(func() { osGetuid = old })
	osGetuid = func() int { return 1000 }
	if CheckRoot() {
		t.Error("CheckRoot true for uid 1000")
	}
}

func TestCanSudoWhenSudoAvailable(t *testing.T) {
	resetRootSudo(t)
	oldLP := execLookPath
	oldCmd := execCommand
	t.Cleanup(func() { execLookPath = oldLP; execCommand = oldCmd })
	execLookPath = func(string) (string, error) { return "/usr/bin/sudo", nil }
	execCommand = func(name string, args ...string) *exec.Cmd {
		return exec.Command("true")
	}
	if !CanSudo() {
		t.Error("CanSudo false for available sudo")
	}
}

func TestCanSudoWhenSudoMissing(t *testing.T) {
	resetRootSudo(t)
	oldLP := execLookPath
	t.Cleanup(func() { execLookPath = oldLP })
	execLookPath = func(string) (string, error) { return "", errors.New("not found") }
	if CanSudo() {
		t.Error("CanSudo true when sudo missing")
	}
}

func TestWithSudoAsRoot(t *testing.T) {
	resetRootSudo(t)
	old := osGetuid
	t.Cleanup(func() { osGetuid = old })
	osGetuid = func() int { return 0 }
	args, err := withSudo("ls", "-l")
	if err != nil {
		t.Fatalf("withSudo: %v", err)
	}
	if len(args) != 2 || args[0] != "ls" || args[1] != "-l" {
		t.Errorf("withSudo root args = %v", args)
	}
}

func TestWithSudoAsNonRoot(t *testing.T) {
	resetRootSudo(t)
	oldUID := osGetuid
	oldLP := execLookPath
	oldCmd := execCommand
	t.Cleanup(func() {
		osGetuid = oldUID
		execLookPath = oldLP
		execCommand = oldCmd
	})
	osGetuid = func() int { return 1000 }
	execLookPath = func(string) (string, error) { return "/usr/bin/sudo", nil }
	execCommand = func(name string, args ...string) *exec.Cmd {
		return exec.Command("true")
	}
	args, err := withSudo("ls", "-l")
	if err != nil {
		t.Fatalf("withSudo: %v", err)
	}
	if len(args) != 3 || args[0] != "sudo" || args[1] != "ls" || args[2] != "-l" {
		t.Errorf("withSudo non-root args = %v", args)
	}
}

func TestWritePrivilegedFileReturnsError(t *testing.T) {
	old := runPrivilegedCommandWithStdin
	t.Cleanup(func() { runPrivilegedCommandWithStdin = old })
	runPrivilegedCommandWithStdin = func(string, []string, string) error { return errors.New("fail") }
	if err := writePrivilegedFile("/x", []byte("x"), 0o644); err == nil {
		t.Fatal("expected error from writePrivilegedFile")
	}
}

func TestRunCommandError(t *testing.T) {
	old := execCommand
	t.Cleanup(func() { execCommand = old })
	execCommand = func(name string, args ...string) *exec.Cmd { return exec.Command("false") }
	if err := runCommand("anything"); err == nil {
		t.Fatal("expected command error")
	}
}

func TestRunPrivilegedCommandSudoError(t *testing.T) {
	resetRootSudo(t)
	oldUID, oldLP := osGetuid, execLookPath
	t.Cleanup(func() { osGetuid, execLookPath = oldUID, oldLP })
	osGetuid = func() int { return 1000 }
	execLookPath = func(string) (string, error) { return "", errors.New("no sudo") }
	if err := _runPrivilegedCommand("ls"); err == nil {
		t.Fatal("expected sudo unavailable error")
	}
}

func TestRunPrivilegedCommandWithStdinSudoError(t *testing.T) {
	resetRootSudo(t)
	oldUID, oldLP := osGetuid, execLookPath
	t.Cleanup(func() { osGetuid, execLookPath = oldUID, oldLP })
	osGetuid = func() int { return 1000 }
	execLookPath = func(string) (string, error) { return "", errors.New("no sudo") }
	if err := _runPrivilegedCommandWithStdin("tee", []string{"/x"}, "x"); err == nil {
		t.Fatal("expected sudo unavailable error")
	}
}

func TestRemovePrivilegedFileError(t *testing.T) {
	old := runPrivilegedCommand
	t.Cleanup(func() { runPrivilegedCommand = old })
	runPrivilegedCommand = func(string, ...string) error { return errors.New("rm") }
	if err := removePrivilegedFile("/x"); err == nil {
		t.Fatal("expected remove error")
	}
}

func TestWritePrivilegedFileSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks not supported: %v", err)
	}
	if err := writePrivilegedFile(link, []byte("x"), 0o644); err == nil {
		t.Fatal("expected symlink rejection")
	}
}
