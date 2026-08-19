// Command tnl is a daemon-based SSH tunnel manager.
//
// It reads a YAML config (default ~/.tnlrc.yaml), spawns the system ssh binary
// for each port mapping, and supervises the mappings: restarting dead ones
// with exponential backoff, reporting port collisions, and supporting
// per-tunnel lifecycle control through a Unix-socket IPC daemon.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/ahmadaidin/tnl/internal/cli"
	"github.com/ahmadaidin/tnl/internal/config"
	"github.com/ahmadaidin/tnl/internal/daemon"
	"github.com/ahmadaidin/tnl/internal/launchd"
	"github.com/ahmadaidin/tnl/internal/output"
	"github.com/ahmadaidin/tnl/internal/sshsetup"
	"github.com/ahmadaidin/tnl/internal/supervisor"
	"github.com/ahmadaidin/tnl/internal/version"
)

// defaultConfigPath is used when no -c/--config flag is given.
const defaultConfigPath = "~/.tnlrc.yaml"

// socketPollInterval is how often the stop command polls for daemon exit.
const socketPollInterval = 50 * time.Millisecond

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	opts, err := cli.Parse(os.Args[1:])
	if err != nil {
		if errors.Is(err, cli.ErrHelp) {
			fmt.Print(cli.Usage)
			return nil
		}
		return err
	}
	cfgPath := expandPath(opts.ConfigPath)
	if cfgPath == "" {
		cfgPath = expandPath(defaultConfigPath)
	}

	switch opts.Command {
	case cli.CommandStart:
		if opts.InternalDaemon {
			return runInternalDaemon(opts, cfgPath)
		}
		if opts.Detach {
			return launchDaemon(opts)
		}
		return runForeground(opts, cfgPath)
	case cli.CommandStatus:
		return runStatus(opts.Watch)
	case cli.CommandStartTunnel:
		return runStartTunnel(opts, cfgPath)
	case cli.CommandStopDaemon:
		return runStopDaemon()
	case cli.CommandStopTunnel:
		return runStopTunnel(opts)
	case cli.CommandRestartTunnel:
		return runRestartTunnel(opts)
	case cli.CommandSetup:
		return runSetup(opts, cfgPath)
	case cli.CommandInstall:
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		return launchd.Install(exe)
	case cli.CommandUninstall:
		return launchd.Uninstall()
	case cli.CommandVersion:
		fmt.Println(version.String())
		return nil
	default:
		return fmt.Errorf("unhandled command %d", opts.Command)
	}
}

// expandPath expands environment variables and a leading "~" in p.
func expandPath(p string) string {
	p = os.ExpandEnv(p)
	if p == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
		return p
	}
	if strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[2:])
		}
	}
	return p
}

// runForeground runs the supervisor in the foreground until SIGINT/SIGTERM.
func runForeground(opts *cli.Options, cfgPath string) error {
	paths, err := daemon.ResolvePaths()
	if err != nil {
		return err
	}
	if pid, running, err := daemon.CheckRunning(paths); err != nil {
		return err
	} else if running {
		return fmt.Errorf("tnl daemon already running (pid %d); use 'tnl status' or stop it", pid)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	m := supervisor.NewManager(cfg, supervisor.Options{
		Selected: opts.Names,
		Log:      log.New(os.Stderr, "tnl: ", log.LstdFlags),
	})

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	done := make(chan error, 1)
	go func() { done <- m.Run(ctx) }()

	select {
	case err := <-done:
		return err
	case <-ctx.Done():
	}
	fmt.Println("Shutting down tunnels...")
	return <-done
}

// statusQuery retrieves one daemon status response.
type statusQuery func(context.Context) (*daemon.Response, error)

// renderStatus queries and renders one-shot or periodically refreshed status.
func renderStatus(ctx context.Context, watch bool, w io.Writer, query statusQuery, ticks <-chan time.Time) error {
	render := func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		resp, err := query(ctx)
		if err != nil {
			return err
		}
		if resp.Error != "" {
			return errors.New(resp.Error)
		}
		if watch {
			if _, err := io.WriteString(w, "\x1b[H\x1b[2J"); err != nil {
				return err
			}
		}
		if resp.Message != "" {
			_, err = fmt.Fprintln(w, resp.Message)
			return err
		}
		output.Render(resp.Tunnels, w)
		return nil
	}

	if !watch {
		return render()
	}
	if err := render(); err != nil {
		if errors.Is(err, context.Canceled) && ctx.Err() != nil {
			return nil
		}
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticks:
			if err := render(); err != nil {
				if errors.Is(err, context.Canceled) && ctx.Err() != nil {
					return nil
				}
				return err
			}
		}
	}
}

