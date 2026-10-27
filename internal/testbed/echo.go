package testbed

import (
	"strconv"
	"strings"
	"time"
)

// plainEchoScript is an RFC 862 echo server: it returns every UDP datagram and every byte of a TCP connection unchanged, on one
// port. The self-test of a probe (M25) sends its datagrams and its bulk transfer to it; the one-way probes of StartEcho need
// the sender's clock in the datagram and are no echo in that sense.
const plainEchoScript = `import socket, sys, threading
addr, port = sys.argv[1], int(sys.argv[2])
u = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
u.bind((addr, port))
t = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
t.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
t.bind((addr, port))
t.listen(8)
def serve(c):
    try:
        while True:
            d = c.recv(65536)
            if not d:
                break
            c.sendall(d)
    finally:
        c.close()
def tcp():
    while True:
        c, _ = t.accept()
        threading.Thread(target=serve, args=(c,), daemon=True).start()
threading.Thread(target=tcp, daemon=True).start()
print("ready", flush=True)
while True:
    d, a = u.recvfrom(65536)
    u.sendto(d, a)
`

// StartPlainEcho starts an RFC 862 echo server (UDP and TCP) in ns, listening on addr:port, and waits until it is ready. It runs
// until the bed is destroyed.
func StartPlainEcho(t testingTB, ns *Namespace, addr string, port int) {
	t.Helper()
	p := ns.Start("python3", "-c", plainEchoScript, addr, strconv.Itoa(port))
	deadline := time.Now().Add(time.Minute)
	for !strings.Contains(p.Output(), "ready") {
		select {
		case <-p.Done():
			t.Fatalf("the echo server in %s did not start: %s", ns.Short, p.Output())
		case <-time.After(20 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			t.Fatalf("the echo server in %s is not ready: %s", ns.Short, p.Output())
		}
	}
}
