//go:build windows

package goservice

import (
	"context"
	"errors"
	"testing"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

type fakeServiceHandle struct {
	closeCalls    int
	deleteErr     error
	controlErr    error
	controlStatus svc.Status
	queryStatus   svc.Status
	queryErr      error
}

func (s *fakeServiceHandle) Close() error  { s.closeCalls++; return nil }
func (s *fakeServiceHandle) Delete() error { return s.deleteErr }
func (s *fakeServiceHandle) Control(c svc.Cmd) (svc.Status, error) {
	if s.controlErr != nil {
		return svc.Status{}, s.controlErr
	}
	if s.controlStatus.State == 0 {
		return svc.Status{State: svc.Stopped}, nil
	}
	return s.controlStatus, nil
}

func (s *fakeServiceHandle) Query() (svc.Status, error) {
	if s.queryErr != nil {
		return svc.Status{}, s.queryErr
	}
	if s.queryStatus.State == 0 {
		return svc.Status{State: svc.Stopped}, nil
	}
	return s.queryStatus, nil
}

type fakeConn struct {
	openErr      error
	openHandle   serviceHandle
	createErr    error
	createHandle serviceHandle
}

func (c *fakeConn) Disconnect() error                              { return nil }
func (c *fakeConn) OpenService(name string) (serviceHandle, error) { return c.openHandle, c.openErr }

func (c *fakeConn) CreateService(name, exepath string, cfg mgr.Config, args ...string) (serviceHandle, error) {
	return c.createHandle, c.createErr
}

type fakeWindowsDeps struct {
	isSvc      bool
	isErr      error
	runName    string
	runHandler svc.Handler
	conn       serviceConn
	connErr    error
	installErr error
	removeErr  error
}

func (d *fakeWindowsDeps) isService() (bool, error) { return d.isSvc, d.isErr }
func (d *fakeWindowsDeps) run(name string, h svc.Handler) error {
	d.runName = name
	d.runHandler = h
	return nil
}
func (d *fakeWindowsDeps) connect() (serviceConn, error)        { return d.conn, d.connErr }
func (d *fakeWindowsDeps) installEventSource(name string) error { return d.installErr }
func (d *fakeWindowsDeps) removeEventSource(name string) error  { return d.removeErr }

func TestQuotePath(t *testing.T) {
	if got := quotePath(`C:\foo.exe`); got != `C:\foo.exe` {
		t.Errorf("quotePath = %q; want %q", got, `C:\foo.exe`)
	}
	if got := quotePath(`C:\Program Files\foo.exe`); got != `"C:\Program Files\foo.exe"` {
		t.Errorf("quotePath = %q; want %q", got, `"C:\Program Files\foo.exe"`)
	}
}

func TestWindowsInstallSuccess(t *testing.T) {
	deps := &fakeWindowsDeps{conn: &fakeConn{openErr: windows.ERROR_SERVICE_DOES_NOT_EXIST, createHandle: &fakeServiceHandle{}}}
	m := &windowsManager{deps: deps}
	if err := m.install(Config{Name: "svc", BinaryPath: `C:\bin\x.exe`, DisplayName: "d", Description: "desc"}); err != nil {
		t.Fatalf("install: %v", err)
	}
}

func TestWindowsInstallAlreadyExists(t *testing.T) {
	deps := &fakeWindowsDeps{conn: &fakeConn{openHandle: &fakeServiceHandle{}}}
	m := &windowsManager{deps: deps}
	if err := m.install(Config{Name: "svc"}); err == nil {
		t.Fatal("expected already exists error")
	}
}

func TestWindowsInstallOpenOtherError(t *testing.T) {
	deps := &fakeWindowsDeps{conn: &fakeConn{openErr: errors.New("access denied")}}
	m := &windowsManager{deps: deps}
	if err := m.install(Config{Name: "svc"}); err == nil {
		t.Fatal("expected error")
	}
}

func TestWindowsInstallCreateError(t *testing.T) {
	deps := &fakeWindowsDeps{conn: &fakeConn{openErr: windows.ERROR_SERVICE_DOES_NOT_EXIST, createErr: errors.New("fail")}}
	m := &windowsManager{deps: deps}
	if err := m.install(Config{Name: "svc"}); err == nil {
		t.Fatal("expected create error")
	}
}

func TestWindowsInstallEventLogError(t *testing.T) {
	handle := &fakeServiceHandle{}
	deps := &fakeWindowsDeps{
		conn:       &fakeConn{openErr: windows.ERROR_SERVICE_DOES_NOT_EXIST, createHandle: handle},
		installErr: errors.New("evt"),
	}
	m := &windowsManager{deps: deps}
	if err := m.install(Config{Name: "svc"}); err == nil {
		t.Fatal("expected event log error")
	}
}

func TestWindowsRemoveSuccess(t *testing.T) {
	deps := &fakeWindowsDeps{conn: &fakeConn{openHandle: &fakeServiceHandle{}}}
	m := &windowsManager{deps: deps}
	if err := m.remove(Config{Name: "svc"}); err != nil {
		t.Fatalf("remove: %v", err)
	}
}

func TestWindowsRemoveNotInstalled(t *testing.T) {
	deps := &fakeWindowsDeps{conn: &fakeConn{openErr: windows.ERROR_SERVICE_DOES_NOT_EXIST}}
	m := &windowsManager{deps: deps}
	if err := m.remove(Config{Name: "svc"}); err == nil {
		t.Fatal("expected not installed error")
	}
}

func TestWindowsRemoveDeleteError(t *testing.T) {
	deps := &fakeWindowsDeps{conn: &fakeConn{openHandle: &fakeServiceHandle{deleteErr: errors.New("locked")}}}
	m := &windowsManager{deps: deps}
	if err := m.remove(Config{Name: "svc"}); err == nil {
		t.Fatal("expected delete error")
	}
}

func TestWindowsRemoveEventLogError(t *testing.T) {
	deps := &fakeWindowsDeps{
		conn:      &fakeConn{openHandle: &fakeServiceHandle{}},
		removeErr: errors.New("evt"),
	}
	m := &windowsManager{deps: deps}
	if err := m.remove(Config{Name: "svc"}); err == nil {
		t.Fatal("expected event log remove error")
	}
}

func TestWindowsManagerRunService(t *testing.T) {
	deps := &fakeWindowsDeps{}
	m := &windowsManager{deps: deps}
	factory := func([]string, Logger) (Runner, error) { return &fakeRunner{}, nil }
	if err := m.runService(Config{Name: "svc"}, factory, &consoleLogger{}); err != nil {
		t.Fatalf("runService: %v", err)
	}
	if deps.runName != "svc" || deps.runHandler == nil {
		t.Error("run not delegated to deps")
	}
}

type windowsFakeRunner struct {
	ran    bool
	closed bool
	err    error
}

func (r *windowsFakeRunner) Run(ctx context.Context) error {
	r.ran = true
	if r.err != nil {
		return r.err
	}
	<-ctx.Done()
	return r.err
}
func (r *windowsFakeRunner) Close() error { r.closed = true; return nil }

func TestWindowsExecuteStop(t *testing.T) {
	runner := &windowsFakeRunner{}
	factory := func([]string, Logger) (Runner, error) { return runner, nil }
	ws := &windowsService{cfg: Config{Name: "svc"}, factory: factory, logger: &consoleLogger{}}

	requests := make(chan svc.ChangeRequest, 2)
	changes := make(chan svc.Status, 10)
	done := make(chan struct{})
	var errno uint32
	go func() {
		_, errno = ws.Execute(nil, requests, changes)
		close(done)
	}()

	requests <- svc.ChangeRequest{Cmd: svc.Interrogate, CurrentStatus: svc.Status{State: svc.Running}}
	requests <- svc.ChangeRequest{Cmd: svc.Stop}
	<-done

	if !runner.ran || !runner.closed {
		t.Errorf("ran=%v closed=%v", runner.ran, runner.closed)
	}
	if errno != 0 {
		t.Errorf("errno = %d; want 0", errno)
	}
}

func TestWindowsExecuteRunnerError(t *testing.T) {
	runner := &windowsFakeRunner{err: errors.New("boom")}
	factory := func([]string, Logger) (Runner, error) { return runner, nil }
	ws := &windowsService{cfg: Config{Name: "svc"}, factory: factory, logger: &consoleLogger{}}

	requests := make(chan svc.ChangeRequest)
	changes := make(chan svc.Status, 10)
	done := make(chan struct{})
	var errno uint32
	go func() {
		_, errno = ws.Execute(nil, requests, changes)
		close(done)
	}()
	<-done
	if errno != 2 {
		t.Errorf("errno = %d; want 2", errno)
	}
	if !runner.closed {
		t.Error("runner not closed")
	}
}

func TestWindowsManagerIsServiceRealDeps(t *testing.T) {
	m := &windowsManager{deps: realWindowsDeps{}}
	if _, err := m.isService(); err != nil {
		t.Logf("isService returned error (acceptable on non-SCM hosts): %v", err)
	}
}

func TestWindowsRemoveStopTimeout(t *testing.T) {
	handle := &fakeServiceHandle{
		controlStatus: svc.Status{State: svc.Running},
		queryStatus:   svc.Status{State: svc.Running},
	}
	deps := &fakeWindowsDeps{conn: &fakeConn{openHandle: handle}}
	m := &windowsManager{deps: deps}
	cfg := Config{Name: "svc", StopTimeout: time.Millisecond, StopPollInterval: time.Millisecond}
	if err := m.remove(cfg); err == nil {
		t.Fatal("expected stop timeout error")
	}
}

func TestWindowsRemoveControlErrorButStopped(t *testing.T) {
	handle := &fakeServiceHandle{
		controlErr:  errors.New("cannot accept control"),
		queryStatus: svc.Status{State: svc.Stopped},
	}
	deps := &fakeWindowsDeps{conn: &fakeConn{openHandle: handle}}
	m := &windowsManager{deps: deps}
	if err := m.remove(Config{Name: "svc"}); err != nil {
		t.Fatalf("remove should succeed once service is stopped: %v", err)
	}
}

func TestWindowsExecuteFactoryError(t *testing.T) {
	factory := func([]string, Logger) (Runner, error) { return nil, errors.New("no runner") }
	ws := &windowsService{cfg: Config{Name: "svc"}, factory: factory, logger: &consoleLogger{}}
	requests := make(chan svc.ChangeRequest)
	changes := make(chan svc.Status, 10)
	done := make(chan struct{})
	var errno uint32
	go func() {
		_, errno = ws.Execute(nil, requests, changes)
		close(done)
	}()
	<-done
	if errno != 1 {
		t.Errorf("errno = %d; want 1", errno)
	}
}

type fakeEventLog struct {
	calls []string
}

func (f *fakeEventLog) Error(id uint32, msg string) error {
	f.calls = append(f.calls, "error")
	return nil
}

func (f *fakeEventLog) Info(id uint32, msg string) error {
	f.calls = append(f.calls, "info")
	return nil
}

func (f *fakeEventLog) Warning(id uint32, msg string) error {
	f.calls = append(f.calls, "warning")
	return nil
}
func (f *fakeEventLog) Close() error { f.calls = append(f.calls, "close"); return nil }

func TestWindowsEventLogger(t *testing.T) {
	old := openEventLog
	t.Cleanup(func() { openEventLog = old })
	fe := &fakeEventLog{}
	openEventLog = func(string) (eventLogWriter, error) { return fe, nil }

	l, err := newPlatformLogger("svc", true)
	if err != nil {
		t.Fatalf("newPlatformLogger: %v", err)
	}
	if err := l.Error(1, "e"); err != nil {
		t.Fatalf("Error: %v", err)
	}
	if err := l.Info(2, "i"); err != nil {
		t.Fatalf("Info: %v", err)
	}
	l.Warn("w")
	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if len(fe.calls) != 4 {
		t.Errorf("event log calls = %v", fe.calls)
	}
}

func TestNewLoggerEventLogFallback(t *testing.T) {
	old := openEventLog
	t.Cleanup(func() { openEventLog = old })
	openEventLog = func(string) (eventLogWriter, error) { return nil, errors.New("not registered") }
	l, err := newPlatformLogger("svc", true)
	if err != nil {
		t.Fatalf("newPlatformLogger: %v", err)
	}
	if _, ok := l.(*consoleLogger); !ok {
		t.Errorf("expected consoleLogger fallback, got %T", l)
	}
}

func TestRealWindowsDeps(t *testing.T) {
	oldIs, oldRun, oldConn := svcIsWindowsServiceFn, svcRunFn, mgrConnectFn
	oldInstall, oldRemove := eventLogInstallFn, eventLogRemoveFn
	t.Cleanup(func() {
		svcIsWindowsServiceFn, svcRunFn, mgrConnectFn = oldIs, oldRun, oldConn
		eventLogInstallFn, eventLogRemoveFn = oldInstall, oldRemove
	})

	svcIsWindowsServiceFn = func() (bool, error) { return true, nil }
	svcRunFn = func(string, svc.Handler) error { return nil }
	mgrConnectFn = func() (*mgr.Mgr, error) { return nil, errors.New("denied") }
	eventLogInstallFn = func(string, uint32) error { return nil }
	eventLogRemoveFn = func(string) error { return nil }

	d := realWindowsDeps{}
	if ok, err := d.isService(); err != nil || !ok {
		t.Errorf("isService = %v, %v", ok, err)
	}
	if err := d.run("svc", nil); err != nil {
		t.Errorf("run: %v", err)
	}
	if _, err := d.connect(); err == nil {
		t.Error("connect expected error")
	}
	if err := d.installEventSource("svc"); err != nil {
		t.Errorf("installEventSource: %v", err)
	}
	if err := d.removeEventSource("svc"); err != nil {
		t.Errorf("removeEventSource: %v", err)
	}
}
