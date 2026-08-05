//go:build linux

package goservice

import (
	"bytes"
	"context"
	"fmt"

	// systemd unit files are plain text, not HTML; html/template would escape their content.
	"os"
	"os/signal"
	"os/user"
	"path/filepath"
	"strings"
	"syscall"
	"text/template" // nosemgrep
	"time"
)

func init() {
	defaultManager = managerAdapter{
		isServiceFn:  isService,
		installFn:    install,
		removeFn:     remove,
		runServiceFn: runService,
	}
}

var (
	userLookup = user.Lookup
	readFile   = os.ReadFile
	lstatFile  = os.Lstat
)

const systemdSystemDir = "/etc/systemd/system"

const defaultUnitTemplate = `[Unit]
Description={{.Description}}
After=local-fs.target network-online.target

[Service]
Type=simple
{{if .User}}User={{.User}}
{{end}}{{if .Group}}Group={{.Group}}
{{end}}Environment=HOME={{.Home}}
ExecStart={{.ExecStart}}
Restart=on-failure
RestartSec=30s

[Install]
WantedBy=multi-user.target
`

type unitData struct {
	Description string
	User        string
	Group       string
	Home        string
	ExecStart   string
}

func isService() (bool, error) {
	return false, nil
}

func runService(cfg Config, factory RunnerFactory, logger Logger) error {
	runner, err := factory(cfg.Args, logger)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	timeout := stopTimeout(cfg)
	runErr := make(chan error, 1)
	go func() { runErr <- runner.Run(ctx) }()

	select {
	case err = <-runErr:
	case <-ctx.Done():
		select {
		case err = <-runErr:
		case <-time.After(timeout):
			err = fmt.Errorf("runner did not stop within %v", timeout)
		}
	}

	stop()
	if closeErr := runner.Close(); err == nil {
		err = closeErr
	}
	return err
}

func install(cfg Config) error {
	if cfg.Instance != "" {
		return installTemplated(cfg)
	}
	return installSimple(cfg)
}

func installTemplated(cfg Config) error {
	full := fmt.Sprintf("%s@%s", cfg.Name, cfg.Instance)
	path := filepath.Join(systemdSystemDir, cfg.Name+"@.service")
	return installUnit(cfg, full, path)
}

func installSimple(cfg Config) error {
	path := filepath.Join(systemdSystemDir, cfg.Name+".service")
	return installUnit(cfg, cfg.Name, path)
}

func installUnit(cfg Config, unit, path string) error {
	data, err := buildUnitData(cfg)
	if err != nil {
		return err
	}
	content, err := renderUnit(cfg, data)
	if err != nil {
		return err
	}
	if err := writePrivilegedFile(path, content, 0o644); err != nil {
		return fmt.Errorf("failed to write unit file %s: %w", path, err)
	}
	if err := runPrivilegedCommand("systemctl", "daemon-reload"); err != nil {
		return err
	}
	if err := runPrivilegedCommand("systemctl", "enable", unit); err != nil {
		return fmt.Errorf("failed to enable service %s: %w", unit, err)
	}
	return nil
}

func renderUnit(cfg Config, data unitData) ([]byte, error) {
	tmplText := defaultUnitTemplate
	if cfg.TemplatePath != "" {
		clean := filepath.Clean(cfg.TemplatePath)
		info, err := lstatFile(clean)
		if err != nil {
			return nil, fmt.Errorf("failed to stat template: %w", err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("template %q is a symlink", clean)
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("template %q is not a regular file", clean)
		}
		b, err := readFile(clean)
		if err != nil {
			return nil, fmt.Errorf("failed to read template: %w", err)
		}
		tmplText = string(b)
	}
	t, err := template.New("unit").Parse(tmplText)
	if err != nil {
		return nil, fmt.Errorf("failed to parse unit template: %w", err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("failed to render unit file: %w", err)
	}
	return buf.Bytes(), nil
}

func buildUnitData(cfg Config) (unitData, error) {
	ug := cfg.UserGroup
	if ug == "" && cfg.User != "" {
		ug = cfg.User
	}
	parts := strings.SplitN(ug, ":", 2)
	userName := strings.TrimSpace(parts[0])
	groupName := ""
	if len(parts) > 1 {
		groupName = strings.TrimSpace(parts[1])
	}

	if userName != "" && !namePattern.MatchString(userName) {
		return unitData{}, fmt.Errorf("invalid user name %q", userName)
	}
	if groupName != "" && !namePattern.MatchString(groupName) {
		return unitData{}, fmt.Errorf("invalid group name %q", groupName)
	}

	home := "/root"
	if userName != "" {
		u, err := userLookup(userName)
		if err != nil {
			return unitData{}, fmt.Errorf("failed to look up user %q: %w", userName, err)
		}
		home = u.HomeDir
	}

	execStart := quoteSystemdArgs(cfg.BinaryPath, cfg.Args)
	return unitData{
		Description: systemdEscape(cfg.Description),
		User:        userName,
		Group:       groupName,
		Home:        systemdEscape(home),
		ExecStart:   execStart,
	}, nil
}

func remove(cfg Config) error {
	if cfg.Instance != "" {
		return removeTemplated(cfg)
	}
	return removeSimple(cfg)
}

func removeTemplated(cfg Config) error {
	full := fmt.Sprintf("%s@%s", cfg.Name, cfg.Instance)
	path := filepath.Join(systemdSystemDir, cfg.Name+"@.service")
	return removeUnit(full, path)
}

func removeSimple(cfg Config) error {
	path := filepath.Join(systemdSystemDir, cfg.Name+".service")
	return removeUnit(cfg.Name, path)
}

func removeUnit(unit, path string) error {
	_ = runPrivilegedCommand("systemctl", "stop", unit)
	if err := runPrivilegedCommand("systemctl", "disable", unit); err != nil {
		return fmt.Errorf("failed to disable service %s: %w", unit, err)
	}
	if err := runPrivilegedCommand("systemctl", "daemon-reload"); err != nil {
		return fmt.Errorf("failed to reload systemd: %w", err)
	}
	if err := removePrivilegedFile(path); err != nil {
		return fmt.Errorf("failed to remove unit %s: %w", path, err)
	}
	return nil
}

// systemdEscape escapes a value written into a systemd unit file so that
// newlines cannot break the line structure and percent specifiers cannot
// be injected.
func systemdEscape(s string) string {
	s = strings.ReplaceAll(s, "\r\n", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "%", "%%")
	return s
}

// quoteSystemdArgs builds an ExecStart value by quoting each argument.
// It prevents whitespace and shell-like metacharacters from changing the
// command structure.
func quoteSystemdArgs(path string, args []string) string {
	quoted := make([]string, 1, len(args)+1)
	quoted[0] = systemdArg(path)
	for _, a := range args {
		quoted = append(quoted, systemdArg(a))
	}
	return strings.Join(quoted, " ")
}

func systemdArg(arg string) string {
	if arg == "" {
		return `""`
	}
	if !strings.ContainsAny(arg, " \t\n\r\"\\%'") {
		return arg
	}
	out := strings.Builder{}
	out.WriteByte('"')
	for _, r := range arg {
		switch r {
		case '"', '\\':
			out.WriteByte('\\')
			out.WriteRune(r)
		case '%':
			out.WriteString("%%")
		case '\n', '\r', '\t':
			out.WriteByte(' ')
		default:
			out.WriteRune(r)
		}
	}
	out.WriteByte('"')
	return out.String()
}
