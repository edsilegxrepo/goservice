//go:build windows

package goservice

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/eventlog"
	"golang.org/x/sys/windows/svc/mgr"
)

func init() {
	defaultManager = &windowsManager{deps: realWindowsDeps{}}
}

// serviceConn abstracts the Windows service manager connection so that unit
// tests can simulate SCM operations without administrative rights.
type serviceConn interface {
	Disconnect() error
	OpenService(name string) (serviceHandle, error)
	CreateService(name, exepath string, c mgr.Config, args ...string) (serviceHandle, error)
}

// serviceHandle abstracts a Windows service handle.
type serviceHandle interface {
	Close() error
	Delete() error
	Control(c svc.Cmd) (svc.Status, error)
	Query() (svc.Status, error)
}

// windowsDeps abstracts the OS-level operations used by windowsManager.
type windowsDeps interface {
	isService() (bool, error)
	run(name string, h svc.Handler) error
	connect() (serviceConn, error)
	installEventSource(name string) error
	removeEventSource(name string) error
}

type realConn struct{ m *mgr.Mgr }

func (c *realConn) Disconnect() error { return c.m.Disconnect() }

func (c *realConn) OpenService(name string) (serviceHandle, error) {
	s, err := c.m.OpenService(name)
	if err != nil {
		return nil, err
	}
	return &realService{s}, nil
}

func (c *realConn) CreateService(name, exepath string, cfg mgr.Config, args ...string) (serviceHandle, error) {
	s, err := c.m.CreateService(name, exepath, cfg, args...)
	if err != nil {
		return nil, err
	}
	return &realService{s}, nil
}

type realService struct{ s *mgr.Service }

func (s *realService) Close() error                          { return s.s.Close() }
func (s *realService) Delete() error                         { return s.s.Delete() }
func (s *realService) Control(c svc.Cmd) (svc.Status, error) { return s.s.Control(c) }
func (s *realService) Query() (svc.Status, error)            { return s.s.Query() }

var (
	svcIsWindowsServiceFn = svc.IsWindowsService
	svcRunFn              = svc.Run
	mgrConnectFn          = mgr.Connect
	eventLogInstallFn     = eventlog.InstallAsEventCreate
	eventLogRemoveFn      = eventlog.Remove
)

type realWindowsDeps struct{}

func (realWindowsDeps) isService() (bool, error)             { return svcIsWindowsServiceFn() }
func (realWindowsDeps) run(name string, h svc.Handler) error { return svcRunFn(name, h) }
func (realWindowsDeps) connect() (serviceConn, error) {
	m, err := mgrConnectFn()
	if err != nil {
		return nil, err
	}
	return &realConn{m}, nil
}

func (realWindowsDeps) installEventSource(name string) error {
	_ = eventLogRemoveFn(name)
	return eventLogInstallFn(name, eventlog.Error|eventlog.Warning|eventlog.Info)
}
func (realWindowsDeps) removeEventSource(name string) error { return eventLogRemoveFn(name) }

type windowsManager struct{ deps windowsDeps }

func (m *windowsManager) isService() (bool, error) { return m.deps.isService() }

func (m *windowsManager) runService(cfg Config, make RunnerFactory, logger Logger) error {
	return m.deps.run(cfg.Name, &windowsService{cfg: cfg, factory: make, logger: logger})
}

func (m *windowsManager) install(cfg Config) error {
	conn, err := m.deps.connect()
	if err != nil {
		if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			return fmt.Errorf("%w: failed to connect to SCM", ErrPermissionDenied)
		}
		return err
	}
	defer func() { _ = conn.Disconnect() }()

	s, err := conn.OpenService(cfg.Name)
	if err == nil {
		_ = s.Close()
		return fmt.Errorf("%w: %s", ErrAlreadyExists, cfg.Name)
	}
	if !errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST) {
		return fmt.Errorf("failed to query service %s: %w", cfg.Name, err)
	}

	c := mgr.Config{
		ServiceType:  windows.SERVICE_WIN32_OWN_PROCESS,
		StartType:    mgr.StartAutomatic,
		ErrorControl: mgr.ErrorNormal,
		DisplayName:  cfg.DisplayName,
		Description:  cfg.Description,
	}
	if cfg.User != "" {
		c.ServiceStartName = cfg.User
		c.Password = cfg.Pass
	}

	exePath := quotePath(cfg.BinaryPath)
	s, err = conn.CreateService(cfg.Name, exePath, c, cfg.Args...)
	if err != nil {
		if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			return fmt.Errorf("%w: failed to create service", ErrPermissionDenied)
		}
		return err
	}
	defer func() { _ = s.Close() }()

	if err := m.deps.installEventSource(cfg.Name); err != nil {
		_ = s.Delete()
		return fmt.Errorf("failed to register event log source: %w", err)
	}
	return nil
}

