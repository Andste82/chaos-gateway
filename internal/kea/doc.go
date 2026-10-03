// Package kea drives the Kea DHCPv4 server (plan §2.7, M6a). It renders the server's configuration
// from the model (one subnet per network with DHCP enabled, bound to the network's bridge,
// reservations from the devices with a fixed address), talks to the control socket (`config-set`,
// `config-get`, lease commands) and turns the `run_script` hook's environment into lease events.
//
// The package knows nothing of the engine: it produces text and speaks a protocol. Kea itself runs in
// its own container (deploy/compose.kea.yaml); its paths follow Kea's path restrictions
// (/run/kea for the control socket, /var/lib/kea for the lease file).
package kea
