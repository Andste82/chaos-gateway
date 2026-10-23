package engine

// DHCPReopensScheduled is how often a reading of the host found an interface of the DHCP server that
// came up and woke the sender to configure the server again (followDHCPLinks).
func (e *Engine) DHCPReopensScheduled() int {
	e.dhcp.mu.Lock()
	defer e.dhcp.mu.Unlock()
	return e.dhcp.scheduled
}

// OfflineAfter is how long a device stays online after its last evidence (issue #79).
const OfflineAfter = offlineAfter

// DryCompiles is how many compiles the state owner made only to check overlay writes: one per batch of writes (or
// per write that is checked alone), never one per write of a burst.
func (e *Engine) DryCompiles() int { return int(e.dryCompiles.Load()) }
