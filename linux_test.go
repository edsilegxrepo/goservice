//go:build linux

package goservice

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/user"
	"strings"
	"testing"
	"time"
)

type fakeFileInfo struct{}

func (fakeFileInfo) Name() string       { return "template" }
func (fakeFileInfo) Size() int64        { return 0 }
func (fakeFileInfo) Mode() os.FileMode  { return 0o644 }
func (fakeFileInfo) ModTime() time.Time { return time.Time{} }
func (fakeFileInfo) IsDir() bool        { return false }
func (fakeFileInfo) Sys() any           { return nil }

func TestSystemdEscape(t *testing.T) {
	if got := systemdEscape("a\nb"); got != "a b" {
		t.Errorf("systemdEscape got %q; want %q", got, "a b")
	}
	if got := systemdEscape("a%b"); got != "a%%b" {
		t.Errorf("systemdEscape got %q; want %q", got, "a%%b")
	}
}

func TestSystemdArg(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"foo", "foo"},
		{"foo bar", `"foo bar"`},
		{`foo"bar`, `"foo\"bar"`},
		{"foo%bar", `"foo%%bar"`},
		{"", `""`},
	}
	for _, c := range cases {
		got := systemdArg(c.in)
		if got != c.want {
			t.Errorf("systemdArg(%q) = %q; want %q", c.in, got, c.want)
		}
	}
}

func TestQuoteSystemdArgs(t *testing.T) {
	got := quoteSystemdArgs("/bin/foo", []string{"a b", `c"d`})
	want := `/bin/foo "a b" "c\"d"`
	if got != want {
		t.Errorf("quoteSystemdArgs = %q; want %q", got, want)
	}
}

func TestBuildUnitData(t *testing.T) {
	old := userLookup
	t.Cleanup(func() { userLookup = old })
	userLookup = func(name string) (*user.User, error) {
		if name == "svc" {
			return &user.User{HomeDir: "/home/svc"}, nil
		}
		return nil, fmt.Errorf("unknown user %s", name)
	}

	cases := []struct {
		cfg                 Config
		wantUser, wantGroup string
		wantHome, wantExec  string
		wantErr             bool
	}{
		{cfg: Config{BinaryPath: "/bin/x", Args: []string{"a"}}, wantUser: "", wantGroup: "", wantHome: "/root", wantExec: `/bin/x a`},
		{cfg: Config{BinaryPath: "/bin/x", UserGroup: "svc"}, wantUser: "svc", wantGroup: "", wantHome: "/home/svc", wantExec: `/bin/x`},
		{cfg: Config{BinaryPath: "/bin/x", UserGroup: "svc:staff"}, wantUser: "svc", wantGroup: "staff", wantHome: "/home/svc", wantExec: `/bin/x`},
		{cfg: Config{BinaryPath: "/bin/x", UserGroup: "bad name"}, wantErr: true},
		{cfg: Config{BinaryPath: "/bin/x", UserGroup: "user:bad group"}, wantErr: true},
	}
	for _, c := range cases {
		got, err := buildUnitData(c.cfg)
		if c.wantErr {
			if err == nil {
				t.Errorf("buildUnitData(%+v) = nil; want error", c.cfg)
			}
			continue
		}
		if err != nil {
			t.Errorf("buildUnitData(%+v) = %v; want nil", c.cfg, err)
			continue
		}
		if got.User != c.wantUser || got.Group != c.wantGroup || got.Home != c.wantHome || got.ExecStart != c.wantExec {
			t.Errorf("buildUnitData(%+v) = %+v; want user=%q group=%q home=%q exec=%q", c.cfg, got, c.wantUser, c.wantGroup, c.wantHome, c.wantExec)
		}
	}
}

func TestBuildUnitDataEscapesDescription(t *testing.T) {
	got, err := buildUnitData(Config{Description: "line\nbreak%", BinaryPath: "/bin/x"})
	if err != nil {
		t.Fatalf("buildUnitData: %v", err)
	}
	if strings.Contains(got.Description, "\n") || !strings.Contains(got.Description, "%%") {
		t.Errorf("Description not escaped: %q", got.Description)
	}
}

func TestRenderUnitDefault(t *testing.T) {
	cfg := Config{Name: "svc"}
	data := unitData{Description: "my svc", ExecStart: "/bin/x"}
	b, err := renderUnit(cfg, data)
	if err != nil {
		t.Fatalf("renderUnit: %v", err)
	}
	if !bytes.Contains(b, []byte("ExecStart=/bin/x")) {
		t.Fatalf("missing ExecStart: %s", b)
	}
	if !bytes.Contains(b, []byte("Description=my svc")) {
		t.Fatalf("missing Description: %s", b)
	}
}

func TestRenderUnitCustomTemplate(t *testing.T) {
	oldRead := readFile
	oldLstat := lstatFile
	t.Cleanup(func() { readFile = oldRead; lstatFile = oldLstat })
	readFile = func(string) ([]byte, error) {
		return []byte("[Service]\nExecStart={{.ExecStart}}"), nil
	}
	lstatFile = func(string) (os.FileInfo, error) { return fakeFileInfo{}, nil }
	b, err := renderUnit(Config{TemplatePath: "/x"}, unitData{ExecStart: "/bin/x"})
	if err != nil {
		t.Fatalf("renderUnit: %v", err)
	}
	if !bytes.Contains(b, []byte("ExecStart=/bin/x")) {
		t.Fatalf("missing ExecStart: %s", b)
	}
}

