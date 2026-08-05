package goservice

import (
	"context"
	"errors"
	"io"
	"log"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type fakeManager struct {
	isServiceVal bool
	isErr        error
	installs     []Config
	installErr   error
	removes      []Config
	removeErr    error
	runs         []Config
	runErr       error
	factory      RunnerFactory
}

func (f *fakeManager) isService() (bool, error) { return f.isServiceVal, f.isErr }
func (f *fakeManager) install(cfg Config) error {
	f.installs = append(f.installs, cfg)
	return f.installErr
}

func (f *fakeManager) remove(cfg Config) error {
	f.removes = append(f.removes, cfg)
	return f.removeErr
}

func (f *fakeManager) runService(cfg Config, factory RunnerFactory, logger Logger) error {
	f.runs = append(f.runs, cfg)
	f.factory = factory
	return f.runErr
}

func setFakeManager(t *testing.T, f *fakeManager) {
	t.Helper()
	old := defaultManager
	defaultManager = f
	t.Cleanup(func() { defaultManager = old })
}

func writeTempBinary(t *testing.T) string {
	t.Helper()
	d := t.TempDir()
	f := filepath.Join(d, "svc")
	if err := os.WriteFile(f, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestValidateName(t *testing.T) {
	cases := []struct {
		in string
		ok bool
	}{
		{"foo", true},
		{"foo-bar", true},
		{"foo.bar", true},
		{"foo_bar", true},
		{"Foo1", true},
		{"", false},
		{"-foo", false},
		{"foo bar", false},
		{"foo/bar", false},
		{"foo@bar", false},
		{string(make([]byte, 257)), false},
	}
	for _, c := range cases {
		err := validateName(c.in, 256)
		if c.ok && err != nil {
			t.Errorf("validateName(%q) = %v; want nil", c.in, err)
		}
		if !c.ok && err == nil {
			t.Errorf("validateName(%q) = nil; want error", c.in)
		}
	}
}

func TestValidateInstance(t *testing.T) {
	if err := validateInstance("default"); err != nil {
		t.Errorf("validateInstance('default') = %v; want nil", err)
	}
	if err := validateInstance("foo bar"); err == nil {
		t.Error("validateInstance('foo bar') = nil; want error")
	}
}

func TestValidateBinaryPath(t *testing.T) {
	f := writeTempBinary(t)
	d := filepath.Dir(f)
	if err := validateBinaryPath(f); err != nil {
		t.Errorf("validateBinaryPath(%q) = %v; want nil", f, err)
	}
	if err := validateBinaryPath(filepath.Join(d, "missing")); err == nil {
		t.Error("expected error for missing file")
	}
	if err := validateBinaryPath("relative/path"); err == nil {
		t.Error("expected error for relative path")
	}
	if err := validateBinaryPath(d); err == nil {
		t.Error("expected error for directory")
	}
}

func TestInstallValidation(t *testing.T) {
	setFakeManager(t, &fakeManager{})
	if err := Install(Config{}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("expected ErrInvalidConfig, got %v", err)
	}
}

func TestInstallDefaults(t *testing.T) {
	f := &fakeManager{}
	setFakeManager(t, f)
	bin := writeTempBinary(t)
	if err := Install(Config{Name: "svc", BinaryPath: bin}); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if len(f.installs) != 1 {
		t.Fatalf("expected 1 install call, got %d", len(f.installs))
	}
	got := f.installs[0]
	if got.DisplayName != "svc" {
		t.Errorf("DisplayName = %q; want svc", got.DisplayName)
	}
	if got.Description != "svc service" {
		t.Errorf("Description = %q; want 'svc service'", got.Description)
	}
}

func TestInstallDefaultsBinary(t *testing.T) {
	f := &fakeManager{}
	setFakeManager(t, f)
	if err := Install(Config{Name: "svc"}); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if f.installs[0].BinaryPath == "" {
		t.Error("BinaryPath not defaulted to os.Executable")
	}
}

func TestInstallPassesManagerError(t *testing.T) {
	f := &fakeManager{installErr: errors.New("boom")}
	setFakeManager(t, f)
	bin := writeTempBinary(t)
	if err := Install(Config{Name: "svc", BinaryPath: bin}); err == nil {
		t.Fatal("expected error from manager")
	}
}

func TestRemove(t *testing.T) {
	f := &fakeManager{}
	setFakeManager(t, f)
	if err := Remove(Config{Name: "svc"}); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if len(f.removes) != 1 {
		t.Fatalf("expected 1 remove call, got %d", len(f.removes))
	}
}

func TestRemoveValidation(t *testing.T) {
	setFakeManager(t, &fakeManager{})
	if err := Remove(Config{}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("expected ErrInvalidConfig, got %v", err)
	}
}

func TestRun(t *testing.T) {
	f := &fakeManager{}
	setFakeManager(t, f)
	factory := func([]string, Logger) (Runner, error) { return &fakeRunner{}, nil }
	if err := Run(Config{Name: "svc"}, factory); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(f.runs) != 1 {
		t.Fatalf("expected 1 run call, got %d", len(f.runs))
	}
}

func TestRunNilFactory(t *testing.T) {
	setFakeManager(t, &fakeManager{})
	if err := Run(Config{Name: "svc"}, nil); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("expected ErrInvalidConfig, got %v", err)
	}
}

func TestIsService(t *testing.T) {
	old := defaultManager
	t.Cleanup(func() { defaultManager = old })

	defaultManager = &fakeManager{isServiceVal: true}
	if !IsService() {
		t.Error("IsService() = false; want true")
	}
	defaultManager = &fakeManager{isErr: errors.New("err")}
	if IsService() {
		t.Error("IsService() = true; want false on error")
	}
}

type fakeRunner struct{}

func (fakeRunner) Run(ctx context.Context) error { return nil }
func (fakeRunner) Close() error                  { return nil }

func TestIsServiceErr(t *testing.T) {
	old := defaultManager
	t.Cleanup(func() { defaultManager = old })

	defaultManager = &fakeManager{isServiceVal: true}
	ok, err := IsServiceErr()
	if err != nil || !ok {
		t.Errorf("IsServiceErr() = %v, %v; want true, nil", ok, err)
	}
	defaultManager = &fakeManager{isErr: errors.New("err")}
	if _, err := IsServiceErr(); err == nil {
		t.Error("IsServiceErr() expected error")
	}
}

func TestManagerAdapter(t *testing.T) {
	var calls []string
	a := managerAdapter{
		isServiceFn: func() (bool, error) { calls = append(calls, "isService"); return true, nil },
		installFn:   func(Config) error { calls = append(calls, "install"); return nil },
		removeFn:    func(Config) error { calls = append(calls, "remove"); return nil },
		runServiceFn: func(Config, RunnerFactory, Logger) error {
			calls = append(calls, "runService")
			return nil
		},
	}
	if _, err := a.isService(); err != nil {
		t.Fatalf("isService: %v", err)
	}
	if err := a.install(Config{}); err != nil {
		t.Fatalf("install: %v", err)
	}
	if err := a.remove(Config{}); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if err := a.runService(Config{}, nil, nil); err != nil {
		t.Fatalf("runService: %v", err)
	}
	if len(calls) != 4 {
		t.Errorf("calls = %v", calls)
	}
}

func TestConsoleLogger(t *testing.T) {
	old := log.Writer()
	log.SetOutput(io.Discard)
	t.Cleanup(func() { log.SetOutput(old) })

	var c consoleLogger
	if err := c.Error(1, "err"); err != nil {
		t.Errorf("Error: %v", err)
	}
	if err := c.Info(2, "info"); err != nil {
		t.Errorf("Info: %v", err)
	}
	c.Warn("warn")
	if err := c.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}

func TestNewLogger(t *testing.T) {
	for _, isService := range []bool{false, true} {
		l, err := NewLogger("goservice-test", isService)
		if err != nil {
			t.Fatalf("NewLogger(%v): %v", isService, err)
		}
		if l == nil {
			t.Fatalf("NewLogger(%v) returned nil", isService)
		}
		if err := l.Close(); err != nil {
			t.Fatalf("Close(%v): %v", isService, err)
		}
	}
}

func TestValidateBinaryPathInvalidChars(t *testing.T) {
	p := writeTempBinary(t)
	if err := validateBinaryPath(p + `"`); !errors.Is(err, ErrInvalidConfig) {
		t.Errorf("expected ErrInvalidConfig for quoted path, got %v", err)
	}
}

func TestStopTimeoutDefaults(t *testing.T) {
	if got := stopTimeout(Config{}); got != 30*time.Second {
		t.Errorf("stopTimeout default = %v", got)
	}
	if got := stopPollInterval(Config{}); got != 300*time.Millisecond {
		t.Errorf("stopPollInterval default = %v", got)
	}
	if got := stopTimeout(Config{StopTimeout: time.Second}); got != time.Second {
		t.Errorf("stopTimeout custom = %v", got)
	}
}
