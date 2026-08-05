package goservice

import (
	"fmt"
	"os"
)

// manager is the platform-specific backend used by the public API. It is
// implemented in the platform files (linux.go, windows.go) and is the single
// extension point that tests can override to avoid making real OS calls.
type manager interface {
	isService() (bool, error)
	install(cfg Config) error
	remove(cfg Config) error
	runService(cfg Config, make RunnerFactory, logger Logger) error
}

// managerAdapter adapts the existing platform functions to the manager
// interface so that each platform can set defaultManager in its init func.
type managerAdapter struct {
	isServiceFn  func() (bool, error)
	installFn    func(Config) error
	removeFn     func(Config) error
	runServiceFn func(Config, RunnerFactory, Logger) error
}

func (a managerAdapter) isService() (bool, error) { return a.isServiceFn() }
func (a managerAdapter) install(cfg Config) error { return a.installFn(cfg) }
func (a managerAdapter) remove(cfg Config) error  { return a.removeFn(cfg) }
func (a managerAdapter) runService(cfg Config, make RunnerFactory, logger Logger) error {
	return a.runServiceFn(cfg, make, logger)
}

// defaultManager is set by the platform-specific init functions. Tests can
// assign a fake manager to this variable to exercise the public API without
// touching the host service manager.
var defaultManager manager

// Install registers the service with the operating system's service manager.
func Install(cfg Config) error {
	if err := validateName(cfg.Name, 256); err != nil {
		return err
	}
	if err := validateInstance(cfg.Instance); err != nil {
		return err
	}
	if cfg.BinaryPath == "" {
		var err error
		cfg.BinaryPath, err = os.Executable()
		if err != nil {
			return fmt.Errorf("failed to resolve executable path: %w", err)
		}
	}
	if err := validateBinaryPath(cfg.BinaryPath); err != nil {
		return err
	}
	if cfg.DisplayName == "" {
		cfg.DisplayName = cfg.Name
	}
	if cfg.Description == "" {
		cfg.Description = cfg.Name + " service"
	}
	return defaultManager.install(cfg)
}

// Remove deregisters the service from the operating system's service manager.
func Remove(cfg Config) error {
	if err := validateName(cfg.Name, 256); err != nil {
		return err
	}
	if err := validateInstance(cfg.Instance); err != nil {
		return err
	}
	return defaultManager.remove(cfg)
}

// Run starts the service execution loop. It should be called when IsService()
// reports true. The supplied factory is invoked to create the Runner once the
// service manager signals a start.
func Run(cfg Config, make RunnerFactory) error {
	if err := validateName(cfg.Name, 256); err != nil {
		return err
	}
	if make == nil {
		return fmt.Errorf("%w: runner factory is required", ErrInvalidConfig)
	}
	logger, err := NewLogger(cfg.Name, true)
	if err != nil {
		return err
	}
	defer func() { _ = logger.Close() }()
	return defaultManager.runService(cfg, make, logger)
}

// IsService reports whether the current process is running under a service
// manager (SCM on Windows, always false on Linux).
func IsService() bool {
	s, err := IsServiceErr()
	if err != nil {
		return false
	}
	return s
}

// IsServiceErr is like IsService but also returns the detection error, if any.
func IsServiceErr() (bool, error) {
	return defaultManager.isService()
}
