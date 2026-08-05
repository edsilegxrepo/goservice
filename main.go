package goservice

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Logger is the platform-agnostic logging interface used by the service
// runtime and exposed to Runner implementations.
type Logger interface {
	Error(id uint32, msg string) error
	Info(id uint32, msg string) error
	Warn(msg string)
	Close() error
}

// consoleLogger writes log messages to the standard logger.
// It is used as a fallback when the process is not running as a service.
type consoleLogger struct{}

func (c *consoleLogger) Error(id uint32, msg string) error {
	log.Printf("ERROR [%d]: %s", id, msg)
	return nil
}

func (c *consoleLogger) Info(id uint32, msg string) error {
	log.Printf("INFO [%d]: %s", id, msg)
	return nil
}

func (c *consoleLogger) Warn(msg string) {
	log.Printf("WARN: %s", msg)
}

func (c *consoleLogger) Close() error {
	return nil
}

// Runner is the interface implemented by the application logic that will
// execute inside the service context.
type Runner interface {
	Run(ctx context.Context) error
	Close() error
}

// RunnerFactory constructs a Runner for the supplied service arguments and
// logger. It is called by Run once the service manager starts the process.
type RunnerFactory func(args []string, logger Logger) (Runner, error)

// Config describes a service to install, remove, or run.
type Config struct {
	Name             string
	DisplayName      string
	Description      string
	BinaryPath       string
	Args             []string
	User             string
	Pass             string
	Instance         string
	UserGroup        string
	TemplatePath     string
	StopTimeout      time.Duration
	StopPollInterval time.Duration
}

var namePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)

// ErrInvalidConfig is returned when a Config value fails validation.
var ErrInvalidConfig = errors.New("invalid configuration")

// Sentinel errors allow the CLI to map failures to granular exit codes.
var (
	ErrAlreadyExists    = errors.New("service already exists")
	ErrNotInstalled     = errors.New("service is not installed")
	ErrPermissionDenied = errors.New("permission denied")
)

func validateName(name string, maxLen int) error {
	if name == "" {
		return fmt.Errorf("%w: service name is required", ErrInvalidConfig)
	}
	if len(name) > maxLen {
		return fmt.Errorf("%w: service name %q is too long (max %d characters)", ErrInvalidConfig, name, maxLen)
	}
	if !namePattern.MatchString(name) {
		return fmt.Errorf("%w: service name %q contains invalid characters", ErrInvalidConfig, name)
	}
	return nil
}

func validateInstance(instance string) error {
	if instance == "" {
		return nil
	}
	if len(instance) > 256 {
		return fmt.Errorf("%w: service instance %q is too long (max 256 characters)", ErrInvalidConfig, instance)
	}
	if !namePattern.MatchString(instance) {
		return fmt.Errorf("%w: service instance %q contains invalid characters", ErrInvalidConfig, instance)
	}
	return nil
}

func validateBinaryPath(path string) error {
	if path == "" {
		return fmt.Errorf("%w: binary path is required", ErrInvalidConfig)
	}
	if strings.ContainsAny(path, "\"\r\n\t") {
		return fmt.Errorf("%w: binary path %q contains invalid characters", ErrInvalidConfig, path)
	}
	if !filepath.IsAbs(path) {
		return fmt.Errorf("%w: binary path %q must be absolute", ErrInvalidConfig, path)
	}
	fi, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("%w: binary path %q is not accessible: %v", ErrInvalidConfig, path, err)
	}
	if fi.IsDir() {
		return fmt.Errorf("%w: binary path %q is a directory", ErrInvalidConfig, path)
	}
	return nil
}

// NewLogger returns a platform-appropriate logger. On Windows this opens the
// Event Log source when isService is true; on Linux it writes to standard log.
func NewLogger(name string, isService bool) (Logger, error) {
	return newPlatformLogger(name, isService)
}

func stopTimeout(cfg Config) time.Duration {
	if cfg.StopTimeout <= 0 {
		return 30 * time.Second
	}
	return cfg.StopTimeout
}

func stopPollInterval(cfg Config) time.Duration {
	if cfg.StopPollInterval <= 0 {
		return 300 * time.Millisecond
	}
	return cfg.StopPollInterval
}