func TestRenderUnitTemplateReadError(t *testing.T) {
	oldRead := readFile
	oldLstat := lstatFile
	t.Cleanup(func() { readFile = oldRead; lstatFile = oldLstat })
	readFile = func(string) ([]byte, error) { return nil, errors.New("fail") }
	lstatFile = func(string) (os.FileInfo, error) { return fakeFileInfo{}, nil }
	if _, err := renderUnit(Config{TemplatePath: "/x"}, unitData{}); err == nil {
		t.Fatal("expected error for missing template")
	}
}

type linuxCallLog struct {
	writes []struct {
		path    string
		content []byte
		mode    os.FileMode
	}
	removes []string
	run     []string
}

func linuxMocks(t *testing.T, log *linuxCallLog) {
	t.Helper()
	oldWrite := writePrivilegedFile
	oldRemove := removePrivilegedFile
	oldRun := runPrivilegedCommand
	t.Cleanup(func() {
		writePrivilegedFile = oldWrite
		removePrivilegedFile = oldRemove
		runPrivilegedCommand = oldRun
	})
	writePrivilegedFile = func(path string, content []byte, mode os.FileMode) error {
		log.writes = append(log.writes, struct {
			path    string
			content []byte
			mode    os.FileMode
		}{path, content, mode})
		return nil
	}
	removePrivilegedFile = func(path string) error { log.removes = append(log.removes, path); return nil }
	runPrivilegedCommand = func(name string, args ...string) error {
		log.run = append(log.run, strings.Join(append([]string{name}, args...), " "))
		return nil
	}
}

func TestInstallSimple(t *testing.T) {
	var log linuxCallLog
	linuxMocks(t, &log)
	cfg := Config{Name: "svc", BinaryPath: "/bin/x"}
	if err := install(cfg); err != nil {
		t.Fatalf("install: %v", err)
	}
	if len(log.writes) != 1 {
		t.Fatalf("expected 1 write, got %d", len(log.writes))
	}
	if log.writes[0].path != "/etc/systemd/system/svc.service" {
		t.Errorf("path = %q; want /etc/systemd/system/svc.service", log.writes[0].path)
	}
	if !strings.Contains(string(log.writes[0].content), "[Unit]") {
		t.Errorf("content missing [Unit]: %s", log.writes[0].content)
	}
	if len(log.run) != 2 || !strings.Contains(log.run[0], "daemon-reload") || !strings.Contains(log.run[1], "enable svc") {
		t.Errorf("unexpected systemctl calls: %v", log.run)
	}
}

func TestInstallTemplated(t *testing.T) {
	var log linuxCallLog
	linuxMocks(t, &log)
	cfg := Config{Name: "svc", BinaryPath: "/bin/x", Instance: "inst"}
	if err := install(cfg); err != nil {
		t.Fatalf("install: %v", err)
	}
	if log.writes[0].path != "/etc/systemd/system/svc@.service" {
		t.Errorf("path = %q", log.writes[0].path)
	}
	if !strings.Contains(log.run[1], "enable svc@inst") {
		t.Errorf("expected enable svc@inst, got %v", log.run)
	}
}

func TestInstallWriteFails(t *testing.T) {
	old := writePrivilegedFile
	t.Cleanup(func() { writePrivilegedFile = old })
	writePrivilegedFile = func(string, []byte, os.FileMode) error { return errors.New("denied") }
	if err := install(Config{Name: "svc", BinaryPath: "/bin/x"}); err == nil {
		t.Fatal("expected error on write")
	}
}

func TestRemoveSimple(t *testing.T) {
	var log linuxCallLog
	linuxMocks(t, &log)
	if err := remove(Config{Name: "svc"}); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if len(log.removes) != 1 || log.removes[0] != "/etc/systemd/system/svc.service" {
		t.Errorf("removes = %v", log.removes)
	}
	if len(log.run) != 3 {
		t.Errorf("expected 3 systemctl calls, got %v", log.run)
	}
}

func TestRemoveDisableFails(t *testing.T) {
	old := runPrivilegedCommand
	t.Cleanup(func() { runPrivilegedCommand = old })
	var calls int
	runPrivilegedCommand = func(name string, args ...string) error {
		calls++
		if strings.Contains(strings.Join(args, " "), "disable") {
			return errors.New("no")
		}
		return nil
	}
	if err := remove(Config{Name: "svc"}); err == nil {
		t.Fatal("expected error on disable")
	}
	if calls == 0 {
		t.Error("no systemctl calls recorded")
	}
}

type linuxFakeRunner struct {
	ran      bool
	closed   bool
	err      error
	closeErr error
}

func (r *linuxFakeRunner) Run(ctx context.Context) error { r.ran = true; return r.err }
func (r *linuxFakeRunner) Close() error                  { r.closed = true; return r.closeErr }

