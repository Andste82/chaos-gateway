# Recovery

What the gateway does when its configuration cannot be applied or the machine was stopped badly, and what you do about it: the last
known good revision, safe mode, the cleanup of the host (`teardown`), a lost password, an update that went wrong. The web app's red
safe-mode banner links here. The commands run on the gateway's host in `/opt/chaosgw`, where `compose.yaml` and `.env` are
([installation](install.md)); the calls to the API go to `$CHAOSGW_URL` with a token of the scope `full` (made from the admin session, [API guide](api-guide.md#tokens-and-scopes); if
you cannot log in, see [forgotten password](#forgotten-password-lost-token-lost-setup-token)).

## Contents

- [What happens at every start](#what-happens-at-every-start)
- [The last known good revision](#the-last-known-good-revision)
- [Safe mode](#safe-mode)
- [A change locked me out](#a-change-locked-me-out)
- [After a crash or a kill: teardown](#after-a-crash-or-a-kill-teardown)
- [Forgotten password, lost token, lost setup token](#forgotten-password-lost-token-lost-setup-token)
- [An update went wrong](#an-update-went-wrong)
- [Backup and restore](#backup-and-restore)

## What happens at every start

The kernel state is never the truth; the configuration is. Whatever happened to the machine before (a clean stop, a power loss, a killed
container), the start compiles the **active revision** again and applies it in full, then reads the kernel back and checks that it is
what was meant. The overlays of tests are gone (a test starts from a clean baseline), and runs that were active ended as `aborted`.

1. The active revision is applied. If that fails, it is tried once more after ten seconds: a failure that only a moment caused must
   not move the gateway to an older configuration for good.
2. If it fails again, or the revision's file is damaged, the gateway applies the **last known good revision**, the newest one that was
   confirmed and worked, and makes it the active one. The revision that failed is marked `rolled_back`.
3. If there is none, or that one fails too, the gateway runs in **safe mode**.

A failure of the way to the executor (its container is not up yet, its socket is gone) says nothing about the revision, so it is none
of these steps: the gateway applies the same revision again, after ten seconds, then after twenty, forty and a minute each time, and does
not fall back or enter safe mode while the executor is away. `GET /state` shows the last apply as failed, the health report is
`degraded`, and the log says `the executor cannot be reached at start`; look at the executor container (`docker compose logs exec`).
When it is up, the revision is applied and the start goes on.

A newer configuration than the program understands is never repaired by falling back: the gateway refuses to start and says which
version is needed (the downgrade check, [update](#an-update-went-wrong)).

## The last known good revision

You notice it in three places: the log of the `api` container says `revision cannot be applied at start: applying the last known good
revision` with the revision, the reason and the one it fell back to; the event `revision_rolled_back` (reason `boot: …`) is on the
event stream; and `GET /api/v1/revisions` shows the revision that failed as `rolled_back`.

```sh host
cd /opt/chaosgw
docker compose logs api | grep -i 'last known good'
```

What to do: the gateway works with the older configuration, so nothing is urgent, but the change you made last is not in force. Read why
it failed (the reason is in the log line above and in the data of the event `revision_rolled_back`, whose `reason` starts with `boot:`), repair the cause (an interface that is gone, an address
that clashes with Docker's pools, a missing kernel module: [troubleshooting](troubleshooting.md)) and apply your change again as a new
revision. A revision that failed at start is not used as a fallback by a later start.

The revisions, newest first, and the one that is active:

```sh run
curl -sS -H "Authorization: Bearer $CHAOSGW_ADMIN_TOKEN" "$CHAOSGW_URL/api/v1/revisions" | jq -c '.items[] | {id, status}'
```

```text output
{"id":2,"status":"active"}
{"id":1,"status":"superseded"}
```

The revision ids in this document are those of the gateway the examples run against (revision 1 from the setup, revision 2 active);
yours differ, so read them from the list before you use one.

## Safe mode

When no revision can be applied, the gateway keeps what it needs for you to repair it and nothing more:

- **The management interface keeps working.** The web app and the API stay reachable from the management network, and the management
  network keeps its normal path to the uplink.
- **There is no forwarding for the test networks.** No test network, no bridge, no route between networks, no NAT: the devices have no
  gateway. A WireGuard network of the role `management` is down too, so **the way in is the management interface**, not a tunnel.
- **Everything that needs a forwarding gateway answers `503 unavailable`** with the reason: new overlays, runs, captures, diagnostics,
  probe calibration. Reading, the revisions, removing overlays and runs, tokens and the setup work, so you can look and repair with the
  same tools.

How you see it: the web app shows a red banner on every page with the reason; `GET /api/v1/state` has `safe_mode: true` and
`safe_mode_reason`; the event `safe_mode` is on the stream; the log of the `api` container says `safe mode: management access only, no
forwarding`.

```sh run
curl -sS -H "Authorization: Bearer $CHAOSGW_TOKEN" "$CHAOSGW_URL/api/v1/state" | jq '{safe_mode, safe_mode_reason}'
```

```text output
  "safe_mode": false,
  "safe_mode_reason": null
```

**When no revision can be read at all** (the state directory is damaged and there is no last known good revision either) the gateway has
no configuration, so it knows no management interface to listen on: the API listens on the loopback of the host only, which is the safe
choice for a state that exists only when the store is broken. Reach it from the host: in an SSH session
`curl --cacert /var/lib/chaosgw/service/api.pem https://127.0.0.1:8443/api/v1/state`, or from your machine with
`ssh -L 8443:127.0.0.1:8443 <host>` and `https://127.0.0.1:8443` in the browser (the certificate is the gateway's own, the browser warns). Log
in as usual and restore a backup as described in [Backup and restore](#backup-and-restore).

**The way out is a revision that works.** There is no timer that tries again, because a revision that fails would flap the kernel
every time. Find the last revision that was good (`GET /state` names it in `last_known_good`, and `GET /revisions` marks it `last_known_good: true`;
the web app's revision history shows it), make a candidate of it
(`clone`: the same configuration as a new revision), and apply it. If the change could lock you out (it touches the management
interface), confirm it within 60 seconds. The apply that commits ends safe mode: the banner goes and forwarding returns.

```sh run
adm="Authorization: Bearer $CHAOSGW_ADMIN_TOKEN"
api="$CHAOSGW_URL/api/v1"
good=1                                                       # the revision to go back to
active=$(curl -sS -H "$adm" "$api/revisions/active" | jq -r .id)
candidate=$(curl -sS -H "$adm" -H "If-Match: \"$active\"" -X POST "$api/revisions/$good/clone" | jq -r .id)
curl -sS -H "$adm" -X POST "$api/revisions/$candidate/preview" | jq -r '.requires_confirm'
curl -sS -H "$adm" -X POST "$api/revisions/$candidate/apply" | jq -r .status
```

```text output
false
active
```

If the revision you clone also fails, the reason of the new failure is in the response of the apply and in the log of the `api`
container; fix the cause first (the same causes as above). If no revision is marked, clone the oldest one that was applied (`status` `superseded` with a `confirmed_at`; usually the revision of the
setup, which loses everything you did since), or start from the exported configuration of a backup ([below](#backup-and-restore)).

**A WireGuard key rotated since the revision you fall back to** cannot be restored: the commit of a rotation retires the old key
generation, so a fallback to a revision from before the rotation has no key to configure and ends in safe mode too. The way out is the
same: a new revision. Clone the revision that was active before the failure, which carries the current keys, and repair the cause in it.

## A change locked me out

A change that could cut off the administrator (the management interface, the binding of the UI and API, a WireGuard network of the
role `management`, the gateway's own protection) is applied provisionally: it waits for you to confirm it within 60 seconds and is rolled back
by itself when nobody does. So if the browser cannot reach the gateway after such a change, wait a minute; the previous revision comes
back, and the first setup opens again with a new token in the log if the first revision was the one that was rolled back. Nothing needs
doing, and `POST /revisions/{id}/rollback` does it at once from another route. Access from the management network to the control plane
(UI, API, SSH) is always allowed by the gateway and cannot be removed by an access rule; a mistake in the host's own network
configuration (netplan) of the management interface is the operating system's to repair.

## After a crash or a kill: teardown

A normal stop (`docker compose stop`, a reboot, an update) removes everything the gateway created in the kernel: the overlays and runs
first, then its nftables table, policy rules and routes, the test bridges and their addresses, the service namespace's pair and name,
the probes with their namespaces and DHCP clients, the tc trees, the rules in `DOCKER-USER` and the WireGuard interfaces. The devices
of the test networks have no gateway until the next start, which builds everything again from the last confirmed revision. The host's
own configuration (the uplink, the management interface, `ip_forward`, the loaded modules, the netplan entries of the test interfaces) is
not touched.

An executor that was killed (out of memory, `docker kill`, a power loss) could not clean up. The next start repairs that by itself,
because it replaces what it finds with the committed revision. If you want the host clean without starting the gateway (to remove the
installation, or to find out what is left), stop the stack and run the teardown:

```sh host
cd /opt/chaosgw
docker compose stop
docker compose --profile teardown run --rm teardown
```

It refuses while an executor is serving, because two writers of the kernel would break each other. It reads what the gateway owns
(the interfaces assigned to the executor, kept in its state file, and the fixed names of its objects), removes it, reads the kernel again
and lists what is still there, with exit code 1 if anything is. It can be repeated and finishes what a killed executor left. It never
touches an interface that was not assigned to the gateway.

`--purge` also empties the data: the revisions, the secrets, the audit log, the service token, the TLS CAs, the captures, Kea's leases
and the executor's state. That is irreversible and meant for starting from nothing (the directories stay, with their owners). It
refuses while the `api` container is running (`docker compose stop` first), and empties the data only when the kernel is clean; when the teardown reports leftovers the data stays, because a second run needs the
executor's state, and you run it again after the cause is gone:

```sh host
docker compose --profile teardown run --rm teardown --purge
```

## Forgotten password, lost token, lost setup token

- **Admin password.** From the host, with the API container running (it ends all browser sessions within a second):

  ```sh host
  cd /opt/chaosgw
  docker compose exec api chaosgw admin reset-password --secrets-dir /var/lib/chaosgw/secrets --data-dir /var/lib/chaosgw/api
  ```

  It asks for the new password twice on the terminal (`--password-stdin` and `--password-file` read it instead) and writes
  `auth.password_reset` into the audit log.
- **API token.** A token is shown once and cannot be read again. Make a new one from the admin session (`POST /api/v1/auth/tokens`, [API guide](api-guide.md#tokens-and-scopes))
  and revoke the old one (`DELETE /api/v1/auth/tokens/{id}`).
- **Setup token.** Only the first setup uses it. It is in the log of the `api` container until the setup is done:
  `docker compose logs api | grep 'Setup token'`. A setup that was rolled back opens again with a new one.
- **Failed logins** are limited: after repeated failures the wait doubles up to 15 minutes (`429 rate_limited`). Waiting is the cure;
  restarting the API container also clears the counter.

## An update went wrong

`update.sh` checks before it stops anything: it pulls the new image, asks it (`chaosgw schema-check`) whether it can read the
revisions on this host, waits until no run is active (`GET /api/v1/system/busy`) and only then restarts all containers together. Its exit
codes say what happened: `3` runs were still active, `4` the configuration is newer than the image (use the version that is needed),
`5` the gateway did not come up after the restart.

- **It did not come up (5).** Go back to the version that worked. The script keeps the previous `compose.yaml` as `compose.yaml.bak`,
  and the old image is still on the host:

  ```sh host
  cd /opt/chaosgw
  ./update.sh --version 1.0.0 --no-wait       # the version that worked before
  docker compose logs --tail 100 api exec
  ```

  Going back works as long as the configuration has not been migrated to a newer schema; the gateway then refuses to start and says which
  version it needs. A start that fails on the configuration is handled as [above](#what-happens-at-every-start): last known good
  revision, else safe mode.
- **A start that is stuck** (the containers restart in a loop): `docker compose logs exec api` says why; the executor's preflight lines
  name the usual host causes ([troubleshooting](troubleshooting.md#the-preflight-checks)).
- **The devices lost their network for a minute** during the update: that is the stop cleanup and the rebuild, not a failure
  ([installation](install.md#updating)). Leases are kept, so a device gets its address again.

## Backup and restore

What defines a gateway is the configuration and its secrets. `include_secrets=true` (scope `full`) makes the export complete, with the
WireGuard keys, so it is a backup to keep as safe as the keys themselves; the file is made readable for its owner only (`umask 077`).
The backup is JSON because `POST /revisions` takes a complete configuration as `application/json` (the YAML export is for reading):

```sh run
umask 077
adm="Authorization: Bearer $CHAOSGW_ADMIN_TOKEN"
active=$(curl -sS -H "$adm" "$CHAOSGW_URL/api/v1/revisions/active" | jq -r .id)
curl -sS --fail-with-body -H "$adm" "$CHAOSGW_URL/api/v1/revisions/$active/export?include_secrets=true&format=json" -o gateway-backup.json
jq .schema_version gateway-backup.json
stat -c 'mode %a' gateway-backup.json
```

```text output
1
mode 600
```

It goes into a gateway that has finished its setup as a candidate against its active revision, which `If-Match` has to name, and is
applied as above (preview, then apply; confirmed within 60 seconds if it touches the management interface):

```sh run
adm="Authorization: Bearer $CHAOSGW_ADMIN_TOKEN"
active=$(curl -sS -H "$adm" "$CHAOSGW_URL/api/v1/revisions/active" | jq -r .id)
curl -sS --fail-with-body -H "$adm" -H "Content-Type: application/json" -H "If-Match: \"$active\"" --data-binary @gateway-backup.json "$CHAOSGW_URL/api/v1/revisions" | jq -r .status
```

```text output
candidate
```

Without the `secrets` the WireGuard peers in `generated` mode get new keys, and the preview lists them as warnings. **The export does not
carry the test CAs** (`/var/lib/chaosgw/tlsca`): devices that installed the test CA keep trusting the old one, so a gateway made from the
export alone fails the TLS cases of those devices until they install the new CA. The files of a whole installation are all the directories
below `/var/lib/chaosgw`: `state` (the revisions), `secrets` (the keys and credentials), `api` (the audit log and the runs), `service` (the
token of the service containers), `tlsca` (the two CAs of the TLS cases), `captures`, `exec` (the interfaces assigned to the executor) and
`kea` (the leases); copy them with the gateway stopped to move it to another host, with the owners that [installation](install.md) lists.
