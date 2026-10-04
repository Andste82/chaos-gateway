package apply

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/Andste82/chaos-gateway/internal/bird"
	"github.com/Andste82/chaos-gateway/internal/compiler"
	"github.com/Andste82/chaos-gateway/internal/executor"
)

// idleText is the configuration of an instance that has nothing to do: it is applied when routing
// is switched off, so the routes BIRD learned leave table 100 and the neighbors see the sessions end.
var idleText = bird.Idle(compiler.PolicyTable)

// TextHash is the hash by which the state recognizes a configuration file.
func TextHash(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

// readBird reads the BIRD instance. An executor without a BIRD directory is not an error: the
// state then has no BIRD part, and a target that wants BIRD fails in plan.
func readBird(ctx context.Context, ex Exec, instance string) (*executor.BirdState, error) {
	out, err := ex.Do(ctx, &executor.Read{What: executor.ReadBird, Instance: instance})
	if err != nil {
		if errors.Is(err, executor.ErrNoBirdDir) {
			return nil, nil
		}
		return nil, fmt.Errorf("read BIRD: %w", err)
	}
	return decode[*executor.BirdState](out, 0, "bird")
}

// planBird adds the BIRD step: last, because the kernel protocol needs table 100 and the interfaces,
// and the neighbors need the input rules.
func planBird(t *compiler.Target, s *State, p *Plan) error {
	switch {
	case t.Bird != nil:
		if s.Bird == nil {
			return errors.New("the configuration uses dynamic routing but the executor has no BIRD directory (--bird-dir)")
		}
		if s.Bird.Running && s.Bird.ConfigHash == TextHash(t.Bird.Text) {
			return nil
		}
		p.Ops = append(p.Ops, &executor.Bird{Action: "apply", Instance: t.Bird.Instance, Config: t.Bird.Text, ImportTables: t.Bird.ImportTables})
		p.Summary = append(p.Summary, "bird: configure "+t.Bird.Instance)
	case s.Bird != nil && s.Bird.ConfigHash != "" && s.Bird.ConfigHash != TextHash(idleText):
		p.Ops = append(p.Ops, &executor.Bird{Action: "apply", Instance: compiler.BirdInstance, Config: idleText})
		p.Summary = append(p.Summary, "bird: withdraw all protocols")
	}
	return nil
}

// verifyBird compares the running instance with the target.
func verifyBird(t *compiler.Target, s *State, bad func(string, string, ...any)) {
	if t.Bird == nil {
		if s.Bird != nil && s.Bird.ConfigHash != "" && s.Bird.ConfigHash != TextHash(idleText) {
			bad("bird", "the BIRD instance runs a configuration although routing is switched off")
		}
		return
	}
	if s.Bird == nil || !s.Bird.Running {
		bad("bird", "the BIRD instance %s is not running", t.Bird.Instance)
		return
	}
	if s.Bird.ConfigHash != TextHash(t.Bird.Text) {
		bad("bird", "the BIRD instance runs another configuration than the target")
	}
}
