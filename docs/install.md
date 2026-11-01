# Installation

How to install Chaos Gateway on a host, update it and take it away again. After the installation read the [user guide](user-guide.md);
when something goes wrong, [troubleshooting](troubleshooting.md) and [recovery](recovery.md). The host is Ubuntu 24.04 or 26.04, x86-64 or ARM64
(a Raspberry Pi 4 or 5 included, not validated on hardware yet, plan H1), with at least one interface for the uplink and management and one
interface per test network (plan §1.5, §3.8), and with `/usr/lib/systemd/network` (systemd ships it on Ubuntu; the executor mounts it
read-only to read the distribution's systemd-networkd defaults). Everything below runs on that host. The gateway is one image,
`ghcr.io/andste82/chaos-gateway`, and one `compose.yaml` that starts its containers (plan §3.8); the host setup and the update script
come out of the image.

## Quick start

One command installs the gateway: it installs Docker if it is missing, pulls the image, prepares the host (modules, forwarding, the data
directories, the rule that keeps systemd-networkd off the test interfaces), starts the containers and waits until they are healthy.
Name every interface that will carry a test network with `--test-interface` (`ip link` lists the names; never the uplink or the
management interface, see [the test interfaces](#the-test-interfaces-belong-to-the-gateway-alone)):

```sh host
curl -fsSL https://github.com/Andste82/chaos-gateway/releases/latest/download/install.sh | sudo sh -s -- --test-interface lan0
```

A particular version is `--version 1.0.0` (`latest` is the newest release that is not a pre-release; a release candidate is
`--version 1.0.0-rc.1`). If you would rather read the script before it runs as root, or check it against the checksums the release carries:

```sh host
V=1.0.0
curl -fsSLO "https://github.com/Andste82/chaos-gateway/releases/download/v$V/install.sh"
curl -fsSLO "https://github.com/Andste82/chaos-gateway/releases/download/v$V/SHA256SUMS"
sha256sum --ignore-missing -c SHA256SUMS      # install.sh: OK
less install.sh
sudo sh install.sh --version "$V" --test-interface lan0
```

The script can be run again whenever it stopped: every step checks what is there. It stops, with the reason, when the host setup finds a
step missing, most often a test interface that the operating system still configures (a netplan entry); do what it says and run it again.
When it is done it prints where the setup wizard is (`https://<management address of this host>:8443`) and the command that shows the
one-time setup token. `sh install.sh --help` lists the options: `--test-interface` (repeatable), `--version`, `--image` (a full reference,
for a local build or a mirror), `--dir` (default `/opt/chaosgw`), `--no-pull` and `--no-start`.

The two other scripts are in `/opt/chaosgw` afterwards:

```sh host
sudo /opt/chaosgw/update.sh --token-file ~/chaosgw.token     # later: the new version, when no run is active (see "Updating")
sudo /opt/chaosgw/uninstall.sh                                # take the gateway away, keep the data (see "Uninstall")
sudo /opt/chaosgw/uninstall.sh --purge                        # and delete the data, the files and the images
```

The sections below are what `install.sh` does, one step at a time, for a host you want to prepare by hand.

## 1. Get the image

Docker must be there to take the files out of the image. On a host without it, download `host-setup.sh` from the release (the files of a
release are attached to it: `compose.yaml`, `host-setup.sh`, `install.sh`, `update.sh`, `uninstall.sh`, `env.example`, `SHA256SUMS`), run it
without `--image` (it installs Docker), then set `IMAGE` and pull it as below, and continue with step 2. (`install.sh` does all of this.)

```sh host
IMAGE=ghcr.io/andste82/chaos-gateway:1.0.0     # a version; :latest is the newest release
docker pull "$IMAGE"
docker run --rm "$IMAGE" version
```

An image is named by tag: `host-setup.sh --image` does not take a reference by digest (`@sha256:`), because `.env` holds `CHAOSGW_IMAGE`
and `CHAOSGW_VERSION`. To pin one exact build, pull it, give it a local tag (`docker tag`) and name that.

To build the image from a checkout instead, with the version, so `chaosgw version` names the commit:

```sh host
git clone https://github.com/Andste82/chaos-gateway.git && cd chaos-gateway
make image-local
IMAGE=chaos-gateway:latest
```

`make image-local` builds the image for the host's architecture, loads it into the local image store and tags it
`chaos-gateway:latest` and `chaos-gateway:<version>` (the version is `git describe --tags --always --dirty`). On a Raspberry Pi the build
takes 15 to 20 minutes. Without `make`, the same build is `docker buildx build --load -f deploy/Dockerfile -t chaos-gateway:latest
--build-arg VERSION="$(git describe --tags --always --dirty)" .`. (`make image` builds both architectures with buildx and keeps no local
image; it is for CI. The release workflow, `.github/workflows/image.yml`, pushes the multi-arch image.) The image holds the gateway and
everything it runs: `chaosgw` and `chaosctl`, the tools of the executor, Kea, BIRD, the interception proxy and `tcpdump`.

## 2. Prepare the host

Once, as root; safe to run again. Name every interface that will carry a test network with `--test-interface`
(here `lan0`; `ip link` lists the names of the host's interfaces):

```sh host
docker run --rm --entrypoint cat "$IMAGE" /usr/local/share/chaosgw/host-setup.sh | sudo sh -s -- --image "$IMAGE" --test-interface lan0
```

It installs Docker if missing, loads the kernel modules now and at boot
(`/etc/modules-load.d/chaos-gateway.conf`), enables IPv4 forwarding and the unprivileged ICMP socket of the ping of the diagnostics
(`net.ipv4.ping_group_range`, M24) in `/etc/sysctl.d/90-chaos-gateway.conf`, makes the directories of the gateway's data (below), copies
`compose.yaml`, `update.sh`, `host-setup.sh` and `env.example` out of the image into `/opt/chaosgw` (`--install-dir` changes it) and writes
`/opt/chaosgw/.env`, which names the image and its version (an existing `.env` is kept), keeps systemd-networkd off each test interface
(below), prints the netplan changes the test interfaces need and ends with the check of each test interface. It never touches the uplink
or the management configuration, or any netplan file, and it refuses, before it changes anything, a `--test-interface` that has the
default route, an IPv4 address or an SSH session of the host (the one it runs in included): that is the uplink or the management interface
named by mistake, which systemd-networkd would otherwise stop configuring. `sh host-setup.sh --help` lists its options. The files in
`/opt/chaosgw` are the ones below; run the other commands of this guide from there.

**The data of the gateway** is in bind mounts of directories of the host (plan §3.6; named volumes before M28), which the host setup makes
with the owners the containers need: `/var/lib/chaosgw/state` (the revisions), `secrets` (WireGuard keys, credentials, the HTTPS key: mode
0700), `api` (the audit log and the runs), `service` (the token of the service containers), `tlsca` (the two CAs of the TLS cases),
`captures`, `exec` (the interfaces assigned to the executor) and `kea` (the leases), all owned by user 65532 (the API container's) but the
last two, which are root's. Back them up with the gateway stopped: they are the whole state. Docker never makes them (`create_host_path` is
off): a missing directory is an error that names it, not a directory of root's that the API could not write. The sockets between the
containers are in named volumes, which carry no state.

### The test interfaces belong to the gateway alone

A test interface must not be configured by the operating system: if it is, the host runs its own DHCP client on
the port and the devices of the test network get no answer from the gateway. Two things to do before the setup
wizard:

1. **Delete the interface's netplan entry.** Ubuntu's installer and cloud-init write one for every port, for
   example in `/etc/netplan/50-cloud-init.yaml`:

   ```yaml fragment
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

   ```ini fragment
   [Match]
   Name=lan0

   [Link]
   Unmanaged=yes
   ```

   `networkctl list` then shows `lan0` as `unmanaged`. `sudo sh host-setup.sh --remove-test-interface lan0`
   removes the file again.

Then check, until it says `the test interfaces are ready`:

```sh host
sudo sh host-setup.sh --check lan0
```

The check changes nothing. For each interface it reports whether it exists and has a carrier (no carrier is only a
warning: DHCP answers once the cable and the device are there), and names every netplan file and line,
NetworkManager connection and systemd-networkd file that configures it, with the fix. It exits with status 1 while
a step is missing. The setup wizard's preflight shows the same hints for netplan and NetworkManager and for the
systemd-networkd files in `/etc/systemd/network`; it cannot see the ones in `/run` and `/usr/lib` (the gateway
reads only the host's `/etc`), so run the check before the wizard.


## 3. Start the containers

```sh host
cd /opt/chaosgw
docker compose up -d
docker compose ps
```

`compose.yaml` holds all the containers (made from the files of `deploy/` by `tools/composegen`): the executor (the only privileged one),
the API, Kea, BIRD, the holder of the service namespace and the DNS proxy and TLS responder that join it, and, under the profile
`interception`, the interception proxy. They start in the order of plan §3.8: the executor first (the others wait for its health check),
then the API, the holder and Kea, then the services that join the service namespace once the API is healthy. Every container restarts
after a reboot (`restart: unless-stopped`, Docker starts at boot). The name of the project is `chaosgw`, whatever the directory is called.
The settings of an installation (the image and its version, the port of the UI, the profiles) are in `.env`; `env.example` lists them.

The TLS responder is the one of the certificate test cases (plan §2.8): without it the health check lists the component `tls` as
degraded and a TLS case has nothing to answer the device. The API makes the two CAs of the cases in `/var/lib/chaosgw/tlsca` at its
start; the certificate to install on a development firmware is `GET /api/v1/tls/ca`.

The **interception proxy** is the optional container for the TLS case `intercept` (plan §2.8, M22): mitmproxy with the
gateway's addon, in the service namespace behind the TLS responder. It starts with the profile `interception`
(`COMPOSE_PROFILES=interception` in `.env`, then `docker compose up -d`) when you want to inspect and change the HTTP(S) traffic of
a device that trusts the test CA. Without it the other cases work, and a connection that an interception case selects is
reset (the health check lists the component `tls_proxy` as degraded while such a case is in force). The proxy checks the
certificates of the servers it connects to against the bundle of mitmproxy: set `CHAOSGW_UPSTREAM_CA_FILE` (a CA file that you
mount into the container, below) for servers with a private CA, or `CHAOSGW_UPSTREAM_INSECURE=1` to verify nothing. These
two settings are the installation's default; a case can say otherwise for its own devices with `intercept.upstream`: `verify`
(the bundle, whatever the installation says), `own_ca` (the CA file of the installation and no other CA; a case that asks for it
where the installation has none is refused, with the reason in the log of the exchanges) or `insecure` (no check of the server's
certificate). A server that is refused answers the device with a 502 page over the proxy's certificate. The test
CA can be replaced (`POST /api/v1/tls/ca/regenerate`, scope `full`); every device that installed the old certificate then has to
install the new one. Captures that run while a device talks through the proxy get the TLS secrets of its connections as a key log
(`GET /api/v1/captures/{id}/download?part=keylog`, scope `full`): the capture and its key log together show the traffic in
Wireshark, so such a capture is marked sensitive. The image of the proxy is built for ARM64 too, but the proxy is not validated
on ARM64 hardware (a Raspberry Pi) yet: its load there is measured with the hardware lab (plan H1). The web app has the **Interception** screen for all of this (the cases and their HTTP rules,
the exchanges the proxy saw, the download and the replacement of the test CA), and `chaosctl` has `intercept set`, `intercept flows` and
`tls ca` ("Command line client" below).


The container of the proxy is read-only and mounts only its two directories, so a CA file of your own needs a mount. Put the file on
the host (PEM, one or more CA certificates, readable by the proxy's user 65532, for example mode `0644`) and add a compose file of your
own next to `compose.yaml`; Compose reads `compose.override.yaml` by itself:

```yaml fragment
# /opt/chaosgw/compose.override.yaml
services:
  tls-proxy:
    environment:
      CHAOSGW_UPSTREAM_CA_FILE: /etc/chaosgw/upstream-ca.pem
    volumes:
      - type: bind
        source: /etc/chaos-gateway/upstream-ca.pem  # the file on the host
        target: /etc/chaosgw/upstream-ca.pem
        read_only: true
```

```sh host
COMPOSE_PROFILES=interception docker compose up -d tls-proxy
docker compose logs tls-proxy | grep 'CA file'   # nothing: the file is readable; a warning names what is wrong
```

The variable is the path inside the container. A path that the container cannot read gives a proxy that starts and looks healthy,
while every connection that is verified against the file fails (the device gets a 502 page); the proxy says so in its log at the
start. Like the DNS proxy and the TLS responder, the proxy leaves the service namespace when it loses the service address (after a
restart of the `svcns` container): it exits with code 3 and its restart policy brings it into the new namespace.

## 4. Finish the setup

The API prints a one-time setup token to its log at the first start:

```sh host
docker compose logs api | grep 'Setup token'
```

Two things are normal until the setup has been applied, because the link between the gateway and the DNS proxy is
made by the first apply: the `dns` container logs once that it cannot fetch its configuration, and
`GET /api/v1/system/health` reports `degraded` (the DNS proxy has not polled yet; the container's health check
still passes, only `unhealthy` fails it). After the setup both are gone; if not, look at the logs of the `api` and
`dns` containers.

Open `https://<address of the host>:8443` in a browser. The certificate is the gateway's own until you install one
([HTTPS certificate](#https-certificate)), so the browser warns once (and once more after an upgrade that issues it again, below). The setup wizard asks for the token, the uplink, the management
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

## HTTPS certificate

The web UI and the API are served over HTTPS with a self-signed certificate that the gateway makes at its first start (valid for ten years, for
the host name, the addresses of the host, the loopback and the link to the service containers). Replace it with one your browsers and tools trust:

```sh host
chaosctl certificate show                                           # the one in use, its names, validity and SHA-256 fingerprint
chaosctl certificate install --cert fullchain.pem --key privkey.pem  # scope full
chaosctl certificate reset                                          # back to the gateway's own
```

`fullchain.pem` is the certificate first, then the intermediates (PEM); the key is PEM without a password (PKCS#8, PKCS#1 RSA or SEC1 EC; an RSA key needs at
least 2048 bits). The gateway refuses a certificate that has expired or is not valid yet (any certificate of the chain, an intermediate too), that names no DNS name and no IP address, that does not allow a
server (`serverAuth`), whose chain is not in order or whose key does not belong to it, and says which. It takes effect for the next connection, without
a restart; connections that are open keep the old one. The key is stored in the secrets directory (`/var/lib/chaosgw/secrets/tls/ui.pem`, mode 0600) and is
never returned. The API behind the commands is `GET`, `PUT` and `DELETE /api/v1/system/certificate` (`PUT` and `DELETE` need the scope `full` and are audit-logged as `certificate.install` and
`certificate.reset`).

The certificate must name the address or the name you reach the gateway by (the management address, or a DNS name that points at it). It does not need to name
`127.0.0.1` or `169.254.100.1`: on those two addresses the gateway always presents its own certificate, which the DNS proxy, the TLS responder and Kea's
hook verify (they get a copy of it, never of the key), so replacing the certificate cannot cut them off. `chaosgw api --health` (the container's health check)
talks to the loopback and is unaffected, too.

If a certificate you installed is damaged on disk (a half-written file after a power cut), the gateway starts with its own and logs why; remove the file with
`chaosctl certificate reset`, or delete `ui.pem`.

`chaosctl certificate show` and `GET /api/v1/system/certificate` describe the certificate of the management addresses. On the loopback the gateway still
presents its own, so a `chaosctl` that runs on the host with its default URL (`https://127.0.0.1:8443`) sees the self-signed certificate even after an
install: pin that one there (`--insecure` is fine on the loopback of the host), or give `chaosctl` the management address or name of the installed certificate
with `--url`. The statement above that a CA you trust makes `--fingerprint`, `--ca-cert` and `--insecure` unnecessary holds for those addresses only.

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
`go build ./cmd/chaosctl`. Create a token once, with the scope `overlays` (faults, profiles, runs, captures; enough for a CI job; `chaosctl probe add` and `probe remove` change the configuration and need the scope `full`):

```sh host
GW=https://gateway.example:8443
curl -sk -c jar -H 'Content-Type: application/json' -d '{"password":"<admin password>"}' $GW/api/v1/auth/login | jq -r .csrf_token > csrf
curl -sk -b jar -H "X-CSRF-Token: $(cat csrf)" -H 'Content-Type: application/json' \
     -d '{"name":"ci","scope":"overlays"}' $GW/api/v1/auth/tokens | jq -r .token > token
```

The token is shown once; keep it in the CI system's secrets. The gateway's certificate is self-signed until you install one
([HTTPS certificate](#https-certificate)): give the client its SHA-256 fingerprint (`--fingerprint`, `CHAOSGW_FINGERPRINT`; `chaosctl certificate show`
prints it), the certificate (`--ca-cert`), or `--insecure`. With a certificate that a CA you trust has signed, none of the three is needed.
These three are one setting: a flag on the command line replaces the `CHAOSGW_*` variables of the same setting as a group
(a `CHAOSGW_INSECURE=1` in the shell does not weaken a `--fingerprint`), `--insecure` together with a fingerprint or a CA file
is refused (exit 2), and `--insecure` prints a warning.

```sh host
export CHAOSGW_URL=$GW CHAOSGW_TOKEN_FILE=token
chaosctl wait device esp32-42 --online --connected tcp/8883 --timeout 90s
chaosctl run -f mqtt-outage.yaml --set target.device=esp32-42 --wait --junit report.xml --artifacts out/
chaosctl with --device esp32-42 --profile bad-lte --ttl 10m -- pytest tests/
chaosctl explain --device esp32-42 --dst mqtt.example.com --protocol tcp --port 8883   # why is it refused or slow?
chaosctl tls ca --out test-ca.pem          # the test CA a development firmware (and the interception) trusts
chaosctl intercept set --device esp32-42 --rule 'host=api.example.com;path=/v1/;status=503' --ttl 5m
chaosctl intercept flows --device esp32-42 --faulted   # what the interception proxy saw, and the rules that acted
chaosctl probe add ci-probe --network IoT              # a virtual client in a test network (scope full)
chaosctl wait device ci-probe --online --timeout 60s   # until its DHCP client got a lease
chaosctl probe list                                    # name, network, address, MAC, state
chaosctl probe remove ci-probe                         # --force also removes its overlays and aborts its runs (scope full)
```

`chaosctl intercept set` needs the interception proxy of `compose.tlsproxy.yaml` (section 3) and a device that trusts the test CA;
`chaosctl tls ca regenerate --yes` replaces the test CA and needs a token with the scope `full`. The Interception screen of the web app does the same
(the cases, the table of the exchanges, the CA buttons). `chaosctl help` lists the commands. A run that is waited for has a lease of 30 s that the client renews, and the overlays of
`chaosctl with` have one too, so a CI job that is killed leaves no fault on the lab: the gateway removes it. The exit codes
(0 passed, 1 a check failed, 2 error or aborted) and the example suites for pytest and Jest are in
[`clients/examples`](../clients/examples/README.md).

The record, the report and the events of an ended run are kept for 30 days by default and then deleted, together with the
captures of the run. `settings.retention.runs` of the active configuration (a duration such as `2160h` for 90 days, 1 hour
at least) sets it and takes effect at the next run that ends, also for the runs that ended before. The flag
`--run-retention` of `chaosgw api` is only the default for a configuration that does not set it.

## Metrics

`GET /api/v1/metrics` answers in the Prometheus text format with the counters of the faults, rules and port forwards, the statistics of the netem queues and
of the interfaces the gateway applied, the devices and the health of the system (the families are listed in the spec under `/metrics`). It needs a token of the
scope `read`, made like the one above with `"scope":"read"`. A scrape configuration:

```yaml fragment
scrape_configs:
  - job_name: chaos-gateway
    scheme: https
    metrics_path: /api/v1/metrics
    authorization: {credentials_file: /etc/prometheus/chaosgw-token}
    tls_config: {insecure_skip_verify: true}   # until the gateway has a certificate of its own (see "HTTPS certificate"); then ca_file: or the default roots
    static_configs: [{targets: ['gateway.example:8443']}]
```

A source the gateway could not read at a scrape is left out of the answer, and `chaosgw_collector_up{source="counters"}` (also `queues`, `interfaces`) is 0 then, also when the read hangs (each source is read within 3 seconds):
alert on that, not on a counter that stopped. The Flows screen of the web app lists the connections the gateway tracks, and a device's page shows its connections,
its recent DNS queries and the path to a destination with the faults that act on it.

## Packet captures

Captures (`POST /api/v1/captures`, `capture: true` and the `capture` steps of a scenario) are recorded by the executor
and kept in the directory `/var/lib/chaosgw/captures` of the host, which the executor and the API container share (`--captures-dir
/var/lib/chaosgw/captures` in both). The host setup gives the directory to the API's user, so that it can make the directory of a
capture (the executor writes the packet files into it); the preflight of the executor reports a directory of another owner.

- A capture takes at most the **quota**, 5 GiB by default (`settings.retention.captures_quota` of the configuration,
  or `--capture-quota` of `chaosgw api` when the configuration names none). The oldest stopped captures are deleted
  first when a new capture needs the room.
- Below **10 % free space** of the disk new captures are refused and running ones stop (`stop_reason: disk_low`,
  the event `disk_low`); a run that records goes on and says so in its report (`capture_missing`).
  `--capture-min-free` of the executor and of the API changes the limit.
- Put `/var/lib/chaosgw/captures` on a disk with room for it (a mount of its own is the way); a capture of a busy 1 Gbit/s link is 100 MB a second. A ring buffer
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

## Diagnostics: the ping needs a sysctl of the host

The ping of the diagnostics (M24, `POST /diagnostics` and the Diagnostics screen) runs in the unprivileged API container, which shares the
network namespace of the host. It opens the ICMP datagram socket that Linux gives a group listed in `net.ipv4.ping_group_range`, so the value of the
host counts. The host setup writes `net.ipv4.ping_group_range=0 2147483647` into `/etc/sysctl.d/90-chaos-gateway.conf` and sets it. On a host that was set up
before M24, run the host setup again (it is safe to run again), or set the sysctl by hand (`sudo sysctl -w net.ipv4.ping_group_range="0 2147483647"` and the
same line in `/etc/sysctl.d/`); until then a ping answers `cannot open an ICMP socket …` and the other diagnostics work.

## Updating

`/opt/chaosgw/update.sh` pulls the image and restarts the containers when no run is active (plan §3.9). The shortest way, once you have a
token with the scope `read` in a file (made from the admin session with `POST /api/v1/auth/tokens`, see the
[API guide](api-guide.md#tokens-and-scopes); the script sends it only to the API of this host):

```sh host
sudo /opt/chaosgw/update.sh --token-file ~/chaosgw.token --version 1.1.0
```

Without a token the script refuses to guess whether a run is active and says how to give it one; `--no-wait` updates without asking (a
run that is active then ends as `aborted`). The options in full:

```sh host
cd /opt/chaosgw
./update.sh --token-file ~/chaosgw.token              # the image of .env (CHAOSGW_VERSION), pulled again
./update.sh --version 1.1.0 --token-file ~/chaosgw.token
./update.sh --check --token-file ~/chaosgw.token      # everything but the restart
```

It checks, before it stops anything, that the new image can read the configuration (`chaosgw schema-check`): a configuration that a
newer version wrote is refused with the version that is needed (exit 4), and the gateway keeps running. Then it waits, up to
`--timeout` seconds (600), until `GET /api/v1/system/busy` reports no queued or running run, because an update ends active runs as
`aborted`; when the time is up it stops (exit 3), and `--force` updates anyway. The question needs a token with the scope `read` (the web
UI has no screen for tokens: make one with `POST /api/v1/auth/tokens`, see the [API guide](api-guide.md#tokens-and-scopes)): `--token-file FILE` or the variable `CHAOSGW_TOKEN`; the token is sent only to an API that shows the certificate
it published in `/var/lib/chaosgw/service/api.pem`, never to whatever else listens on the port. Where nothing listens, the gateway is down and has no run; an API that
listens and does not answer in time may have one, so the update stops (`--force` goes on). Then all containers are replaced together (the executor protocol
version rejects a set of two versions), and the script waits for their health checks and for the API's own health (exit 5 with the way
back when they do not come; the DNS proxy and the TLS responder cannot be healthy before the first configuration, so a gateway that is not
set up yet is not waited for with them). The flags, mounts, capabilities and health checks of the containers belong to the version, so the
script also reads `compose.yaml`, `env.example` and `update.sh` out of the new image, checks that compose file with your `.env`, and
replaces the installed one just before the restart (the old one stays as `compose.yaml.bak`; `.env` is not touched, except that `--version V` writes `CHAOSGW_VERSION` into it after a healthy start). The update script
the image carries is used from the next update on. A signal (Ctrl-C) during the wait ends the script without a restart. **The update stops the executor like any stop** (plan §3.8, D35): it removes the test networks, so the
devices lose the gateway and their DHCP for the seconds to a minute the new executor needs to rebuild them from the confirmed revision;
the leases are kept (`/var/lib/chaosgw/kea`), and a device gets the same address again. Going back is the same command with the old
version, as long as the configuration has not been migrated to a newer schema.

By hand it is `docker compose pull && docker compose up -d`, without the checks.

Every update leaves the previous image on the host: it is the way back, and an image is over 1 GB, which matters on the SD card of a
Raspberry Pi. `docker images ghcr.io/andste82/chaos-gateway` lists them and `docker image rm IMAGE_ID` removes one you no longer need; never
remove the one the containers use.

## Troubleshooting: the executor's preflight

At every start the executor checks the host it runs on and writes the result into its log (`docker compose logs exec | grep preflight`:
`INFO` ok, `WARN` a warning with its fix, `ERROR` a failure with its fix); `GET /api/v1/system/preflight` and the setup wizard show the
same lines, next to the kernel, the modules and the check of each test interface that the API makes. It does not stop the executor: a
failed check is a thing to fix, the others go on working.

| Check | What it looks at | Fix |
|---|---|---|
| `host.tools` | `ip`, `tc`, `nft`, `ethtool`, `conntrack`, `iptables`, `wg`, `bird` are in the image | pull the image again |
| `host.iptables` | the nf_tables backend of `iptables` (Docker's `DOCKER-USER` chain is there) | `update-alternatives --set iptables /usr/sbin/iptables-nft` on the host, restart Docker |
| `host.modules` | the required kernel modules; those that exist and are not loaded are loaded | `linux-modules-extra-$(uname -r)` for a kernel without them; the host setup loads them at boot |
| `host.ip_forward` | `net.ipv4.ip_forward` (the first apply switches it on) | the host setup makes it permanent |
| `host.ping_group_range` | the unprivileged ICMP socket of the ping of the diagnostics | the host setup |
| `host.proc_sys` | `/proc/sys` is writable in the executor's container | the executor container is privileged (`compose.yaml`) |
| `host.netns` | a network namespace can be made, and `/run/netns` is shared with the host (`rshared`) | the mounts of the executor in `compose.yaml` |
| `host.firewall` | no other firewall (`ufw`, `firewalld`, an `nft` table of your own) whose drop verdicts would hit test traffic | allow the traffic of the test networks there, or stop it |
| `host.data.<dir>` | the directories below `/var/lib/chaosgw` exist, belong to the API's user (the secrets: mode 0700) | `sudo sh /opt/chaosgw/host-setup.sh --data-only` |

## Moving from named volumes (an installation before M28)

Until M28 the data was in Docker volumes (`<project>_chaosgw-state` and so on). To move it into the directories of the host with the
gateway stopped (`docker compose down`, with the old compose files), make the directories and copy:

```sh host
sudo sh /opt/chaosgw/host-setup.sh --data-only
for v in state secrets api service captures tlsca; do
  docker run --rm --user 0:0 -v "${COMPOSE_PROJECT_NAME:-chaos-gateway}_chaosgw-$v:/from:ro" -v "/var/lib/chaosgw/$v:/to" --entrypoint cp "$IMAGE" -a /from/. /to/
done
docker run --rm --user 0:0 -v "${COMPOSE_PROJECT_NAME:-chaos-gateway}_chaosgw-exec-state:/from:ro" -v /var/lib/chaosgw/exec:/to --entrypoint cp "$IMAGE" -a /from/. /to/
docker run --rm --user 0:0 -v "${COMPOSE_PROJECT_NAME:-chaos-gateway}_chaosgw-kea-state:/from:ro" -v /var/lib/chaosgw/kea:/to --entrypoint cp "$IMAGE" -a /from/. /to/
sudo sh /opt/chaosgw/host-setup.sh --data-only      # the owner and mode of the directories themselves
cd /opt/chaosgw && docker compose up -d
```

Then `docker volume rm` the old volumes. (A stack started from `deploy/` without a project name had the project name `deploy`.)

## Upgrading an installation made before issue #64 was fixed

The gateway's own self-signed certificate is now also valid for the service address 169.254.100.1, which the DNS proxy
verifies it for. An older one lacks that address, so the API issues the certificate again at the first start of the new
image, and the browser warns once more. A certificate you installed yourself is not changed; it has to include
169.254.100.1 itself, or you install it with `PUT /api/v1/system/certificate` (see "HTTPS certificate"), which does not have to name that address: the
service containers keep verifying the gateway's own certificate, which is presented on that address and on the loopback.

## Stop, restart and recovery

Stopping the gateway (`docker compose stop`, which stops the API container before the executor) removes everything it created
in the kernel (plan §3.8, D35): the overlays and the runs first, then the `inet chaosgw` table, its policy rules and routes,
the test bridges and their addresses, the service pair and the name `cgsvc`, the probes with their namespaces and DHCP clients,
the tc trees, the rules in `DOCKER-USER` and the WireGuard interfaces. Nothing of it changes the host's or the devices'
traffic afterwards; the devices of a test network have no gateway until the next `docker compose up`, which builds the
state again from the last confirmed revision. The revisions, the secrets, the audit log and the captures stay in `/var/lib/chaosgw`.

If the executor was killed (out of memory, `docker kill`, a power loss) it could not clean up. With the stack stopped, remove what it left:

```sh host
docker compose --profile teardown run --rm teardown
```

The `teardown` service mounts every data directory writable, and `--purge` empties them all: the revisions, the secrets, the audit log, the
service token, the TLS CAs, the captures, Kea's leases and the executor's state (irreversible; the directories stay, with their owners).
It refuses while an executor is serving, and `--purge` empties the directories only once the kernel is clean: when the teardown reports
what it could not remove (it exits with 1), the directories stay, because a second run needs the executor's state in them; run it again
after the cause is gone.

At the start the gateway applies the active revision. If that fails (it is tried twice) or its file is damaged, it applies the
last revision that worked and makes that the active one; if that fails too, or there is none, it runs in **safe mode**: management
access only, no forwarding for the test networks (a WireGuard network of the role `management` is down too: the way in is the management
interface, not a tunnel), and everything that needs forwarding answers `503`. `GET /api/v1/state` reports
`safe_mode` and `safe_mode_reason`, and the web app shows a red banner that links to the [recovery instructions](recovery.md), which
say what to look at and how to leave safe mode, and also cover the teardown, a forgotten password and an update that went wrong. If not a
single revision can be read, the gateway has no configuration to take the management interface from, and the API listens on the host's
loopback only; the recovery instructions say how to reach it.

## Uninstall

```sh host
sudo /opt/chaosgw/uninstall.sh            # stop the gateway and take it out of the kernel; the data and the files stay
sudo /opt/chaosgw/uninstall.sh --purge    # and delete the data, the files, the images (asks for a yes; --yes does not)
```

The script stops the containers (the API first, the executor last, which removes everything the gateway created in the kernel, see
[Stop, restart and recovery](#stop-restart-and-recovery)), runs the teardown service for what a killed executor left, removes the containers and
the volumes of the sockets, and gives each test interface back to systemd-networkd (it removes the rule `/etc/systemd/network/10-chaosgw-IF.network`). Without `--purge` the revisions,
secrets, audit log and captures in `/var/lib/chaosgw`, the files in `/opt/chaosgw` and the images stay, so `install.sh` brings the gateway
back as it was. With `--purge` they are deleted as well, and so are the modules and sysctl files the host setup wrote
(`/etc/modules-load.d/chaos-gateway.conf`, `/etc/sysctl.d/90-chaos-gateway.conf`). It never removes Docker, touches the uplink or the
management configuration, or undoes what you changed in netplan for the test interfaces. A step that fails is named, the others go on, the exit
status is 1, and with `--purge` the data and `/opt/chaosgw` then stay, because a second run needs the executor's state in them.
`--help` lists the options (`--purge`, `--yes`, `--dir`).

By hand, the same is:

```sh host
cd /opt/chaosgw
docker compose stop                                           # the API first, the executor last: it removes what it made in the kernel
docker compose --profile teardown run --rm teardown --purge   # only needed if the executor was killed; it refuses while one serves
docker compose down -v
sudo sh host-setup.sh --remove-test-interface lan0
sudo rm -f /etc/modules-load.d/chaos-gateway.conf /etc/sysctl.d/90-chaos-gateway.conf
sudo rm -rf /var/lib/chaosgw /opt/chaosgw                       # the data: revisions, secrets, audit log, captures
```

The stop removed what the gateway created in the kernel (above). What stays is the host's: the modules and
`net.ipv4.ip_forward` (a reboot resets them) and the netplan entries you changed for the test interfaces.

If `docker compose down -v` fails with `device or resource busy` on the `chaosgw-run` volume, a mount of the
executor has leaked into the host (issue #57, F4). Unmount it, then run `down -v` again:

```sh host
grep chaosgw /proc/self/mountinfo        # the leaked mount ends in .../_data/bird
sudo umount /var/lib/docker/volumes/chaosgw_chaosgw-run/_data/bird
```
