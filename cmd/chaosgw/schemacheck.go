package main

import (
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/Andste82/chaos-gateway/internal/store"
	"github.com/Andste82/chaos-gateway/internal/version"
)

// runSchemaCheck is `chaosgw schema-check`: whether this build can read the revisions in the state directory (plan §3.9,
// downgrade). The update script (deploy/update.sh) runs it with the image it is about to install, in a throw-away container
// that mounts the state directory read-only, before it stops the running gateway: a newer schema is refused there, with the
// version that is needed, and not after the executor has torn the test networks down. It takes no lock and writes nothing.
func runSchemaCheck(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("chaosgw schema-check", flag.ContinueOnError)
	fs.SetOutput(stderr)
	stateDir := fs.String("state-dir", "/var/lib/chaosgw/state", "directory of the revisions")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: chaosgw schema-check [--state-dir <dir>]")
		return 2
	}
	if err := store.CheckSchema(*stateDir); err != nil {
		var newer *store.ErrNewerSchema
		if errors.As(err, &newer) {
			fmt.Fprintf(stderr, "chaosgw schema-check: %v\n", err)
			fmt.Fprintf(stderr, "chaosgw schema-check: %s reads schema versions up to %d; the configuration needs version %d: run the newer version, or restore a backup of %s\n",
				version.String("chaosgw"), newer.Supported, newer.Found, *stateDir)
			return 1
		}
		fmt.Fprintf(stderr, "chaosgw schema-check: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "%s reads the configuration in %s (schema version %d)\n", version.String("chaosgw"), *stateDir, store.SchemaVersion)
	return 0
}
