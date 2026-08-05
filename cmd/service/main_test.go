package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"reflect"
	"runtime"
	"testing"

	"github.com/edsilegxrepo/goservice"
)

func TestSplitArgs(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"a b c", []string{"a", "b", "c"}},
		{`"a b" c`, []string{"a b", "c"}},
		{`'a b' c`, []string{"a b", "c"}},
		{`a\ b`, []string{"a b"}},
		{`a "b c" d`, []string{"a", "b c", "d"}},
		{`a 'b c' d`, []string{"a", "b c", "d"}},
		{`"a\\b"`, []string{`a\b`}},
		{`a\tb`, []string{"a\tb"}},
	}
	for _, c := range cases {
		got := splitArgs(c.in)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("splitArgs(%q) = %v; want %v", c.in, got, c.want)
		}
	}
}

func TestMakeRunnerFactory(t *testing.T) {
	cfg := goservice.Config{BinaryPath: "/bin/echo", Args: []string{"hello"}}
	factory := makeRunnerFactory(cfg)
	runner, err := factory(nil, nil)
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	cr, ok := runner.(*commandRunner)
	if !ok {
		t.Fatalf("expected *commandRunner, got %T", runner)
	}
	if cr.path != cfg.BinaryPath || !reflect.DeepEqual(cr.args, cfg.Args) || cr.exec == nil {
		t.Errorf("runner fields wrong: path=%q args=%v exec=%v", cr.path, cr.args, cr.exec != nil)
	}
	if err := runner.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestCommandRunnerRun(t *testing.T) {
	r := &commandRunner{
		path: "go",
		args: []string{"version"},
		exec: exec.CommandContext,
	}
	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if r.cancel == nil {
		t.Error("cancel not set")
	}
}

func TestCommandRunnerClose(t *testing.T) {
	r := &commandRunner{}
	if err := r.Close(); err != nil {
		t.Fatalf("Close on zero runner: %v", err)
	}
}

func TestParseConfig(t *testing.T) {
	cfg, opts, code := parseConfig([]string{"-name", "svc", "-binary", "/bin/x", "-args", `"a b" c`, "-install"})
	if code != exitOK {
		t.Fatalf("parseConfig code = %d", code)
	}
	if cfg.Name != "svc" || cfg.BinaryPath != "/bin/x" || !opts.install {
		t.Errorf("cfg/opts wrong: %+v %+v", cfg, opts)
	}
	if !reflect.DeepEqual(cfg.Args, []string{"a b", "c"}) {
		t.Errorf("Args = %v", cfg.Args)
	}
}

func TestParseConfigUserNormalization(t *testing.T) {
	cfg, _, code := parseConfig([]string{"-name", "svc", "-user", "a:b", "-install", "-binary", "/bin/x"})
	if code != exitOK {
		t.Fatalf("parseConfig code = %d", code)
	}
	if runtime.GOOS == "windows" {
		if cfg.User != "a:b" || cfg.UserGroup != "" {
			t.Errorf("windows user not preserved: %+v", cfg)
		}
	} else {
		if cfg.User != "" || cfg.UserGroup != "a:b" {
			t.Errorf("linux user not normalized: %+v", cfg)
		}
	}
}

func TestParseConfigUnknownFlag(t *testing.T) {
	if _, _, code := parseConfig([]string{"-nope"}); code != exitUsage {
		t.Errorf("code = %d; want exitUsage", code)
	}
}

func TestValidateMode(t *testing.T) {
	cases := []struct {
		name string
		cfg  goservice.Config
		opts cliOptions
		want int
	}{
		{"missing name", goservice.Config{}, cliOptions{install: true}, exitUsage},
		{"no mode", goservice.Config{Name: "x"}, cliOptions{}, exitUsage},
		{"two modes", goservice.Config{Name: "x", BinaryPath: "/bin/x"}, cliOptions{install: true, remove: true}, exitUsage},
		{"install missing binary", goservice.Config{Name: "x"}, cliOptions{install: true}, exitUsage},
		{"valid install", goservice.Config{Name: "x", BinaryPath: "/bin/x"}, cliOptions{install: true}, exitOK},
		{"valid remove", goservice.Config{Name: "x"}, cliOptions{remove: true}, exitOK},
	}
	for _, c := range cases {
		if got := validateMode(c.cfg, c.opts); got != c.want {
			t.Errorf("%s: validateMode = %d; want %d", c.name, got, c.want)
		}
	}
}

func TestExitCodeForError(t *testing.T) {
	cases := []struct {
		err  error
		want int
	}{
		{goservice.ErrInvalidConfig, exitValidation},
		{goservice.ErrAlreadyExists, exitAlreadyExists},
		{goservice.ErrNotInstalled, exitNotInstalled},
		{goservice.ErrPermissionDenied, exitPermission},
		{errors.New("boom"), exitRuntime},
	}
	for _, c := range cases {
		if got := exitCodeForError(c.err); got != c.want {
			t.Errorf("exitCodeForError(%v) = %d; want %d", c.err, got, c.want)
		}
	}
}

func TestScrubSecrets(t *testing.T) {
	if err := os.Setenv("SERVICE_PASS", "secret"); err != nil {
		t.Fatal(err)
	}
	cfg := &goservice.Config{Pass: "secret"}
	scrubSecrets(cfg)
	if cfg.Pass != "" {
		t.Error("Pass not cleared")
	}
	if v := os.Getenv("SERVICE_PASS"); v != "" {
		t.Errorf("SERVICE_PASS not unset: %q", v)
	}
}

type serviceOps struct {
	isService  bool
	installs   []goservice.Config
	installErr error
	removes    []goservice.Config
	removeErr  error
	runs       []goservice.Config
	runErr     error
}

func fakeServiceOps(t *testing.T) *serviceOps {
	t.Helper()
	ops := &serviceOps{}
	oldIs, oldI, oldR, oldRun := isServiceFn, installFn, removeFn, runFn
	isServiceFn = func() bool { return ops.isService }
	installFn = func(c goservice.Config) error { ops.installs = append(ops.installs, c); return ops.installErr }
	removeFn = func(c goservice.Config) error { ops.removes = append(ops.removes, c); return ops.removeErr }
	runFn = func(c goservice.Config, f goservice.RunnerFactory) error {
		ops.runs = append(ops.runs, c)
		return ops.runErr
	}
	t.Cleanup(func() { isServiceFn, installFn, removeFn, runFn = oldIs, oldI, oldR, oldRun })
	return ops
}

func TestRunInstallSuccess(t *testing.T) {
	ops := fakeServiceOps(t)
	if code := run([]string{"-name", "svc", "-binary", "/bin/x", "-install"}); code != exitOK {
		t.Fatalf("run code = %d; want exitOK", code)
	}
	if len(ops.installs) != 1 {
		t.Fatalf("expected 1 install call, got %d", len(ops.installs))
	}
}

func TestRunInstallAlreadyExists(t *testing.T) {
	ops := fakeServiceOps(t)
	ops.installErr = goservice.ErrAlreadyExists
	if code := run([]string{"-name", "svc", "-binary", "/bin/x", "-install"}); code != exitAlreadyExists {
		t.Fatalf("run code = %d; want exitAlreadyExists", code)
	}
}

func TestRunUsageError(t *testing.T) {
	ops := fakeServiceOps(t)
	if code := run([]string{"-install"}); code != exitUsage {
		t.Fatalf("run code = %d; want exitUsage", code)
	}
	if len(ops.installs) != 0 {
		t.Error("backend should not be called on usage error")
	}
}

func TestRunRemoveNotInstalled(t *testing.T) {
	ops := fakeServiceOps(t)
	ops.removeErr = goservice.ErrNotInstalled
	if code := run([]string{"-name", "svc", "-remove"}); code != exitNotInstalled {
		t.Fatalf("run code = %d; want exitNotInstalled", code)
	}
}

func TestRunAutoRunWhenService(t *testing.T) {
	ops := fakeServiceOps(t)
	ops.isService = true
	if code := run([]string{"-name", "svc", "-binary", "/bin/x"}); code != exitOK {
		t.Fatalf("run code = %d; want exitOK", code)
	}
	if len(ops.runs) != 1 {
		t.Fatalf("expected 1 run call, got %d", len(ops.runs))
	}
}

func TestRunContextCanceled(t *testing.T) {
	ops := fakeServiceOps(t)
	ops.runErr = context.Canceled
	if code := run([]string{"-name", "svc", "-binary", "/bin/x", "-run"}); code != exitOK {
		t.Fatalf("run code = %d; want exitOK", code)
	}
}

func TestRunRuntimeError(t *testing.T) {
	ops := fakeServiceOps(t)
	ops.runErr = errors.New("boom")
	if code := run([]string{"-name", "svc", "-binary", "/bin/x", "-run"}); code != exitRuntime {
		t.Fatalf("run code = %d; want exitRuntime", code)
	}
}

func TestRunScrubsSecrets(t *testing.T) {
	if err := os.Setenv("SERVICE_PASS", "secret"); err != nil {
		t.Fatal(err)
	}
	ops := fakeServiceOps(t)
	if code := run([]string{"-name", "svc", "-binary", "/bin/x", "-install"}); code != exitOK {
		t.Fatalf("run code = %d; want exitOK", code)
	}
	if len(ops.installs) != 1 || ops.installs[0].Pass != "secret" {
		t.Errorf("install did not receive env password: %+v", ops.installs)
	}
	if v := os.Getenv("SERVICE_PASS"); v != "" {
		t.Errorf("SERVICE_PASS not unset: %q", v)
	}
}
