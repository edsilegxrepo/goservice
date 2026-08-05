//go:build windows

package goservice

import (
	"golang.org/x/sys/windows/svc/eventlog"
)

// eventLogWriter abstracts the Windows Event Log so tests can inject a fake.
type eventLogWriter interface {
	Error(id uint32, msg string) error
	Info(id uint32, msg string) error
	Warning(id uint32, msg string) error
	Close() error
}

// windowsLogger writes application events to the Windows Event Log.
type windowsLogger struct {
	elog eventLogWriter
}

var openEventLog = func(name string) (eventLogWriter, error) {
	return eventlog.Open(name)
}

func newPlatformLogger(name string, isService bool) (Logger, error) {
	if !isService {
		return &consoleLogger{}, nil
	}
	elog, err := openEventLog(name)
	if err != nil {
		// Event log source is not registered; fall back to console logging
		// so that service start-up diagnostics are still visible.
		return &consoleLogger{}, nil
	}
	return &windowsLogger{elog: elog}, nil
}

func (l *windowsLogger) Error(id uint32, msg string) error {
	return l.elog.Error(id, msg)
}

func (l *windowsLogger) Info(id uint32, msg string) error {
	return l.elog.Info(id, msg)
}

func (l *windowsLogger) Warn(msg string) {
	_ = l.elog.Warning(2, msg)
}

func (l *windowsLogger) Close() error {
	return l.elog.Close()
}
