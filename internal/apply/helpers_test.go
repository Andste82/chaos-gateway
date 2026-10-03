package apply_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Andste82/chaos-gateway/internal/apply"
	"github.com/Andste82/chaos-gateway/internal/compiler"
	"github.com/Andste82/chaos-gateway/internal/executor"
)

func ptr[T any](v T) *T { return &v }

func asError(err error, target **apply.Error) bool { return errors.As(err, target) }

func js(v any) string { b, _ := json.Marshal(v); return string(b) }

func (e *env) nftJSON() string {
	s, err := apply.ReadState(context.Background(), e.exec(), "", apply.Want{})
	if err != nil {
		e.t.Fatal(err)
	}
	return js(s.Nft)
}

// newEnvHandle adds the helpers that reach around the executor to manipulate the kernel.
type newEnvHandle struct {
	*env
	tg *compiler.Target
}

func newHandle(t *testing.T) *newEnvHandle {
	h := &newEnvHandle{env: newEnv(t)}
	return h
}

func (h *newEnvHandle) compile() *compiler.Target {
	h.tg = h.env.compile()
	return h.tg
}

func (h *newEnvHandle) mgmtSet() string {
	for _, s := range h.tg.Nft.Sets {
		if strings.HasPrefix(s.Name, "mgmt_src") {
			return s.Name
		}
	}
	return ""
}

func (h *newEnvHandle) run(c executor.Command) {
	if _, err := h.k.Run(context.Background(), c); err != nil {
		h.t.Fatal(err)
	}
}

func (h *newEnvHandle) runSysctl(arg string) {
	h.run(executor.Command{Tool: executor.ToolSysctl, Args: []string{"-w", arg}})
}
func (h *newEnvHandle) runEthtool(dev string) {
	h.run(executor.Command{Tool: executor.ToolEthtool, Args: []string{"-K", dev, "gro", "on"}})
}
func (h *newEnvHandle) runIP(args ...string) {
	h.run(executor.Command{Tool: executor.ToolIP, Args: args})
}
func (h *newEnvHandle) runIptables(args ...string) {
	h.run(executor.Command{Tool: executor.ToolIptables, Args: append([]string{"-w", "5", args[0], "DOCKER-USER"}, append(args[2:], "-m", "comment", "--comment", "chaosgw", "-j", "ACCEPT")...)})
}
func (h *newEnvHandle) batch(lines string) {
	h.run(executor.Command{Tool: executor.ToolIP, Args: []string{"-4", "-force", "-batch", "-"}, Stdin: lines + "\n"})
}