// runStatus queries the daemon and renders the snapshot.
func runStatus(watch bool) error {
	paths, err := daemon.ResolvePaths()
	if err != nil {
		return err
	}
	query := func(ctx context.Context) (*daemon.Response, error) {
		resp, err := daemon.QueryStatus(ctx, paths)
		if err != nil {
			return nil, errors.New("tnl daemon is not running")
		}
		return resp, nil
	}
	if !watch {
		return renderStatus(context.Background(), false, os.Stdout, query, nil)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	return renderStatus(ctx, true, os.Stdout, query, ticker.C)
}

// sendCommand forwards a command to the daemon, mapping a missing-socket dial
// error to "tnl daemon is not running". The daemon can exit between a
// CheckRunning pass and the dial (mid-shutdown window), in which case the raw
// dial error ("no such file or directory") would be misleading.
func sendCommand(ctx context.Context, paths daemon.Paths, command, tunnel string) (*daemon.Response, error) {
	resp, err := daemon.SendCommand(ctx, paths, command, tunnel)
	if err != nil && errors.Is(err, os.ErrNotExist) {
		return nil, errors.New("tnl daemon is not running")
	}
	return resp, err
}

// runStartTunnel starts tunnels through the daemon, auto-daemonizing first if
// no daemon is running. With no names, all enabled tunnels are started.
func runStartTunnel(opts *cli.Options, cfgPath string) error {
	paths, err := daemon.ResolvePaths()
	if err != nil {
		return err
	}
	if _, running, err := daemon.CheckRunning(paths); err != nil {
		return err
	} else if !running {
		if err := launchDaemon(opts); err != nil {
			return err
		}
	}

	names := opts.Names
	if len(names) == 0 {
		cfg, err := config.Load(cfgPath)
		if err != nil {
			return err
		}
		for _, n := range cfg.SortedNames() {
			if cfg.Enabled(n) {
				names = append(names, n)
			}
		}
	}
	if len(names) == 0 {
		fmt.Println("tnl no tunnels to start")
		return nil
	}
	for _, n := range names {
		resp, err := sendCommand(context.Background(), paths, "start", n)
		if err != nil {
			return err
		}
		if resp.Error != "" {
			return errors.New(resp.Error)
		}
		msg := resp.Message
		if msg == "" {
			msg = "started"
		}
		fmt.Printf("tnl tunnel %s %s\n", n, msg)
	}
	return nil
}

// runStopDaemon shuts the daemon down and waits for it to exit.
func runStopDaemon() error {
	paths, err := daemon.ResolvePaths()
	if err != nil {
		return err
	}
	if _, running, err := daemon.CheckRunning(paths); err != nil {
		return err
	} else if !running {
		return errors.New("tnl daemon is not running")
	}
	resp, err := sendCommand(context.Background(), paths, "shutdown", "")
	if err != nil {
		return err
	}
	if resp.Error != "" {
		return errors.New(resp.Error)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(paths.SocketFile); errors.Is(err, os.ErrNotExist) {
			fmt.Println("tnl daemon stopped")
			return nil
		}
		time.Sleep(socketPollInterval)
	}
	return errors.New("tnl daemon did not stop in time")
}

// runStopTunnel stops one or more tunnels through the daemon.
func runStopTunnel(opts *cli.Options) error {
	paths, err := daemon.ResolvePaths()
	if err != nil {
		return err
	}
	if _, running, err := daemon.CheckRunning(paths); err != nil {
		return err
	} else if !running {
		return errors.New("tnl daemon is not running")
	}
	for _, n := range opts.Names {
		resp, err := sendCommand(context.Background(), paths, "stop", n)
		if err != nil {
			return err
		}
		if resp.Error != "" {
			return errors.New(resp.Error)
		}
		msg := resp.Message
		if msg == "" {
			msg = "stopped"
		}
		fmt.Printf("tnl tunnel %s %s\n", n, msg)
	}
	return nil
}

// runRestartTunnel restarts one tunnel through the daemon.
func runRestartTunnel(opts *cli.Options) error {
	paths, err := daemon.ResolvePaths()
	if err != nil {
		return err
	}
	if _, running, err := daemon.CheckRunning(paths); err != nil {
		return err
	} else if !running {
		return errors.New("tnl daemon is not running")
	}
	n := opts.Names[0]
	resp, err := sendCommand(context.Background(), paths, "restart", n)
	if err != nil {
		return err
	}
	if resp.Error != "" {
		return errors.New(resp.Error)
	}
	msg := resp.Message
	if msg == "" {
		msg = "restarted"
	}
	fmt.Printf("tnl tunnel %s %s\n", n, msg)
	return nil
}

// runSetup provisions ssh identities for tunnels. With no names, all tunnels
// in the config are provisioned, enabled or not.
func runSetup(opts *cli.Options, cfgPath string) error {
	paths, err := daemon.ResolvePaths()
	if err != nil {
		return err
	}
	if _, running, err := daemon.CheckRunning(paths); err != nil {
		return err
	} else if running {
		return errors.New("tnl daemon is running; run 'tnl stop' before 'tnl setup'")
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	var names []string
	switch {
	case len(opts.Names) == 1:
		name := opts.Names[0]
		if cfg.Tunnels[name] == nil {
			return fmt.Errorf("unknown tunnel %q", name)
		}
		names = []string{name}
	case len(opts.Names) == 0:
		names = cfg.SortedNames()
	default:
		return errors.New("tnl setup accepts at most one tunnel name")
	}
	provisioner := &sshsetup.Provisioner{}
	setupOpts := sshsetup.Options{
		Algorithm:      opts.SetupAlgorithm,
		Filename:       opts.SetupFilename,
		PassphraseFile: opts.SetupPassphraseFile,
		Yes:            opts.Yes,
	}
	failed := false
	for _, name := range names {
		r := provisioner.Provision(context.Background(), cfg.Tunnels[name], setupOpts)
		switch {
		case r.Err != nil:
			fmt.Fprintf(os.Stderr, "tnl %s: %v\n", name, r.Err)
			failed = true
		case r.Skipped:
			if r.User == "" {
				fmt.Printf("tnl %s already provisioned\n", r.Host)
			} else {
				fmt.Printf("tnl %s@%s already provisioned\n", r.User, r.Host)
			}
		default:
			if r.User == "" {
				fmt.Printf("tnl %s provisioned (key %s)\n", r.Host, r.KeyPath)
			} else {
				fmt.Printf("tnl %s@%s provisioned (key %s)\n", r.User, r.Host, r.KeyPath)
			}
		}
	}
	if failed {
		return errors.New("tnl setup failed for one or more tunnels")
	}
	return nil
}

// tailLogMessage returns the last non-empty line of the daemon log, or an
// error if the log cannot be read. Used to surface the daemon's real startup
// failure when it dies before exposing its socket.
func tailLogMessage(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if line := strings.TrimSpace(lines[i]); line != "" {
			return line, nil
		}
	}
	return "", errors.New("empty log")
}

// launchDaemon re-executes the current binary in daemon mode and waits until
// it is serving. An explicitly passed -c path travels to the child through
// opts.ConfigPath; the default config path is re-resolved by the child.
func launchDaemon(opts *cli.Options) error {
	paths, err := daemon.ResolvePaths()
	if err != nil {
		return err
	}
	if pid, running, err := daemon.CheckRunning(paths); err != nil {
		return err
	} else if running {
		return fmt.Errorf("tnl daemon already running (pid %d)", pid)
	}

	exe, err := os.Executable()
	if err != nil {
		return err
	}
	args := []string{"--internal-daemon"}
	args = append(args, opts.Names...)
	if opts.ConfigPath != "" {
		args = append(args, "-c", opts.ConfigPath)
	}

	logf, err := os.OpenFile(paths.LogFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = logf.Close() }()

	cmd := exec.Command(exe, args...)
	cmd.Stdout = logf
	cmd.Stderr = logf
	if err := cmd.Start(); err != nil {
		return err
	}
	pid := cmd.Process.Pid

	killAndCleanup := func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		daemon.Cleanup(paths)
	}
	if err := daemon.WritePID(paths, pid); err != nil {
		killAndCleanup()
		return err
	}
	if err := daemon.WaitForSocket(paths, 3*time.Second); err != nil {
		killAndCleanup()
		if msg, logErr := tailLogMessage(paths.LogFile); logErr == nil && msg != "" {
			return fmt.Errorf("tnl daemon failed to start: %s", msg)
		}
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	resp, err := daemon.QueryStatus(ctx, paths)
	if err != nil || !resp.Running {
		killAndCleanup()
		if err != nil {
			return err
		}
		return errors.New("tnl daemon failed to start")
	}
	_ = cmd.Process.Release()
	fmt.Printf("tnl daemon started (pid %d)\n", pid)
	return nil
}

// runInternalDaemon is the daemon process: it supervises tunnels and serves
// the Unix socket until shutdown or a signal.
func runInternalDaemon(opts *cli.Options, cfgPath string) error {
	paths, err := daemon.ResolvePaths()
	if err != nil {
		return err
	}
	defer daemon.Cleanup(paths)

	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	logf, err := os.OpenFile(paths.LogFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	logger := log.New(logf, "", log.LstdFlags)
	defer func() { _ = logf.Close() }()

	m := supervisor.NewManager(cfg, supervisor.Options{
		Selected: opts.Names,
		Log:      logger,
	})
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	srv := daemon.NewServer(paths, m, os.Getpid(), stop)
	go daemon.SelfHealPID(ctx, paths, os.Getpid())

	var wg sync.WaitGroup
	done := make(chan error, 2)
	wg.Add(2)
	go func() { defer wg.Done(); done <- m.Run(ctx) }()
	go func() { defer wg.Done(); done <- srv.Run(ctx) }()

	select {
	case <-ctx.Done():
	case err := <-done:
		if err != nil && !errors.Is(err, context.Canceled) {
			logger.Printf("daemon exiting after error: %v", err)
		}
		stop()
	}
	// Both components must finish before we exit. The manager kills its ssh
	// children (SIGINT, 2s grace, SIGKILL) during graceful stop; returning
	// early would orphan them. The kill grace bounds this wait.
	wg.Wait()
	return nil
}