func TestLinuxRunService(t *testing.T) {
	runner := &linuxFakeRunner{err: context.Canceled}
	factory := func([]string, Logger) (Runner, error) { return runner, nil }
	err := runService(Config{Name: "svc", Args: []string{"a"}}, factory, &consoleLogger{})
	if err != context.Canceled {
		t.Fatalf("runService err = %v; want context.Canceled", err)
	}
	if !runner.ran || !runner.closed {
		t.Errorf("runner ran=%v closed=%v", runner.ran, runner.closed)
	}
}

func TestLinuxIsService(t *testing.T) {
	ok, err := isService()
	if err != nil || ok {
		t.Errorf("isService() = %v, %v; want false, nil", ok, err)
	}
}

func TestBuildUnitDataUserFallback(t *testing.T) {
	old := userLookup
	t.Cleanup(func() { userLookup = old })
	userLookup = func(name string) (*user.User, error) {
		return &user.User{HomeDir: "/home/svc"}, nil
	}
	got, err := buildUnitData(Config{BinaryPath: "/bin/x", User: "svc"})
	if err != nil {
		t.Fatalf("buildUnitData: %v", err)
	}
	if got.User != "svc" || got.Home != "/home/svc" {
		t.Errorf("got %+v", got)
	}
}

func TestRenderUnitParseError(t *testing.T) {
	oldRead := readFile
	oldLstat := lstatFile
	t.Cleanup(func() { readFile = oldRead; lstatFile = oldLstat })
	readFile = func(string) ([]byte, error) { return []byte("{{"), nil }
	lstatFile = func(string) (os.FileInfo, error) { return fakeFileInfo{}, nil }
	if _, err := renderUnit(Config{TemplatePath: "/x"}, unitData{}); err == nil {
		t.Fatal("expected template parse error")
	}
}

func TestInstallDaemonReloadFails(t *testing.T) {
	oldWrite := writePrivilegedFile
	oldRun := runPrivilegedCommand
	t.Cleanup(func() { writePrivilegedFile = oldWrite; runPrivilegedCommand = oldRun })
	writePrivilegedFile = func(string, []byte, os.FileMode) error { return nil }
	runPrivilegedCommand = func(name string, args ...string) error {
		if len(args) > 0 && args[0] == "daemon-reload" {
			return errors.New("reload")
		}
		return nil
	}
	if err := install(Config{Name: "svc", BinaryPath: "/bin/x"}); err == nil {
		t.Fatal("expected daemon-reload error")
	}
}

func TestInstallEnableFails(t *testing.T) {
	oldWrite := writePrivilegedFile
	oldRun := runPrivilegedCommand
	t.Cleanup(func() { writePrivilegedFile = oldWrite; runPrivilegedCommand = oldRun })
	writePrivilegedFile = func(string, []byte, os.FileMode) error { return nil }
	runPrivilegedCommand = func(name string, args ...string) error {
		if len(args) > 0 && args[0] == "enable" {
			return errors.New("enable")
		}
		return nil
	}
	if err := install(Config{Name: "svc", BinaryPath: "/bin/x"}); err == nil {
		t.Fatal("expected enable error")
	}
}

func TestRemoveDaemonReloadFails(t *testing.T) {
	oldRun := runPrivilegedCommand
	oldRemove := removePrivilegedFile
	t.Cleanup(func() { runPrivilegedCommand = oldRun; removePrivilegedFile = oldRemove })
	runPrivilegedCommand = func(name string, args ...string) error {
		if len(args) > 0 && args[0] == "daemon-reload" {
			return errors.New("reload")
		}
		return nil
	}
	removePrivilegedFile = func(string) error { return nil }
	if err := remove(Config{Name: "svc"}); err == nil {
		t.Fatal("expected daemon-reload error")
	}
}

func TestRemoveFileFails(t *testing.T) {
	oldRun := runPrivilegedCommand
	oldRemove := removePrivilegedFile
	t.Cleanup(func() { runPrivilegedCommand = oldRun; removePrivilegedFile = oldRemove })
	runPrivilegedCommand = func(string, ...string) error { return nil }
	removePrivilegedFile = func(string) error { return errors.New("rm") }
	if err := remove(Config{Name: "svc"}); err == nil {
		t.Fatal("expected remove file error")
	}
}

func TestLinuxRunServiceRunnerError(t *testing.T) {
	runner := &linuxFakeRunner{err: errors.New("boom")}
	factory := func([]string, Logger) (Runner, error) { return runner, nil }
	if err := runService(Config{Name: "svc"}, factory, &consoleLogger{}); err == nil {
		t.Fatal("expected runner error")
	}
	if !runner.closed {
		t.Error("runner not closed")
	}
}

func TestLinuxRunServiceCloseError(t *testing.T) {
	runner := &linuxFakeRunner{closeErr: errors.New("close")}
	factory := func([]string, Logger) (Runner, error) { return runner, nil }
	if err := runService(Config{Name: "svc"}, factory, &consoleLogger{}); err == nil {
		t.Fatal("expected close error")
	}
}