func (m *windowsManager) remove(cfg Config) error {
	conn, err := m.deps.connect()
	if err != nil {
		if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			return fmt.Errorf("%w: failed to connect to SCM", ErrPermissionDenied)
		}
		return err
	}
	defer func() { _ = conn.Disconnect() }()

	s, err := conn.OpenService(cfg.Name)
	if err != nil {
		if errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST) {
			return fmt.Errorf("%w: %s", ErrNotInstalled, cfg.Name)
		}
		return fmt.Errorf("failed to open service %s: %w", cfg.Name, err)
	}
	defer func() { _ = s.Close() }()

	status, controlErr := s.Control(svc.Stop)
	timeout := time.Now().Add(stopTimeout(cfg))
	poll := stopPollInterval(cfg)
	for status.State != svc.Stopped && time.Now().Before(timeout) {
		time.Sleep(poll)
		status, err = s.Query()
		if err != nil {
			break
		}
	}
	if status.State != svc.Stopped {
		if controlErr != nil {
			return fmt.Errorf("failed to stop service %s: %w", cfg.Name, controlErr)
		}
		return fmt.Errorf("service %s did not stop within %v", cfg.Name, stopTimeout(cfg))
	}

	if err := s.Delete(); err != nil {
		return err
	}
	if err := m.deps.removeEventSource(cfg.Name); err != nil {
		return fmt.Errorf("failed to remove event log source: %w", err)
	}
	return nil
}

type windowsService struct {
	cfg     Config
	factory RunnerFactory
	logger  Logger
}

func (m *windowsService) Execute(args []string, r <-chan svc.ChangeRequest, changes chan<- svc.Status) (ssec bool, errno uint32) {
	const cmdsAccepted = svc.AcceptStop | svc.AcceptShutdown
	timeout := stopTimeout(m.cfg)
	waitHintDuration := timeout / time.Millisecond
	if waitHintDuration > math.MaxUint32 {
		waitHintDuration = math.MaxUint32
	}
	waitHint := uint32(waitHintDuration)
	checkpoint := uint32(1)

	changes <- svc.Status{State: svc.StartPending, WaitHint: waitHint, CheckPoint: checkpoint}
	checkpoint++

	runner, err := m.factory(args, m.logger)
	if err != nil {
		m.logError(fmt.Sprintf("failed to create runner: %v", err))
		changes <- svc.Status{State: svc.Stopped}
		return false, 1
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errChan := make(chan error, 1)
	go func() {
		errChan <- runner.Run(ctx)
	}()

	changes <- svc.Status{State: svc.Running, Accepts: cmdsAccepted, CheckPoint: checkpoint}
	checkpoint++

	var runnerErr error
	runnerDone := false

loop:
	for {
		select {
		case c := <-r:
			switch c.Cmd {
			case svc.Interrogate:
				changes <- c.CurrentStatus
			case svc.Stop, svc.Shutdown:
				changes <- svc.Status{State: svc.StopPending, WaitHint: waitHint, CheckPoint: checkpoint}
				checkpoint++
				cancel()
			default:
				m.logError(fmt.Sprintf("unexpected control request #%d", c.Cmd))
			}
		case runnerErr = <-errChan:
			if runnerErr != nil && runnerErr != context.Canceled {
				m.logError(fmt.Sprintf("runner stopped with error: %v", runnerErr))
				errno = 2
			}
			runnerDone = true
			break loop
		}
	}

	if !runnerDone {
		select {
		case runnerErr = <-errChan:
			if runnerErr != nil && runnerErr != context.Canceled {
				m.logError(fmt.Sprintf("runner stopped with error: %v", runnerErr))
				errno = 2
			}
		case <-time.After(timeout):
			m.logError("runner did not stop in time; forcing close")
		}
	}

	if err := runner.Close(); err != nil {
		m.logError(fmt.Sprintf("failed to close runner: %v", err))
	}

	changes <- svc.Status{State: svc.Stopped}
	return false, errno
}

func (m *windowsService) logError(msg string) {
	if m.logger != nil {
		_ = m.logger.Error(1, msg)
	}
}

func quotePath(p string) string {
	if p == "" || !strings.ContainsAny(p, " \t") {
		return p
	}
	if strings.HasPrefix(p, `"`) && strings.HasSuffix(p, `"`) {
		return p
	}
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range p {
		if r == '"' {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	b.WriteByte('"')
	return b.String()
}
