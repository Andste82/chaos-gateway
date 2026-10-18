package engine

// DHCPReopensScheduled is how often a reading of the host found an interface of the DHCP server that
// came up and woke the sender to configure the server again (followDHCPLinks).
func (e *Engine) DHCPReopensScheduled() int {
	e.dhcp.mu.Lock()
	defer e.dhcp.mu.Unlock()
	return e.dhcp.scheduled
}
