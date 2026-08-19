package cli

import (
	"errors"
	"reflect"
	"testing"
)

func TestParse(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want *Options
	}{
		{name: "bare", args: nil, want: &Options{Command: CommandStart}},
		{name: "bare names", args: []string{"web", "db"}, want: &Options{Command: CommandStart, Names: []string{"web", "db"}}},
		{name: "detach short", args: []string{"-d"}, want: &Options{Command: CommandStart, Detach: true}},
		{name: "detach long", args: []string{"--detach"}, want: &Options{Command: CommandStart, Detach: true}},
		{name: "detach names", args: []string{"-d", "web"}, want: &Options{Command: CommandStart, Detach: true, Names: []string{"web"}}},
		{name: "names before detach", args: []string{"web", "--detach"}, want: &Options{Command: CommandStart, Detach: true, Names: []string{"web"}}},
		{name: "status", args: []string{"status"}, want: &Options{Command: CommandStatus}},
		{name: "status watch long", args: []string{"status", "--watch"}, want: &Options{Command: CommandStatus, Watch: true}},
		{name: "watch before status", args: []string{"--watch", "status"}, want: &Options{Command: CommandStatus, Watch: true}},
		{name: "status watch short", args: []string{"status", "-w"}, want: &Options{Command: CommandStatus, Watch: true}},
		{name: "start", args: []string{"start"}, want: &Options{Command: CommandStartTunnel}},
		{name: "start names", args: []string{"start", "web", "db"}, want: &Options{Command: CommandStartTunnel, Names: []string{"web", "db"}}},
		{name: "stop", args: []string{"stop"}, want: &Options{Command: CommandStopDaemon}},
		{name: "stop name", args: []string{"stop", "web"}, want: &Options{Command: CommandStopTunnel, Names: []string{"web"}}},
		{name: "restart", args: []string{"restart", "web"}, want: &Options{Command: CommandRestartTunnel, Names: []string{"web"}}},
		{name: "install", args: []string{"install"}, want: &Options{Command: CommandInstall}},
		{name: "uninstall", args: []string{"uninstall"}, want: &Options{Command: CommandUninstall}},
		{name: "version", args: []string{"version"}, want: &Options{Command: CommandVersion}},
		{name: "config short", args: []string{"-c", "/tmp/tnl.yml"}, want: &Options{Command: CommandStart, ConfigPath: "/tmp/tnl.yml"}},
		{name: "config long", args: []string{"--config", "/tmp/tnl.yml"}, want: &Options{Command: CommandStart, ConfigPath: "/tmp/tnl.yml"}},
		{name: "config equals", args: []string{"--config=/tmp/tnl.yml"}, want: &Options{Command: CommandStart, ConfigPath: "/tmp/tnl.yml"}},
		{name: "config with verb", args: []string{"status", "-c", "/x"}, want: &Options{Command: CommandStatus, ConfigPath: "/x"}},
		{name: "internal daemon", args: []string{"--internal-daemon"}, want: &Options{Command: CommandStart, InternalDaemon: true}},
		{name: "internal daemon reexec", args: []string{"--internal-daemon", "web", "-c", "/x"}, want: &Options{Command: CommandStart, InternalDaemon: true, Names: []string{"web"}, ConfigPath: "/x"}},
		{name: "double dash", args: []string{"--", "status"}, want: &Options{Command: CommandStart, Names: []string{"status"}}},
		{name: "setup", args: []string{"setup"}, want: &Options{Command: CommandSetup}},
		{name: "setup names", args: []string{"setup", "web"}, want: &Options{Command: CommandSetup, Names: []string{"web"}}},
		{name: "setup algorithm", args: []string{"setup", "--algorithm", "ecdsa"}, want: &Options{Command: CommandSetup, SetupAlgorithm: "ecdsa"}},
		{name: "setup algorithm equals", args: []string{"setup", "--algorithm=rsa", "web"}, want: &Options{Command: CommandSetup, SetupAlgorithm: "rsa", Names: []string{"web"}}},
		{name: "setup filename", args: []string{"setup", "--filename", "/tmp/k"}, want: &Options{Command: CommandSetup, SetupFilename: "/tmp/k"}},
		{name: "setup passphrase file", args: []string{"setup", "--passphrase-file", "/tmp/p"}, want: &Options{Command: CommandSetup, SetupPassphraseFile: "/tmp/p"}},
		{name: "setup yes short", args: []string{"setup", "-y"}, want: &Options{Command: CommandSetup, Yes: true}},
		{name: "setup yes long", args: []string{"setup", "--yes"}, want: &Options{Command: CommandSetup, Yes: true}},
		{name: "yes before setup", args: []string{"-y", "setup"}, want: &Options{Command: CommandSetup, Yes: true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse(tt.args)
			if err != nil {
				t.Fatalf("Parse(%q) error: %v", tt.args, err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Parse(%q) = %+v, want %+v", tt.args, got, tt.want)
			}
		})
	}
}

