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
	// CommandSetup provisions ssh identities for tunnels.
	CommandSetup
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
  tnl status -w, --watch continuously refresh daemon and tunnel status
  tnl start [names]      start tunnels through the daemon
  tnl stop               stop the daemon
  tnl stop <name>        stop a single tunnel
  tnl restart <name>     restart a single tunnel
  tnl setup [name]       provision ssh identity for tunnels
  tnl install            register tnl as a macOS launch agent
  tnl uninstall          remove the macOS launch agent
  tnl version            print the version

Options:
  -d, --detach           run as a background daemon
  -w, --watch            refresh status every second
  -c, --config <path>    config file (default ~/.tnlrc.yaml)
      --algorithm <alg>  key algorithm: ed25519, ecdsa, or rsa
      --filename <path>  private key path (overrides the recommendation)
      --passphrase-file <path>
                         read the key passphrase from a file (empty = none)
  -y, --yes              accept defaults; push via agent only
  -h, --help             show this help
`

// Options holds the parsed command line.
type Options struct {
	Command        Command
	Detach         bool
	Watch          bool
	InternalDaemon bool
	ConfigPath     string
	Names          []string

	// Setup options for CommandSetup.
	SetupAlgorithm      string
	SetupFilename       string
	SetupPassphraseFile string
	Yes                 bool
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
			case a == "-w" || a == "--watch":
				opts.Watch = true
			case a == "-c" || a == "--config":
				i++
				if i >= len(args) {
					return nil, fmt.Errorf("flag needs an argument: %s", a)
				}
				opts.ConfigPath = args[i]
			case strings.HasPrefix(a, "--config="):
				opts.ConfigPath = strings.TrimPrefix(a, "--config=")
			case a == "--algorithm":
				i++
				if i >= len(args) {
					return nil, fmt.Errorf("flag needs an argument: %s", a)
				}
				opts.SetupAlgorithm = args[i]
			case strings.HasPrefix(a, "--algorithm="):
				opts.SetupAlgorithm = strings.TrimPrefix(a, "--algorithm=")
			case a == "--filename":
				i++
				if i >= len(args) {
					return nil, fmt.Errorf("flag needs an argument: %s", a)
				}
				opts.SetupFilename = args[i]
			case strings.HasPrefix(a, "--filename="):
				opts.SetupFilename = strings.TrimPrefix(a, "--filename=")
			case a == "--passphrase-file":
				i++
				if i >= len(args) {
					return nil, fmt.Errorf("flag needs an argument: %s", a)
				}
				opts.SetupPassphraseFile = args[i]
			case strings.HasPrefix(a, "--passphrase-file="):
				opts.SetupPassphraseFile = strings.TrimPrefix(a, "--passphrase-file=")
			case a == "-y" || a == "--yes":
				opts.Yes = true
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
			case "setup":
				verb, opts.Command = a, CommandSetup
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
	if opts.Watch && opts.Command != CommandStatus {
		return fmt.Errorf("--watch can only be used with status")
	}
	if opts.Detach && opts.Command != CommandStart {
		return fmt.Errorf("%s command cannot be used with --detach", verb)
	}
	if opts.Command != CommandSetup {
		switch {
		case opts.SetupAlgorithm != "":
			return fmt.Errorf("--algorithm can only be used with setup")
		case opts.SetupFilename != "":
			return fmt.Errorf("--filename can only be used with setup")
		case opts.SetupPassphraseFile != "":
			return fmt.Errorf("--passphrase-file can only be used with setup")
		case opts.Yes:
			return fmt.Errorf("--yes can only be used with setup")
		}
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
	case CommandSetup:
		if len(opts.Names) > 1 {
			return fmt.Errorf("setup command accepts only one tunnel name")
		}
	}
	return nil
}
