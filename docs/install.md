# Installation (interim)

How to install Chaos Gateway on a host today, until M28 (container deployment: one `compose.yaml`, the update
script, `teardown`) and M30 (the user guide) replace this page. The host is Ubuntu 24.04 or 26.04, x86-64 or
ARM64, with at least one interface for the uplink and management and one interface per test network
(plan §1.5, §3.8), and with `/usr/lib/systemd/network` (systemd ships it on Ubuntu; the executor mounts it read-only to
read the distribution's systemd-networkd defaults). Everything below runs on that host.

## 1. Build the image

There is no published image yet. Build it from a checkout, with the version, so `chaosgw version` names the
commit:

```sh
git clone https://github.com/Andste82/chaos-gateway.git && cd chaos-gateway
make image-local
docker run --rm chaos-gateway:latest version
```

`make image-local` builds the image for the host's architecture, loads it into the local image store and tags
it `chaos-gateway:latest` and `chaos-gateway:<version>` (the version is `git describe --tags --always --dirty`).
On a Raspberry Pi the build takes 15 to 20 minutes. Without `make`, the same build is:

```sh
docker buildx build --load -f deploy/Dockerfile -t chaos-gateway:latest \
  --build-arg VERSION="$(git describe --tags --always --dirty)" \
  --build-arg COMMIT="$(git rev-parse --short HEAD)" \
  --build-arg DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)" .
```

The compose files use `chaos-gateway:${CHAOSGW_VERSION:-latest}`; `CHAOSGW_VERSION=<version>` runs a tagged
build instead. (`make image` builds both architectures with buildx and keeps no local image; it is for CI. The
published multi-arch image comes with M28.)

## 2. Prepare the host

Once, as root; safe to run again. Name every interface that will carry a test network with `--test-interface`
(here `lan0`; `ip link` lists the names of the host's interfaces):

```sh
docker run --rm --entrypoint cat chaos-gateway:latest /usr/local/share/chaosgw/host-setup.sh > host-setup.sh
sudo sh host-setup.sh --test-interface lan0
```

It installs Docker if missing, loads the kernel modules now and at boot
(`/etc/modules-load.d/chaos-gateway.conf`), enables IPv4 forwarding (`/etc/sysctl.d/90-chaos-gateway.conf`),
keeps systemd-networkd off each test interface (below), prints the netplan changes the test interfaces need and
ends with the check of each test interface. It never touches the uplink or the management configuration, or any
netplan file, and it refuses, before it changes anything, a `--test-interface` that has the default route, an IPv4
address or an SSH session of the host (the one it runs in included): that is the uplink or the management interface named by mistake, which
systemd-networkd would otherwise stop configuring. `sh host-setup.sh --help` lists its options.

### The test interfaces belong to the gateway alone

A test interface must not be configured by the operating system: if it is, the host runs its own DHCP client on
the port and the devices of the test network get no answer from the gateway. Two things to do before the setup
wizard:

1. **Delete the interface's netplan entry.** Ubuntu's installer and cloud-init write one for every port, for
   example in `/etc/netplan/50-cloud-init.yaml`:

   ```yaml
       lan0:
         optional: true
         dhcp4: false
         dhcp6: false
   ```

   Delete the whole entry (an entry that only turns DHCP off still hands the port to the operating system), keep
   the uplink's and the management interface's entries as they are, then run `sudo netplan apply`. Remove a
   NetworkManager connection of the interface too, if there is one (`nmcli connection show`).
2. **Keep systemd-networkd off the interface.** With the netplan entry gone, a default of the distribution may
   take the port: on Ubuntu for the Raspberry Pi, `/run/systemd/network/zzzz-dracut-default.network` runs DHCP on
   every ethernet port no other file names. `--test-interface lan0` writes
   `/etc/systemd/network/10-chaosgw-lan0.network`, which comes before every such default, and reloads
   systemd-networkd:

   ```ini
   [Match]
   Name=lan0

   [Link]
   Unmanaged=yes
   ```

   `networkctl list` then shows `lan0` as `unmanaged`. `sudo sh host-setup.sh --remove-test-interface lan0`
   removes the file again.

Then check, until it says `the test interfaces are ready`:

```sh
sudo sh host-setup.sh --check lan0
```

The check changes nothing. For each interface it reports whether it exists and has a carrier (no carrier is only a
warning: DHCP answers once the cable and the device are there), and names every netplan file and line,
NetworkManager connection and systemd-networkd file that configures it, with the fix. It exits with status 1 while
a step is missing. The setup wizard's preflight shows the same hints for netplan and NetworkManager and for the
systemd-networkd files in `/etc/systemd/network`; it cannot see the ones in `/run` and `/usr/lib` (the gateway
reads only the host's `/etc`), so run the check before the wizard.

## 3. Start the containers

The deployment is five compose files in `deploy/`. Name them once in the environment, so every later
`docker compose` command (logs, exec, down) finds them without repeating `-f`:

```sh
cd deploy
export COMPOSE_PROJECT_NAME=chaos-gateway
export COMPOSE_FILE=compose.executor.yaml:compose.api.yaml:compose.kea.yaml:compose.dns.yaml:compose.bird.yaml
docker compose up -d
docker compose ps
```

The executor starts first (the others wait for its health check). Put the two `export` lines into the shell
profile of the user that operates the gateway, or into `deploy/.env` (compose reads `COMPOSE_FILE` and
`COMPOSE_PROJECT_NAME` from there too).

## 4. Finish the setup

The API prints a one-time setup token to its log at the first start:

```sh
docker compose logs api | grep -A1 'First start'
```

Two things are normal until the setup has been applied, because the link between the gateway and the DNS proxy is
made by the first apply: the `dns` container logs once that it cannot fetch its configuration, and
`GET /api/v1/system/health` reports `degraded` (the DNS proxy has not polled yet; the container's health check
still passes, only `unhealthy` fails it). After the setup both are gone; if not, look at the logs of the `api` and
`dns` containers.

Open `https://<address of the host>:8443` in a browser. The certificate is the gateway's own until one is
installed (M29), so the browser warns once (and once more after an upgrade that issues it again, below). The setup wizard asks for the token, the uplink, the management
interface, the test interfaces and the admin password, and shows the preflight's hints (for example a netplan
entry to remove from a test interface).

Applying the setup configures the host's real interfaces and restricts the UI and API to the management network.
Revision 1 waits for confirmation: if the browser cannot reach the gateway afterwards, it is rolled back when the
countdown ends and the setup opens again, with a new token in the log.

**Security until the setup is done** (plan §2.16): the wizard listens on every address of the host that is not
yet on a test network, and whoever has the setup token can finish the setup and choose the admin password. The
token is in the API container's log, which everyone who may run `docker compose logs` can read (membership in
the `docker` group is as good as root on the host anyway). So run the setup right after the first start, from a
network you trust, and keep the host's port 8443 away from untrusted networks until it is done; do not share the
container logs before. After the setup the UI and API listen on the management network only, and the token is of
no use any more.

## Port forwarding

A device in a test network can be made reachable from behind the uplink, or from the management network
when it has a port of its own: the uplink or management card of the **Networks** page lists the port forwards
and adds them (protocol, external port or range, a device or an address as the target, target port). A
forward answers on the entry interface's addresses (every IPv4 address of it), follows a device's new address by itself, and
goes through preview and apply like every other change. Things to know:

- The access rules apply to forwarded connections, with the device as the responder: a rule "device →
  uplink: drop" also stops connections forwarded to it. Faults do not apply to them yet; the preview says so.
- The gateway refuses the ports it needs itself: the UI/API port (8443 with the compose files above), the
  WireGuard listen ports (udp), 22/tcp on the management side or when uplink and management share one
  interface, and 179/tcp while BGP runs. With uplink and management on one interface, forwards enter on the
  uplink only.
- Docker's published ports live on the same addresses: do not forward a port a container on the host
  publishes, the two would compete for it.
- A forward from the management network answers peers in the management subnet and behind the management
  interface's default gateway only; a peer reached through another route of that interface gets no reply (the
  preview warns when the management interface has no default route).
