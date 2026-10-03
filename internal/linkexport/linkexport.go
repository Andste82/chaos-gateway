// Package linkexport renders what a link's remote side needs besides its WireGuard configuration:
// the BIRD configuration of the routing protocol that runs over the link (plan M4c, remote-side
// snippet in link exports). It sits above the compiler, which knows the protocol, and the
// wireguard package, which knows the link.
package linkexport

import (
	"errors"

	"github.com/Andste82/chaos-gateway/internal/bird"
	"github.com/Andste82/chaos-gateway/internal/compiler"
	"github.com/Andste82/chaos-gateway/internal/model"
	"github.com/Andste82/chaos-gateway/internal/secrets"
	"github.com/Andste82/chaos-gateway/internal/wireguard"
)

// RemoteBird renders the BIRD configuration for the remote end of the link netID. remoteIface is
// the name of the tunnel interface on the remote machine.
func RemoteBird(cfg *model.Configuration, sec *secrets.Store, netID, remoteIface string) (string, error) {
	keys, err := wireguard.InterfaceKeys(cfg, sec)
	if err != nil {
		return "", err
	}
	tg := compiler.Compile(compiler.Input{Config: cfg, Generation: compiler.Generation{Seq: 1}, Keys: keys})
	if tg.Bird == nil {
		return "", ErrNoRouting
	}
	for _, w := range tg.WireGuard {
		if w.NetworkID != netID {
			continue
		}
		for _, p := range tg.Bird.Config.Protocols {
			if p.Interface == w.Name {
				return bird.RenderRemote(tg.Bird.Config, p, remoteIface)
			}
		}
	}
	return "", ErrNoProtocol
}

// Errors of RemoteBird.
var (
	ErrNoRouting  = errors.New("the configuration runs no routing protocol")
	ErrNoProtocol = errors.New("no routing protocol runs on this link")
)
