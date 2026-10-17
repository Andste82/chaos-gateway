# Installation (interim)

How to install Chaos Gateway on a host today, until M28 (container deployment: one `compose.yaml`, the update
script, `teardown`) and M30 (the user guide) replace this page. The host is Ubuntu 24.04 or 26.04, x86-64 or
ARM64, with at least one interface for the uplink and management and one interface per test network
(plan §1.5, §3.8). Everything below runs on that host.

## 1. Build the image

There is no published image yet. Build it from a checkout, with the version, so `chaosgw version` names the
commit:

```sh
git clone https://github.com/Andste82/chaos-gateway.git && cd chaos-gateway
docker build -f deploy/Dockerfile -t chaos-gateway:latest \
  --build-arg VERSION="$(git describe --tags --always --dirty)" \
  --build-arg COMMIT="$(git rev-parse --short HEAD)" \
  --build-arg DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)" .
docker run --rm chaos-gateway:latest version
```

The compose files use `chaos-gateway:${CHAOSGW_VERSION:-latest}`. (`make image` builds both architectures with
buildx and keeps no local image; it is for CI.)

## 2. Prepare the host

Once, as root; safe to run again:

```sh
docker run --rm --entrypoint cat chaos-gateway:latest /usr/local/share/chaosgw/host-setup.sh | sudo sh
```

It installs Docker if missing, loads the kernel modules now and at boot
(`/etc/modules-load.d/chaos-gateway.conf`), enables IPv4 forwarding (`/etc/sysctl.d/90-chaos-gateway.conf`) and
prints the netplan changes the test interfaces need. It never touches the uplink or the management configuration.

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

Open `https://<address of the host>:8443` in a browser. The certificate is the gateway's own until one is
installed (M29), so the browser warns once. The setup wizard asks for the token, the uplink, the management
interface, the test interfaces and the admin password, and shows the preflight's hints (for example a netplan
entry to remove from a test interface).

Applying the setup configures the host's real interfaces and restricts the UI and API to the management network.
Revision 1 waits for confirmation: if the browser cannot reach the gateway afterwards, it is rolled back when the
countdown ends and the setup opens again, with a new token in the log.

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
- The status of each forward says why it is inactive, for example "the device has no address".

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
sudo rm -f /etc/modules-load.d/chaos-gateway.conf /etc/sysctl.d/90-chaos-gateway.conf
sudo reboot
```

Stopping the executor removes the active faults; the bridges, nftables tables, routes and WireGuard interfaces of
the last configuration stay until the reboot, which also unloads the modules and resets IPv4 forwarding. Undo
by hand what you changed in netplan for the test interfaces.

If `docker compose down -v` fails with `device or resource busy` on the `chaosgw-run` volume, a mount of the
executor has leaked into the host (issue #57, F4). Unmount it, then run `down -v` again:

```sh
grep chaosgw /proc/self/mountinfo        # the leaked mount ends in .../_data/bird
sudo umount /var/lib/docker/volumes/${COMPOSE_PROJECT_NAME}_chaosgw-run/_data/bird
```
