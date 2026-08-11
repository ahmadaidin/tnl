// Package cli parses the tnl command line.
//
// The parser is deliberately tunn-style: the first positional argument that
// names a command selects the verb; any other positional arguments are tunnel
// names. Flags may appear anywhere before "--".
package cli

import (
	"errors"
	"fmt"
	"strings"
)

// Command identifies the operation requested on the command line.
type Command int

const (
	// CommandStart runs the supervisor in the foreground (bare `tnl`).
	CommandStart Command = iota
	// CommandStatus prints daemon and tunnel status.
	CommandStatus
	// CommandStopDaemon shuts the daemon down.
	CommandStopDaemon
	// CommandStopTunnel stops a single tunnel through the daemon.
	CommandStopTunnel
	// CommandStartTunnel starts tunnels through the daemon.
	CommandStartTunnel
	// CommandRestartTunnel restarts a single tunnel through the daemon.
	CommandRestartTunnel
	// CommandInstall registers the launch agent.
	CommandInstall
	// CommandUninstall removes the launch agent.
	CommandUninstall
	// CommandVersion prints the version.
	CommandVersion
)

// ErrHelp is returned by Parse when -h or --help is requested; the caller
// should print Usage and exit successfully.
var ErrHelp = errors.New("help requested")

// Usage is the help text printed for -h/--help.
const Usage = `Usage: tnl [options] [tunnel ...]

SSH tunnel manager. By default, tnl starts tunnels in the foreground.

Commands:
  tnl [names]            start tunnels in the foreground
  tnl -d [names]         start tunnels in a background daemon
  tnl status             show daemon and tunnel status
  tnl start [names]      start tunnels through the daemon
  tnl stop               stop the daemon
  tnl stop <name>        stop a single tunnel
  tnl restart <name>     restart a single tunnel
  tnl install            register tnl as a macOS launch agent
  tnl uninstall          remove the macOS launch agent
  tnl version            print the version

Options:
  -d, --detach           run as a background daemon
  -c, --config <path>    config file (default ~/.tnlrc.yaml)
  -h, --help             show this help
`

// Options holds the parsed command line.
type Options struct {
	Command        Command
	Detach         bool
	InternalDaemon bool
	ConfigPath     string
	Names          []string
}

// Parse parses args (excluding the program name) into Options.
//
// A bare `tnl` or `tnl <names>` selects CommandStart in the foreground;
// --detach makes it start a background daemon instead. The verbs status,
// start, stop, restart, install, uninstall and version select the
// corresponding commands.
func Parse(args []string) (*Options, error) {
	opts := &Options{Command: CommandStart}
	var names []string
	var verb string
	flagsDone := false

	for i := 0; i < len(args); i++ {
		a := args[i]
		if !flagsDone && len(a) > 1 && a[0] == '-' {
			switch {
			case a == "-h" || a == "--help":
				return nil, ErrHelp
			case a == "-d" || a == "--detach":
				opts.Detach = true
			case a == "-c" || a == "--config":
				i++
				if i >= len(args) {
					return nil, fmt.Errorf("flag needs an argument: %s", a)
				}
				opts.ConfigPath = args[i]
			case strings.HasPrefix(a, "--config="):
				opts.ConfigPath = strings.TrimPrefix(a, "--config=")
			case a == "--internal-daemon":
				opts.InternalDaemon = true
			case a == "--":
				flagsDone = true
			default:
				return nil, fmt.Errorf("unknown flag: %s", a)
			}
			continue
		}
		if verb == "" && !flagsDone {
			switch a {
			case "status":
				verb, opts.Command = a, CommandStatus
			case "start":
				verb, opts.Command = a, CommandStartTunnel
			case "stop":
				verb, opts.Command = a, CommandStopDaemon
			case "restart":
				verb, opts.Command = a, CommandRestartTunnel
			case "install":
				verb, opts.Command = a, CommandInstall
			case "uninstall":
				verb, opts.Command = a, CommandUninstall
			case "version":
				verb, opts.Command = a, CommandVersion
			default:
				// Bare form: the first positional that is not a command is a
				// tunnel name for the foreground start.
				names = append(names, a)
			}
			continue
		}
		names = append(names, a)
	}
	opts.Names = names

	// `stop` stops the daemon; `stop <name>` stops one tunnel. More than one
	// name is rejected.
	if verb == "stop" {
		switch len(names) {
		case 0:
			opts.Command = CommandStopDaemon
		case 1:
			opts.Command = CommandStopTunnel
		default:
			return nil, fmt.Errorf("stop command does not accept tunnel names")
		}
	}

	if err := checkConflicts(opts, verb); err != nil {
		return nil, err
	}
	return opts, nil
}

// checkConflicts rejects flag/verb combinations that make no sense, in the
// tunn style.
func checkConflicts(opts *Options, verb string) error {
	if opts.Detach && opts.Command != CommandStart {
		return fmt.Errorf("%s command cannot be used with --detach", verb)
	}
	switch opts.Command {
	case CommandStatus, CommandInstall, CommandUninstall, CommandVersion:
		if len(opts.Names) > 0 {
			return fmt.Errorf("%s command does not accept tunnel names", verb)
		}
	case CommandRestartTunnel:
		if len(opts.Names) == 0 {
			return fmt.Errorf("restart requires a tunnel name")
		}
		if len(opts.Names) > 1 {
			return fmt.Errorf("restart command accepts only one tunnel name")
		}
	}
	return nil
}
