package output

import (
	"bytes"
	"testing"

	"github.com/ahmadaidin/tnl/internal/status"
)

func TestRender(t *testing.T) {
	snapshot := []status.TunnelStatus{
		{
			Name: "web",
			Mappings: []status.MappingStatus{
				{Label: "primary", Local: 3000, Remote: 3000, State: status.StateActive},
				{Local: 3001, Remote: 3001, State: status.StateConnecting},
				{Local: 3002, Remote: 3002, State: status.StateBackoff, Attempt: 3},
				{Local: 3003, Remote: 3003, State: status.StateError, Message: "port 3003 in use"},
				{Local: 3004, Remote: 3004, State: status.StateStopped},
			},
		},
		{
			Name: "db",
			Mappings: []status.MappingStatus{
				{Label: "postgres", Local: 5432, Remote: 5432, State: status.StateActive, Attempt: 0},
				{Local: 3329, RemoteHost: "db.suteki.tech", Remote: 3306, State: status.StateActive},
			},
		},
	}
	want := "" +
		"\x1b[32m[web] primary 3000:3000 [active]\x1b[0m\n" +
		"\x1b[33m[web] 3001:3001 [connecting]\x1b[0m\n" +
		"\x1b[33m[web] 3002:3002 [backing off] (attempt 3)\x1b[0m\n" +
		"\x1b[31m[web] 3003:3003 [error] - port 3003 in use\x1b[0m\n" +
		"\x1b[90m[web] 3004:3004 [stopped]\x1b[0m\n" +
		"\x1b[32m[db] postgres 5432:5432 [active]\x1b[0m\n" +
		"\x1b[32m[db] 3329:db.suteki.tech:3306 [active]\x1b[0m\n"

	var buf bytes.Buffer
	Render(snapshot, &buf)
	if buf.String() != want {
		t.Errorf("Render output:\n%q\nwant:\n%q", buf.String(), want)
	}
}

func TestRenderEmpty(t *testing.T) {
	var buf bytes.Buffer
	Render(nil, &buf)
	if buf.Len() != 0 {
		t.Errorf("Render(nil) produced %q, want empty output", buf.String())
	}
}
