package main

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/Andste82/chaos-gateway/internal/executor"
	"github.com/Andste82/chaos-gateway/internal/preflight"
)

// The directories of the persistence layout the executor's container mounts are checked for the user of the API container, the
// first uid it lets onto the socket; the secrets must not be open to others.
func TestThePreflightChecksTheMountedDirectoriesForTheAPIsUser(t *testing.T) {
	dirs := preflightDirs(uidList{65532, 1000}, "/var/lib/chaosgw/exec/state.json", "/var/lib/chaosgw/secrets", "/var/lib/chaosgw/captures")
	want := map[string]preflight.DataDir{
		"/var/lib/chaosgw/exec":     {Path: "/var/lib/chaosgw/exec", UID: -1},
		"/var/lib/chaosgw/secrets":  {Path: "/var/lib/chaosgw/secrets", UID: 65532, MaxMode: 0o700},
		"/var/lib/chaosgw/captures": {Path: "/var/lib/chaosgw/captures", UID: 65532},
	}
	if len(dirs) != len(want) {
		t.Fatalf("dirs: %+v", dirs)
	}
	for _, d := range dirs {
		w, ok := want[d.Path]
		if !ok || d.UID != w.UID || d.MaxMode != w.MaxMode || d.What == "" {
			t.Errorf("%+v, want %+v", d, w)
		}
	}
	// no volumes named, no user allowed: only the executor's own directory, and no owner to check
	dirs = preflightDirs(nil, "/x/state.json", "", "")
	if len(dirs) != 1 || dirs[0].Path != "/x" || dirs[0].UID != -1 {
		t.Errorf("dirs: %+v", dirs)
	}
}

func TestTheChecksOfTheHostAreInTheContainerLogWithTheirFixes(t *testing.T) {
	var b bytes.Buffer
	logPreflight(executor.HostPreflightState{Checks: []preflight.HostCheck{
		{ID: "host.tools", Status: "ok", Message: "ip utility"},
		{ID: "host.firewall", Status: "warn", Message: "another firewall is active", Fix: "stop firewalld"},
		{ID: "host.netns", Status: "fail", Message: "cannot create a network namespace", Fix: "run the executor privileged"},
	}}, slog.New(slog.NewTextHandler(&b, nil)))
	out := b.String()
	for _, want := range []string{"level=INFO msg=preflight check=host.tools", "level=WARN msg=preflight check=host.firewall", `fix="stop firewalld"`, "level=ERROR msg=preflight check=host.netns", `fix="run the executor privileged"`} {
		if !strings.Contains(out, want) {
			t.Errorf("the log lacks %q:\n%s", want, out)
		}
	}
}
