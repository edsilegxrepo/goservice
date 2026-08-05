//go:build linux

package goservice

func newPlatformLogger(name string, isService bool) (Logger, error) {
	_ = name
	_ = isService
	return &consoleLogger{}, nil
}