- The status of each forward says why it is inactive, for example "the device has no address".

## Command line client

`chaosctl` is in the image next to `chaosgw` (`/usr/local/bin/chaosctl`). It talks to the API with a token, so it runs
anywhere that reaches the management network: copy the binary out of the image
(`docker create --name tmp chaos-gateway && docker cp tmp:/usr/local/bin/chaosctl . && docker rm tmp`), or build it with
`go build ./cmd/chaosctl`. Create a token once, with the scope `overlays` (faults, profiles, runs, captures; enough for a CI job):

```sh
GW=https://gateway.example:8443
curl -sk -c jar -H 'Content-Type: application/json' -d '{"password":"<admin password>"}' $GW/api/v1/auth/login | jq -r .csrf_token > csrf
curl -sk -b jar -H "X-CSRF-Token: $(cat csrf)" -H 'Content-Type: application/json' \
     -d '{"name":"ci","scope":"overlays"}' $GW/api/v1/auth/tokens | jq -r .token > token
```

The token is shown once; keep it in the CI system's secrets. The gateway's certificate is self-signed until M29: give the
client its SHA-256 fingerprint (`--fingerprint`, `CHAOSGW_FINGERPRINT`), the certificate (`--ca-cert`), or `--insecure`.

```sh
export CHAOSGW_URL=$GW CHAOSGW_TOKEN_FILE=token
chaosctl wait device esp32-42 --online --connected tcp/8883 --timeout 90s
chaosctl run -f mqtt-outage.yaml --set target.device=esp32-42 --wait --junit report.xml --artifacts out/
chaosctl with --device esp32-42 --profile bad-lte --ttl 10m -- pytest tests/
```

