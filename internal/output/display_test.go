package output

import (
	"bytes"
	"testing"

	"github.com/ahmadaidin/tnl/internal/status"
)

func TestRender(t *testing.T) {
	snapshot := []status.TunnelStatus{
		{Name: "web", Mappings: []status.MappingStatus{
			{Local: 3000, Remote: 3000, State: status.StateActive},
		}},
		{Name: "db_suteki", Mappings: []status.MappingStatus{
			{Local: 3303, Remote: 3303, State: status.StateActive},
			{Label: "maria", Local: 5432, Remote: 5432, State: status.StateActive},
			{Label: "redis", Local: 6380, RemoteHost: "redis", Remote: 6379, State: status.StateBackoff, Attempt: 8},
		}},
		{Name: "broken", Mappings: []status.MappingStatus{
			{Local: 4000, Remote: 4000, State: status.StateError, Message: "port 4000 in use"},
		}},
	}
	want := "" +
		"\x1b[32mweb\x1b[0m [\x1b[32m1 mapping, 1 active\x1b[0m]\n" +
		"  \x1b[34m-\x1b[0m \x1b[36m3000:3000\x1b[0m [\x1b[32mactive\x1b[0m]\n" +
		"\x1b[33mdb_suteki\x1b[0m [\x1b[33m3 mappings, 1 backing off, 2 active\x1b[0m]\n" +
		"  \x1b[34m-\x1b[0m     \x1b[36m3303:3303\x1b[0m       [\x1b[32mactive\x1b[0m]\n" +
		"  \x1b[34mmaria\x1b[0m \x1b[36m5432:5432\x1b[0m       [\x1b[32mactive\x1b[0m]\n" +
		"  \x1b[34mredis\x1b[0m \x1b[36m6380:redis:6379\x1b[0m [\x1b[33mbacking off\x1b[0m] (attempt 8)\n" +
		"\x1b[31mbroken\x1b[0m [\x1b[31m1 mapping, 1 error\x1b[0m]\n" +
		"  \x1b[34m-\x1b[0m \x1b[36m4000:4000\x1b[0m [\x1b[31merror\x1b[0m] - port 4000 in use\n"

	var buf bytes.Buffer
	Render(snapshot, &buf)
	if buf.String() != want {
		t.Errorf("Render output:\n%q\nwant:\n%q", buf.String(), want)
	}
}

func TestRenderEmptyTunnel(t *testing.T) {
	var buf bytes.Buffer
	Render([]status.TunnelStatus{{Name: "empty"}}, &buf)
	want := "\x1b[90mempty\x1b[0m [\x1b[90m0 mappings\x1b[0m]\n"
	if buf.String() != want {
		t.Errorf("Render output %q, want %q", buf.String(), want)
	}
}

func TestRenderEmpty(t *testing.T) {
	var buf bytes.Buffer
	Render(nil, &buf)
	if buf.Len() != 0 {
		t.Errorf("Render(nil) produced %q, want empty output", buf.String())
	}
}
