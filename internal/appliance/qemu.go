package appliance

import (
	"fmt"
	"os"
	"strconv"
)

// NIC is a virtio NIC of the VM, connected to a tap device on the host.
type NIC struct {
	Tap string
	MAC string
}

// VMConfig is what QEMU is started with.
type VMConfig struct {
	// Disk is the qcow2 overlay of the VM, Seed the cloud-init image.
	Disk, Seed string
	NICs       []NIC
	MemoryMiB  int
	CPUs       int
	// SerialLog is the file the console is written to.
	SerialLog string
	// KVM selects hardware acceleration; without it QEMU emulates (slow, but functional).
	KVM bool
}

// HasKVM reports whether /dev/kvm can be used.
func HasKVM() bool {
	f, err := os.OpenFile("/dev/kvm", os.O_RDWR, 0)
	if err != nil {
		return false
	}
	_ = f.Close()
	return true
}

// QEMUArgs is the command line of the VM.
func QEMUArgs(c VMConfig) []string {
	mem, cpus := c.MemoryMiB, c.CPUs
	if mem == 0 {
		mem = 2048
	}
	if cpus == 0 {
		cpus = 2
	}
	args := []string{"-machine", "q35", "-m", strconv.Itoa(mem), "-smp", strconv.Itoa(cpus), "-nographic"}
	if c.KVM {
		args = append(args, "-accel", "kvm", "-cpu", "host")
	} else {
		args = append(args, "-accel", "tcg", "-cpu", "max")
	}
	args = append(args,
		"-drive", fmt.Sprintf("file=%s,if=virtio,format=qcow2", c.Disk),
		"-drive", fmt.Sprintf("file=%s,if=virtio,format=raw,media=cdrom,readonly=on", c.Seed),
		"-serial", "file:"+c.SerialLog)
	for i, n := range c.NICs {
		id := "net" + strconv.Itoa(i)
		args = append(args,
			"-netdev", fmt.Sprintf("tap,id=%s,ifname=%s,script=no,downscript=no", id, n.Tap),
			"-device", fmt.Sprintf("virtio-net-pci,netdev=%s,mac=%s", id, n.MAC))
	}
	return args
}
