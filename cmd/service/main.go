package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/edsilegxrepo/goservice"
)

const (
	exitOK            = 0
	exitRuntime       = 1
	exitUsage         = 2
	exitValidation    = 3
	exitPermission    = 4
	exitAlreadyExists = 5
	exitNotInstalled  = 6
)

func main() {
	os.Exit(run(os.Args[1:]))
}

type cliOptions struct {
	install bool
	remove  bool
	run     bool
}

var (
	isServiceFn = goservice.IsService
	installFn   = goservice.Install
	removeFn    = goservice.Remove
	runFn       = goservice.Run
)

func run(args []string) int {
	cfg, opts, code := parseConfig(args)
	if code != exitOK {
		return code
	}
	defer scrubSecrets(&cfg)

	if cfg.Pass == "" {
		cfg.Pass = os.Getenv("SERVICE_PASS")
	}

	if isServiceFn() && !opts.install && !opts.remove && !opts.run {
		opts.run = true
	}

	if code := validateMode(cfg, opts); code != exitOK {
		return code
	}

	if err := execute(cfg, opts); err != nil {
		if errors.Is(err, context.Canceled) {
			return exitOK
		}
		fmt.Fprintln(os.Stderr, err)
		return exitCodeForError(err)
	}

	if opts.install || opts.remove {
		action := "removed"
		if opts.install {
			action = "installed"
		}
		fmt.Printf("service %s %s successfully\n", cfg.Name, action)
	}
	return exitOK
}

func parseConfig(args []string) (goservice.Config, cliOptions, int) {
	var cfg goservice.Config
	var opts cliOptions
	var argString, display, desc string

	fs := flag.NewFlagSet("service", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.StringVar(&cfg.Name, "name", "", "service name (required)")
	fs.StringVar(&cfg.BinaryPath, "binary", "", "path to binary (required for install/run)")
	fs.StringVar(&argString, "args", "", "binary arguments as a single quoted string")
	fs.StringVar(&display, "display", "", "service display name")
	fs.StringVar(&desc, "description", "", "service description")
	fs.StringVar(&cfg.User, "user", "", "service user (Windows) or user:group (Linux)")
	fs.StringVar(&cfg.Pass, "pass", "", "Windows service user password (deprecated; use SERVICE_PASS)")
	fs.StringVar(&cfg.Instance, "instance", "", "Linux systemd instance (e.g. default)")
	fs.StringVar(&cfg.TemplatePath, "template", "", "Linux systemd unit template path")
	fs.BoolVar(&opts.install, "install", false, "install the service")
	fs.BoolVar(&opts.remove, "remove", false, "remove the service")
	fs.BoolVar(&opts.run, "run", false, "run the service (auto-detected when started by SCM)")
	if err := fs.Parse(args); err != nil {
		return cfg, opts, exitUsage
	}
	if cfg.Pass != "" {
		fmt.Fprintln(os.Stderr, "warning: -pass is deprecated and may leak the password; use SERVICE_PASS instead")
	}

	cfg.Args = splitArgs(argString)
	if display != "" {
		cfg.DisplayName = display
	}
	if desc != "" {
		cfg.Description = desc
	}
	if runtime.GOOS != "windows" && cfg.User != "" {
		cfg.UserGroup = cfg.User
		cfg.User = ""
	}
	return cfg, opts, exitOK
}

func validateMode(cfg goservice.Config, opts cliOptions) int {
	if cfg.Name == "" {
		fmt.Fprintln(os.Stderr, "-name is required")
		return exitUsage
	}
	selected := 0
	if opts.install {
		selected++
	}
	if opts.remove {
		selected++
	}
	if opts.run {
		selected++
	}
	if selected != 1 {
		fmt.Fprintln(os.Stderr, "exactly one of -install, -remove or -run is required")
		return exitUsage
	}
	if (opts.install || opts.run) && cfg.BinaryPath == "" {
		fmt.Fprintln(os.Stderr, "-binary is required for install/run")
		return exitUsage
	}
	if runtime.GOOS == "windows" && (opts.install || opts.run) && cfg.User != "" && cfg.Pass == "" {
		fmt.Fprintln(os.Stderr, "SERVICE_PASS is required when -user is set on Windows")
		return exitValidation
	}
	return exitOK
}

func execute(cfg goservice.Config, opts cliOptions) error {
	switch {
	case opts.install:
		return installFn(cfg)
	case opts.remove:
		return removeFn(cfg)
	case opts.run:
		return runFn(cfg, makeRunnerFactory(cfg))
	default:
		return nil
	}
}

func exitCodeForError(err error) int {
	switch {
	case errors.Is(err, goservice.ErrInvalidConfig):
		return exitValidation
	case errors.Is(err, goservice.ErrAlreadyExists):
		return exitAlreadyExists
	case errors.Is(err, goservice.ErrNotInstalled):
		return exitNotInstalled
	case errors.Is(err, goservice.ErrPermissionDenied):
		return exitPermission
	default:
		return exitRuntime
	}
}

func scrubSecrets(cfg *goservice.Config) {
	cfg.Pass = ""
	_ = os.Unsetenv("SERVICE_PASS")
}

type commandRunner struct {
	path   string
	args   []string
	cancel context.CancelFunc
	exec   func(context.Context, string, ...string) *exec.Cmd
}

func (r *commandRunner) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	r.cancel = cancel
	defer cancel()
	execFn := r.exec
	if execFn == nil {
		execFn = exec.CommandContext
	}
	cmd := execFn(ctx, r.path, r.args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func (r *commandRunner) Close() error {
	if r.cancel != nil {
		r.cancel()
	}
	return nil
}

func makeRunnerFactory(cfg goservice.Config) goservice.RunnerFactory {
	return func(_ []string, _ goservice.Logger) (goservice.Runner, error) {
		return &commandRunner{path: cfg.BinaryPath, args: cfg.Args, exec: exec.CommandContext}, nil
	}
}

// splitArgs splits a string respecting single/double quotes and backslash
// escapes so that argument values can contain spaces when they are quoted.
func splitArgs(s string) []string {
	var res []string
	var cur strings.Builder
	var quote byte
	var escaped bool
	for i := 0; i < len(s); i++ {
		c := s[i]
		if escaped {
			switch c {
			case 'n':
				cur.WriteByte('\n')
			case 't':
				cur.WriteByte('\t')
			default:
				cur.WriteByte(c)
			}
			escaped = false
			continue
		}
		if c == '\\' {
			escaped = true
			continue
		}
		if quote != 0 {
			if c == quote {
				quote = 0
			} else {
				cur.WriteByte(c)
			}
			continue
		}
		if c == '"' || c == '\'' {
			quote = c
			continue
		}
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' {
			if cur.Len() > 0 {
				res = append(res, cur.String())
				cur.Reset()
			}
			continue
		}
		cur.WriteByte(c)
	}
	if cur.Len() > 0 {
		res = append(res, cur.String())
	}
	return res
}
