// Package output renders tunnel status snapshots for the terminal.
package output

import (
	"fmt"
	"io"
	"strings"

	"github.com/ahmadaidin/tnl/internal/status"
)

const (
	colorReset  = "\x1b[0m"
	colorGreen  = "\x1b[32m"
	colorYellow = "\x1b[33m"
	colorRed    = "\x1b[31m"
	colorGray   = "\x1b[90m"
	colorBlue   = "\x1b[34m"
	colorCyan   = "\x1b[36m"
)

// Render writes one colored summary header per tunnel followed by its mapping rows.
func Render(snapshot []status.TunnelStatus, w io.Writer) {
	for _, t := range snapshot {
		tunnelState, summary := tunnelSummary(t.Mappings)
		stateColor := colorFor(tunnelState)
		_, _ = fmt.Fprintf(w, "%s%s%s [%s%s%s]\n", stateColor, t.Name, colorReset, stateColor, summary, colorReset)

		labelWidth, specWidth := 1, 0
		parts := make([][2]string, len(t.Mappings))
		for i, m := range t.Mappings {
			label, spec := mappingParts(m)
			if label == "" {
				label = "-"
			}
			parts[i] = [2]string{label, spec}
			if len(label) > labelWidth {
				labelWidth = len(label)
			}
			if len(spec) > specWidth {
				specWidth = len(spec)
			}
		}

		for i, m := range t.Mappings {
			label, spec := parts[i][0], parts[i][1]
			_, _ = fmt.Fprintf(w, "  %s%s%s%s %s%s%s%s [%s%s%s]%s%s\n",
				colorBlue, label, colorReset, spaces(labelWidth-len(label)),
				colorCyan, spec, colorReset, spaces(specWidth-len(spec)),
				colorFor(m.State), m.State, colorReset,
				attemptSuffix(m), messageSuffix(m))
		}
	}
}

func spaces(n int) string {
	if n <= 0 {
		return ""
	}
	return fmt.Sprintf("%*s", n, "")
}

func tunnelSummary(mappings []status.MappingStatus) (state status.MappingState, text string) {
	if len(mappings) == 0 {
		return status.StateStopped, "0 mappings"
	}

	counts := make(map[status.MappingState]int, 5)
	for _, m := range mappings {
		counts[m.State]++
	}
	state = status.StateStopped
	for _, candidate := range []status.MappingState{
		status.StateError,
		status.StateBackoff,
		status.StateConnecting,
		status.StateActive,
		status.StateStopped,
	} {
		if counts[candidate] > 0 {
			state = candidate
			break
		}
	}

	text = fmt.Sprintf("%d mapping", len(mappings))
	if len(mappings) != 1 {
		text += "s"
	}
	parts := make([]string, 0, 5)
	for _, candidate := range []status.MappingState{
		status.StateError,
		status.StateBackoff,
		status.StateConnecting,
		status.StateActive,
		status.StateStopped,
	} {
		if count := counts[candidate]; count > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", count, candidate))
		}
	}
	if len(parts) > 0 {
		text += ", " + strings.Join(parts, ", ")
	}
	return state, text
}

// labelOrSpec returns the mapping's label, or its port spec (local:remote,
// or local:desthost:remote when forwarding through the ssh host) when the
// label is unset.
func mappingParts(m status.MappingStatus) (label, spec string) {
	return m.Label, mappingSpec(m)
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
