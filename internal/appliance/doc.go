// Package appliance is the harness of test level 2 (plan §4.5, M5b): a gateway in a virtual
// machine booted from an Ubuntu cloud image, with virtio NICs connected through tap devices and
// bridges to a client and a server namespace on the host. The tests on top of it (build tag
// `appliance`) run the host setup, load the image into the VM's Docker, start the executor
// container, apply a configuration and pass traffic through the gateway.
//
// The harness needs root (taps, bridges, namespaces), QEMU and, for speed, KVM (`/dev/kvm`); it
// runs in the nightly workflow on a hosted runner. The pieces that need none of that — image
// download with checksum verification, the cloud-init documents, the QEMU command line, the host
// topology as a list of commands — are plain functions with unit tests that run everywhere.
package appliance