func TestParseErrors(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "unknown flag", args: []string{"--bogus"}, want: "unknown flag: --bogus"},
		{name: "watch without status", args: []string{"--watch"}, want: "--watch can only be used with status"},
		{name: "start with watch", args: []string{"start", "--watch"}, want: "--watch can only be used with status"},
		{name: "status watch with name", args: []string{"status", "--watch", "web"}, want: "status command does not accept tunnel names"},
		{name: "unknown short flag", args: []string{"-x"}, want: "unknown flag: -x"},
		{name: "flag needs argument", args: []string{"-c"}, want: "flag needs an argument: -c"},
		{name: "config long missing arg", args: []string{"--config"}, want: "flag needs an argument: --config"},
		{name: "status with detach", args: []string{"status", "--detach"}, want: "status command cannot be used with --detach"},
		{name: "detach before status", args: []string{"-d", "status"}, want: "status command cannot be used with --detach"},
		{name: "start with detach", args: []string{"start", "-d"}, want: "start command cannot be used with --detach"},
		{name: "stop with detach", args: []string{"stop", "--detach"}, want: "stop command cannot be used with --detach"},
		{name: "restart with detach", args: []string{"restart", "web", "-d"}, want: "restart command cannot be used with --detach"},
		{name: "status with names", args: []string{"status", "web"}, want: "status command does not accept tunnel names"},
		{name: "stop with names", args: []string{"stop", "web", "db"}, want: "stop command does not accept tunnel names"},
		{name: "install with names", args: []string{"install", "web"}, want: "install command does not accept tunnel names"},
		{name: "version with names", args: []string{"version", "web"}, want: "version command does not accept tunnel names"},
		{name: "restart no name", args: []string{"restart"}, want: "restart requires a tunnel name"},
		{name: "restart two names", args: []string{"restart", "web", "db"}, want: "restart command accepts only one tunnel name"},
		{name: "setup with watch", args: []string{"setup", "--watch"}, want: "--watch can only be used with status"},
		{name: "setup with detach", args: []string{"setup", "--detach"}, want: "setup command cannot be used with --detach"},
		{name: "setup two names", args: []string{"setup", "web", "db"}, want: "setup command accepts only one tunnel name"},
		{name: "algorithm without setup", args: []string{"--algorithm", "ed25519"}, want: "--algorithm can only be used with setup"},
		{name: "status with yes", args: []string{"status", "--yes"}, want: "--yes can only be used with setup"},
		{name: "restart with filename", args: []string{"restart", "--filename", "/tmp/k", "web"}, want: "--filename can only be used with setup"},
		{name: "start with passphrase file", args: []string{"start", "--passphrase-file", "/tmp/p"}, want: "--passphrase-file can only be used with setup"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse(tt.args)
			if err == nil {
				t.Fatalf("Parse(%q) = %+v, want error %q", tt.args, got, tt.want)
			}
			if err.Error() != tt.want {
				t.Errorf("Parse(%q) error = %q, want %q", tt.args, err.Error(), tt.want)
			}
		})
	}
}

func TestParseHelp(t *testing.T) {
	for _, args := range [][]string{{"-h"}, {"--help"}, {"status", "-h"}, {"-c", "/x", "--help"}} {
		if _, err := Parse(args); !errors.Is(err, ErrHelp) {
			t.Errorf("Parse(%q) error = %v, want ErrHelp", args, err)
		}
	}
}