`chaosctl help` lists the commands. A run that is waited for has a lease of 30 s that the client renews, and the overlays of
`chaosctl with` have one too, so a CI job that is killed leaves no fault on the lab: the gateway removes it. The exit codes
(0 passed, 1 a check failed, 2 error or aborted) and the example suites for pytest and Jest are in
[`clients/examples`](../clients/examples/README.md).

The record, the report and the events of an ended run are kept for 30 days by default and then deleted, together with the
captures of the run. `settings.retention.runs` of the active configuration (a duration such as `2160h` for 90 days, 1 hour
at least) sets it and takes effect at the next run that ends, also for the runs that ended before. The flag
`--run-retention` of `chaosgw api` is only the default for a configuration that does not set it.

## Packet captures

Captures (`POST /api/v1/captures`, `capture: true` and the `capture` steps of a scenario) are recorded by the executor
and kept in the volume `chaosgw-captures`, which the executor and the API container share (`--captures-dir
/var/lib/chaosgw/captures` in both compose files). The image gives the directory to the API's user, so the volume
starts with an owner that can make the directory of a capture. A volume of that name that exists already with another
owner is given to the API's user the way the upgrade notes below do it for the other volumes.

- A capture takes at most the **quota**, 5 GiB by default (`settings.retention.captures_quota` of the configuration,
  or `--capture-quota` of `chaosgw api` when the configuration names none). The oldest stopped captures are deleted
  first when a new capture needs the room.
- Below **10 % free space** of the volume new captures are refused and running ones stop (`stop_reason: disk_low`,
  the event `disk_low`); a run that records goes on and says so in its report (`capture_missing`).
  `--capture-min-free` of the executor and of the API changes the limit.
- Put the volume on a disk with room for it; a capture of a busy 1 Gbit/s link is 100 MB a second. A ring buffer
  (`ring_buffer`) bounds a capture by the size of its files instead.
- A capture of a local network is taken on the gateway's bridge (`br-<network>`): it holds what the gateway sends and
  receives, in particular everything a device exchanges with the uplink or another network, before NAT. Frames the
  bridge forwards between two devices of the same network never reach the bridge device and are not in it.

## Troubleshooting: DHCP does not answer

A device on a test network gets no address. In this order:

1. **The operating system leaves the port alone.** `sudo sh host-setup.sh --check lan0` (step 2) must say `the
   test interfaces are ready`. `networkctl list` must show the port as `unmanaged`; `configuring`,
   `configured` or `enslaved` mean systemd-networkd still runs DHCP on it: write the rule
   (`--test-interface lan0`) and run `sudo networkctl reload`.
2. **The port has a link and is in the network's bridge.** `ip link show lan0` shows `state UP` and
   `master br-<network>` (the bridge of the network; the Linux view of the network in the UI names it).
3. **Kea listens on the bridge.** Kea opens its packet socket on the bridge only while the bridge has a carrier,
   that is while one of its ports is up. The gateway configures it again whenever the bridge comes up or its
   carrier went and came back, so a port that flapped needs nothing. To see it: the bridge's index
   (`cat /sys/class/net/br-<network>/ifindex`) must be among the interface indexes of the packet sockets
   (`sudo awk 'NR > 1 {print $5}' /proc/net/packet`). `docker compose logs kea` names an interface Kea skipped
   ("is not running"). If the index is missing although the port is up, `docker compose restart kea` restores
   it; please report that case.
4. **The device asks again.** A device that gave up after its first tries waits a long time before the next:
   renew its lease (`sudo dhclient -r eth0 && sudo dhclient eth0`, `ipconfig /renew`, or unplug and plug the
   cable).
5. **Capture on the port, not on the bridge.** `sudo tcpdump -eni lan0 port 67 or port 68` shows the device's
   requests and the gateway's answers on the wire. Frames the bridge forwards between its ports are not seen on
   the bridge device, so a capture on `br-<network>` can miss what the device sends or receives.

## Upgrading an installation made before issue #64 was fixed

The gateway's own self-signed certificate is now also valid for the service address 169.254.100.1, which the DNS proxy
verifies it for. An older one lacks that address, so the API issues the certificate again at the first start of the new
image, and the browser warns once more. A certificate you installed yourself is not changed; it has to include
169.254.100.1 itself (installing one comes with M29).

## Upgrading an installation made before issue #57 was fixed

Images built before the fix of issue #57 created the API's volumes owned by root, and the API container (user
65532) cannot start (`mkdir /var/lib/chaosgw/secrets/wireguard: permission denied`). A rebuilt image does not
change volumes that already exist. With no configuration applied yet, remove them and start again
(`docker compose down -v`, then step 3). To keep them instead, give them to the API's user:

```sh
for v in state secrets api service; do
  docker run --rm -v "${COMPOSE_PROJECT_NAME}_chaosgw-$v:/v" --entrypoint chown chaos-gateway:latest -R 65532:65532 /v
done
docker run --rm -v "${COMPOSE_PROJECT_NAME}_chaosgw-secrets:/v" --entrypoint chmod chaos-gateway:latest 0700 /v
```

(A stack started from `deploy/` without `COMPOSE_PROJECT_NAME` has the project name `deploy`.)

## Uninstall

`chaosgw teardown` (plan §3.8), which removes everything the gateway created in the kernel, comes with M27. Until
then:

```sh
docker compose down -v
sudo sh host-setup.sh --remove-test-interface lan0
sudo rm -f /etc/modules-load.d/chaos-gateway.conf /etc/sysctl.d/90-chaos-gateway.conf
sudo reboot
```

Stopping the executor removes the active faults; the bridges, nftables tables, routes and WireGuard interfaces of
the last configuration stay until the reboot, which also unloads the modules and resets IPv4 forwarding.
`--remove-test-interface` removes the systemd-networkd rule of a test interface (once per interface). Undo by
hand what you changed in netplan for the test interfaces.

If `docker compose down -v` fails with `device or resource busy` on the `chaosgw-run` volume, a mount of the
executor has leaked into the host (issue #57, F4). Unmount it, then run `down -v` again:

```sh
grep chaosgw /proc/self/mountinfo        # the leaked mount ends in .../_data/bird
sudo umount /var/lib/docker/volumes/${COMPOSE_PROJECT_NAME}_chaosgw-run/_data/bird
```
