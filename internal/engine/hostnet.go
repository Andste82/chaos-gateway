package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/Andste82/chaos-gateway/internal/executor"
)

// HostPreflight reads through the executor the checks it makes of the host it runs on (plan §3.4, M28): the tools, the kernel
// modules it loads, forwarding, /proc/sys, the namespaces, other firewalls and the data directories. The API adds them to its own
// report; the executor, privileged, sees what the API container cannot.
func (e *Engine) HostPreflight(ctx context.Context) (*executor.HostPreflightState, error) {
	res, err := e.cfg.Exec.Do(ctx, &executor.Read{What: executor.ReadHostPreflight})
	if err != nil {
		return nil, err
	}
	if len(res.Data) == 0 {
		return nil, errors.New("read the executor's preflight: no answer")
	}
	var st executor.HostPreflightState
	if err := json.Unmarshal(res.Data[0], &st); err != nil {
		return nil, fmt.Errorf("read the executor's preflight: %w", err)
	}
	return &st, nil
}

// HostNetConfig reads through the executor where the host's operating system configures network interfaces
// (netplan entries, NetworkManager connections): the preflight's check that the test interfaces carry no OS
// configuration (plan §3.4, docs/open-items.md P3-M12-07). The executor reads the files as root, the API
// container could not; only the entries come back.
func (e *Engine) HostNetConfig(ctx context.Context) (*executor.HostNetState, error) {
	res, err := e.cfg.Exec.Do(ctx, &executor.Read{What: executor.ReadHostNet})
	if err != nil {
		return nil, err
	}
	if len(res.Data) == 0 {
		return nil, errors.New("read the host's network configuration: no answer")
	}
	var st executor.HostNetState
	if err := json.Unmarshal(res.Data[0], &st); err != nil {
		return nil, fmt.Errorf("read the host's network configuration: %w", err)
	}
	return &st, nil
}
