// Package output renders tunnel status snapshots for the terminal.
package output

import (
	"fmt"
	"io"

	"github.com/ahmadaidin/tnl/internal/status"
)

const (
	colorReset  = "\x1b[0m"
	colorGreen  = "\x1b[32m"
	colorYellow = "\x1b[33m"
	colorRed    = "\x1b[31m"
	colorGray   = "\x1b[90m"
)

// Render writes one colored line per tunnel mapping to w:
//
//	[<tunnel>] <label and spec, or "local:remote"> [<state>]
//
// with a " (attempt N)" suffix while backing off and a " - <message>" suffix
// for error states. Active lines are green, connecting and backing-off lines
// yellow, error lines red, stopped lines gray.
func Render(snapshot []status.TunnelStatus, w io.Writer) {
	for _, t := range snapshot {
		for _, m := range t.Mappings {
			line := fmt.Sprintf("[%s] %s [%s]%s%s",
				t.Name, labelOrSpec(m), m.State, attemptSuffix(m), messageSuffix(m))
			fmt.Fprintf(w, "%s%s%s\n", colorFor(m.State), line, colorReset)
		}
	}
}

// labelOrSpec returns the mapping's label, or its port spec (local:remote,
// or local:desthost:remote when forwarding through the ssh host) when the
// label is unset.
func labelOrSpec(m status.MappingStatus) string {
	spec := mappingSpec(m)
	if m.Label != "" {
		return fmt.Sprintf("%s %s", m.Label, spec)
	}
	return spec
}

func mappingSpec(m status.MappingStatus) string {
	if m.RemoteHost != "" {
		return fmt.Sprintf("%d:%s:%d", m.Local, m.RemoteHost, m.Remote)
	}
	return fmt.Sprintf("%d:%d", m.Local, m.Remote)
}

// attemptSuffix reports the restart attempt while a mapping is backing off.
func attemptSuffix(m status.MappingStatus) string {
	if m.State == status.StateBackoff && m.Attempt > 0 {
		return fmt.Sprintf(" (attempt %d)", m.Attempt)
	}
	return ""
}

// messageSuffix reports the message for error states.
func messageSuffix(m status.MappingStatus) string {
	if m.State == status.StateError && m.Message != "" {
		return " - " + m.Message
	}
	return ""
}

// colorFor returns the ANSI color code for a mapping state.
func colorFor(s status.MappingState) string {
	switch s {
	case status.StateActive:
		return colorGreen
	case status.StateConnecting, status.StateBackoff:
		return colorYellow
	case status.StateError:
		return colorRed
	default: // status.StateStopped
		return colorGray
	}
}
