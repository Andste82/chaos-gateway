#!/bin/sh
# Starts Kea in its container (plan §2.7, §3.8). Kea restricts its paths: the control socket lives in
# /run/kea (a directory not more open than 0750) and the leases in /var/lib/kea. The socket gets the
# group of the API container's user, so the API can talk to it and nobody else can.
set -eu
GROUP="${CHAOSGW_API_GID:-65532}"
umask 0007
mkdir -p /run/kea /var/lib/kea
# a PID file of an earlier run (a killed container, a reboot) would make Kea refuse to start
rm -f /run/kea/*.pid
# the control socket of an earlier run lives on in the volume across a recreated container and a reboot, and the health check
# (`test -S`) would pass on it before this Kea has made its own
rm -f /run/kea/kea4-ctrl.sock
chgrp "$GROUP" /run/kea
chmod 0750 /run/kea
# before the API has sent the scopes: no subnet, and the hook that reports leases. It is written to a file of its own and renamed, so
# that a kill during the write leaves no partial configuration that the next start takes for a finished one.
if [ ! -s /var/lib/kea/kea-dhcp4.conf ]; then
	/usr/local/bin/chaosgw kea-config >/var/lib/kea/kea-dhcp4.conf.new
	mv -f /var/lib/kea/kea-dhcp4.conf.new /var/lib/kea/kea-dhcp4.conf
fi
exec /usr/sbin/kea-dhcp4 -c /var/lib/kea/kea-dhcp4.conf
