package appliance

import (
	"context"
	"os"
	"path/filepath"
	"time"
)

// logCommands are what is collected from the VM after a test: enough to see why a deployment did
// not come up (plan M5b: "collect logs").
var logCommands = []struct{ File, Command string }{
	{"cloud-init-output.log", "sudo cat /var/log/cloud-init-output.log"},
	{"journal.log", "sudo journalctl -b --no-pager -o short-precise"},
	{"docker-journal.log", "sudo journalctl -b -u docker --no-pager"},
	{"docker-ps.txt", "sudo docker ps -a"},
	{"docker-logs.txt", "for c in $(sudo docker ps -aq); do echo \"=== $c\"; sudo docker logs --tail 500 $c 2>&1; done"},
	{"ip-addr.txt", "ip -d addr; echo; ip route; echo; ip -4 rule; echo; ip route show table 100"},
	{"nft-ruleset.txt", "sudo nft list ruleset"},
	{"iptables.txt", "sudo iptables -S; sudo iptables -t nat -S"},
	{"sysctl.txt", "sysctl net.ipv4.ip_forward; cat /etc/sysctl.d/90-chaos-gateway.conf /etc/modules-load.d/chaos-gateway.conf"},
	{"modules.txt", "lsmod"},
	{"os-release.txt", "cat /etc/os-release; uname -a"},
}

// CollectLogs writes the logs of the VM into dir: the commands above (best effort, each with its own
// time limit), the serial console and QEMU's own output.
func CollectLogs(ctx context.Context, v *VM, dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if v.client != nil {
		for _, c := range logCommands {
			cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
			r, _ := v.Run(cctx, c.Command, nil)
			cancel()
			_ = os.WriteFile(filepath.Join(dir, c.File), []byte(r.Stdout+r.Stderr), 0o644)
		}
	}
	if b, err := os.ReadFile(v.SerialLog); err == nil {
		_ = os.WriteFile(filepath.Join(dir, "serial.log"), b, 0o644)
	}
	return os.WriteFile(filepath.Join(dir, "qemu.log"), []byte(v.QEMULog()), 0o644)
}
