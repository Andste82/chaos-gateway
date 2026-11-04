package ntpserver

import (
	"fmt"
	"net"
	"time"

	"github.com/Andste82/chaos-gateway/internal/clock"
)

// Probe sends the health check's query to the server at addr and succeeds when a server reply to it comes back (the
// container's health check). The server answers the probe before it has a configuration, so it tells that the server
// is up, not that it has been configured.
func Probe(addr string) error {
	c, err := net.DialTimeout("udp", addr, 2*time.Second)
	if err != nil {
		return err
	}
	defer func() { _ = c.Close() }()
	_ = c.SetDeadline(clock.NewReal().Now().Add(2 * time.Second))
	q := Packet{Version: 4, Mode: ModeClient, Transmit: probeTransmit}
	if _, err := c.Write(q.Marshal()); err != nil {
		return err
	}
	buf := make([]byte, 512)
	n, err := c.Read(buf)
	if err != nil {
		return err
	}
	p, err := Parse(buf[:n])
	if err != nil {
		return err
	}
	if p.Mode != ModeServer || p.Origin != probeTransmit {
		return fmt.Errorf("%s answered, but not to the probe", addr)
	}
	return nil
}
