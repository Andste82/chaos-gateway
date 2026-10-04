package executor

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// ErrOutOfScope is wrapped by every scope violation.
var ErrOutOfScope = errors.New("outside the executor's scope")

// Scope holds what the statically validated operations are checked against at run time: the
// interfaces that belong to Chaos Gateway. The static parts (table inet chaosgw, routing tables
// and the protocol tag, the closed set of tc keywords) are enforced by the decoder.
type Scope struct {
	mu      sync.RWMutex
	devs    map[string]bool
	osOwned map[string]bool
}

// NewScope returns a scope with the given interfaces assigned.
func NewScope(devs ...string) *Scope {
	s := &Scope{}
	s.Set(devs)
	return s
}

// Set replaces the assigned interfaces.
func (s *Scope) Set(devs []string) {
	m := make(map[string]bool, len(devs))
	for _, d := range devs {
		m[d] = true
	}
	s.mu.Lock()
	s.devs = m
	s.mu.Unlock()
}

// SetOSOwned replaces the OS-owned subset of the assigned interfaces (M3-01): assigned for tc,
// routing and DOCKER-USER, but off limits to links, sysctl, wireguard and service_ns.
func (s *Scope) SetOSOwned(devs []string) {
	m := make(map[string]bool, len(devs))
	for _, d := range devs {
		m[d] = true
	}
	s.mu.Lock()
	s.osOwned = m
	s.mu.Unlock()
}

// Devs returns the assigned interfaces, sorted.
func (s *Scope) Devs() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, 0, len(s.devs))
	for d := range s.devs {
		out = append(out, d)
	}
	sort.Strings(out)
	return out
}

// OSOwned returns the OS-owned interfaces, sorted.
func (s *Scope) OSOwned() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, 0, len(s.osOwned))
	for d := range s.osOwned {
		out = append(out, d)
	}
	sort.Strings(out)
	return out
}

// Has reports whether the interface is assigned.
func (s *Scope) Has(dev string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.devs[dev]
}

// IsOSOwned reports whether the interface is the host's own (M3-01).
func (s *Scope) IsOSOwned(dev string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.osOwned[dev]
}

func (s *Scope) need(dev string) error {
	if !s.Has(dev) {
		return fmt.Errorf("%w: interface %q is not assigned to Chaos Gateway", ErrOutOfScope, dev)
	}
	return nil
}

// needNotOSOwned refuses an interface that is assigned only as the host's own (M3-01): it must
// already pass need() too, since an unassigned interface is refused that way first.
func (s *Scope) needNotOSOwned(dev string) error {
	if s.IsOSOwned(dev) {
		return fmt.Errorf("%w: interface %q is OS-owned, not Chaos Gateway's own", ErrOutOfScope, dev)
	}
	return nil
}

// Check verifies that the operation only touches assigned interfaces. For an AssignInterfaces
// operation earlier in the same batch, pass the interfaces it assigns as `pending`.
func (s *Scope) Check(op Operation) error {
	switch o := op.(type) {
	case *TC:
		for i, e := range o.Entries {
			if err := s.need(e.Dev); err != nil {
				return fmt.Errorf("entries[%d]: %w", i, err)
			}
			// "dev" inside the arguments names a second interface (mirred redirect)
			for j := 0; j+1 < len(e.Args); j++ {
				if e.Args[j] == "dev" {
					if err := s.need(e.Args[j+1]); err != nil {
						return fmt.Errorf("entries[%d]: %w", i, err)
					}
				}
			}
			if len(e.Args) > 0 && e.Args[len(e.Args)-1] == "dev" {
				return fmt.Errorf("entries[%d]: %w: dangling dev", i, ErrOutOfScope)
			}
		}
	case *Routing:
		for i, r := range o.Routes {
			if r.Dev != "" {
				if err := s.need(r.Dev); err != nil {
					return fmt.Errorf("routes[%d]: %w", i, err)
				}
			}
		}
		for i, r := range o.Rules {
			for _, d := range []string{r.Iif, r.Oif} {
				if d != "" {
					if err := s.need(d); err != nil {
						return fmt.Errorf("rules[%d]: %w", i, err)
					}
				}
			}
		}
	case *Links:
		for i, e := range o.Entries {
			if err := s.need(e.Name); err != nil {
				return fmt.Errorf("entries[%d]: %w", i, err)
			}
			if err := s.needNotOSOwned(e.Name); err != nil {
				return fmt.Errorf("entries[%d]: %w", i, err)
			}
			if e.Master != "" {
				if err := s.need(e.Master); err != nil {
					return fmt.Errorf("entries[%d]: %w", i, err)
				}
				if err := s.needNotOSOwned(e.Master); err != nil {
					return fmt.Errorf("entries[%d]: %w", i, err)
				}
			}
		}
	case *WireGuard:
		if err := s.need(o.Name); err != nil {
			return err
		}
		return s.needNotOSOwned(o.Name)
	case *ServiceNS:
		if err := s.need(o.HostIf); err != nil {
			return err
		}
		return s.needNotOSOwned(o.HostIf)
	case *Bird:
		return nil // no interface: the instance is a name inside the executor's own BIRD directory
	case *Sysctl:
		for i, e := range o.Entries {
			if e.Dev != "" {
				if err := s.need(e.Dev); err != nil {
					return fmt.Errorf("entries[%d]: %w", i, err)
				}
				if err := s.needNotOSOwned(e.Dev); err != nil {
					return fmt.Errorf("entries[%d]: %w", i, err)
				}
			}
		}
	case *Offloads:
		return s.needAll(o.Devs)
	case *DockerUser:
		return s.needAll(o.Devs)
	}
	return nil
}

func (s *Scope) needAll(devs []string) error {
	for _, d := range devs {
		if err := s.need(d); err != nil {
			return err
		}
	}
	return nil
}
