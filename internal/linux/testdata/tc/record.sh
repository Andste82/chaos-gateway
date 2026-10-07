#!/bin/sh
# Records the `tc -j` fixtures of this directory from a real kernel. Run it as root in a machine
# that may create network namespaces and dummy/veth interfaces (the persistent VM of
# docs/development.md: `make vm-exec CMD='sh internal/linux/testdata/tc/record.sh /tmp/chaosgw-vm/out/tc'`),
# then copy the files over. The files here were recorded with kernel 6.8.0-142 and iproute2 6.19.0.
#
# What it records: a representative Chaos Gateway tree (HTB root, default class, one class with a
# netem leaf and an fw filter per fault id and direction), without and with counters, with a 10 s
# queue that holds 25 packets, and after a queue overflow; the usual qdiscs of the operating
# system (noqueue, mq with pfifo_fast and fq_codel children, an fq_codel root, ingress); a tree with
# shapes the compiler never emits (slot, ecn, rate overheads, a class below a class, a u32 filter);
# a netem tree on a second interface that has a duplicating leaf (the kernel refuses to mix those
# with other netems in one tree).
set -e
OUT=${1:?output directory}
mkdir -p "$OUT"
N="ip netns exec m8b"
T="$N tc"
setup() {
  ip netns del m8b 2>/dev/null || true
  ip netns add m8b
  $N ip link add dum0 type dummy; $N ip link set dum0 up
  $N ip addr add 10.9.0.1/24 dev dum0
  $N ip neigh add 10.9.0.2 lladdr 02:00:00:00:00:02 dev dum0 nud permanent
  $T qdisc add dev dum0 root handle 1: htb default 1
  $T class replace dev dum0 parent 1: classid 1:1 htb rate 10gbit quantum 60000
}
# send COUNT udp packets with mark MARK at PPS packets per second through dum0
send() {
  $N python3 - "$1" "$2" "$3" <<'PY'
import socket, sys, time
mark, n, pps = int(sys.argv[1], 0), int(sys.argv[2]), float(sys.argv[3])
s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
s.setsockopt(socket.SOL_SOCKET, socket.SO_MARK, mark)
s.bind(("10.9.0.1", 0))
t0 = time.time()
for i in range(n):
    s.sendto(b"x" * 100, ("10.9.0.2", 9))
    d = t0 + (i + 1) / pps - time.time()
    if d > 0:
        time.sleep(d)
PY
}
set +e
setup
mkc() { # minor mark netem-args...
  m=$1; mark=$2; shift 2
  $T class replace dev dum0 parent 1: classid 1:$m htb rate 10gbit quantum 60000
  $T qdisc replace dev dum0 parent 1:$m handle $m: netem "$@"
  $T filter replace dev dum0 parent 1: handle $mark/0x1fff0 protocol ip prio 1 fw flowid 1:$m
}
mkc 24 0x000a0 limit 5000 delay 50ms 10ms 0% distribution normal loss random 1% 25% reorder 0% 0% duplicate 0% 0% corrupt 0% 0% rate 0bit
mkc 25 0x100a0 limit 1000 delay 20ms 5ms 0% loss gemodel 1% 10% 70% 0.1% reorder 25% 0% duplicate 0% 0% corrupt 0.1% 0% rate 2mbit
mkc 26 0x000b0 limit 1000 delay 600ms 0ms 0% loss random 0% 0% reorder 0% 0% duplicate 0% 0% corrupt 0% 0% rate 0bit
mkc 27 0x100b0 limit 1000 delay 0ms 0ms 0% loss random 100% 0% reorder 0% 0% duplicate 0% 0% corrupt 0% 0% rate 0bit
mkc 28 0x000c0 limit 1000 delay 0ms 0ms 0% loss random 0% 0% reorder 0% 0% duplicate 0% 0% corrupt 0% 0% rate 0bit
for k in qdisc class filter; do $T -j $k show dev dum0 > $OUT/tree_${k}.json; done
# traffic: 200 packets through 1:26 (600 ms), 100 through 1:24 (1% loss), 50 through 1:27 (all dropped), 30 default
send 0xb0 200 100 &
send 0xa0 100 100 &
send 0x100b0 50 100 &
send 0 30 100 &
wait
sleep 1
for k in qdisc class filter; do $T -s -j $k show dev dum0 > $OUT/tree_${k}_stats.json; done
# a queue that is not empty when read
send 0xb0 100 100 &
sleep 2.5
$T -s -j qdisc show dev dum0 > $OUT/tree_qdisc_stats_queued.json
wait
# other qdisc shapes
$N ip link add v0 numtxqueues 4 numrxqueues 4 type veth peer name v1
$N ip link set v0 up; $N ip link set v1 up
$T -j qdisc show dev v0 > $OUT/os_noqueue.json
$T qdisc replace dev v0 root mq; $T -j qdisc show dev v0 > $OUT/os_mq.json
$T qdisc replace dev v0 root handle 8001: mq; 
$T qdisc replace dev v0 parent 8001:1 fq_codel; $T -j qdisc show dev v0 > $OUT/os_mq_fqcodel.json
$T qdisc replace dev v0 root fq_codel; $T -j qdisc show dev v0 > $OUT/os_fq_codel.json
$T qdisc add dev v0 ingress; $T -j qdisc show dev v0 > $OUT/os_fq_codel_ingress.json
$T -j qdisc show > $OUT/os_all_devs.json
# unusual shapes
$N ip link add dum2 type dummy; $N ip link set dum2 up
$T qdisc add dev dum2 root handle 1: htb default 10 r2q 5
$T class add dev dum2 parent 1: classid 1:5 htb rate 100mbit ceil 200mbit prio 2 burst 15k
$T class add dev dum2 parent 1:5 classid 1:10 htb rate 10mbit ceil 20mbit quantum 2000
$T qdisc add dev dum2 parent 1:10 handle 10: netem limit 500 delay 10ms slot 1ms 2ms packets 10 ecn loss random 0.5% rate 1mbit 20 10 5
$T filter add dev dum2 parent 1: protocol ip prio 5 u32 match ip src 10.0.0.0/24 flowid 1:10
$T filter add dev dum2 parent 1: protocol ip prio 1 handle 7 fw flowid 1:5
for k in qdisc class filter; do $T -j $k show dev dum2 > $OUT/odd_${k}.json; done
$T -d qdisc show dev dum2 > $OUT/odd_qdisc.txt
ip netns del m8b

setup
$T class replace dev dum0 parent 1: classid 1:24 htb rate 10gbit quantum 60000
$T qdisc replace dev dum0 parent 1:24 handle 24: netem limit 1000 delay 10s 0ms 0% loss random 0% 0% reorder 0% 0% duplicate 0% 0% corrupt 0% 0% rate 0bit
$T filter replace dev dum0 parent 1: handle 0x000a0/0x1fff0 protocol ip prio 1 fw flowid 1:24
send 0xa0 25 1000
$T -s -j qdisc show dev dum0 > $OUT/queued_qdisc_stats.json
$T -s -j class show dev dum0 > $OUT/queued_class_stats.json
$T -s qdisc show dev dum0 > $OUT/queued_qdisc_stats.txt
# limit overflow: limit 10, delay 10s, 25 packets -> drops
$T qdisc replace dev dum0 parent 1:24 handle 24: netem limit 10 delay 10s 0ms 0% loss random 0% 0% reorder 0% 0% duplicate 0% 0% corrupt 0% 0% rate 0bit
send 0xa0 25 1000
$T -s -j qdisc show dev dum0 > $OUT/overflow_qdisc_stats.json
$T -s qdisc show dev dum0 > $OUT/overflow_qdisc_stats.txt
ip netns del m8b


setup
$T class replace dev dum0 parent 1: classid 1:24 htb rate 10gbit quantum 60000
$T qdisc replace dev dum0 parent 1:24 handle 24: netem limit 5000 delay 50ms 10ms 0% distribution normal loss random 1% 25% reorder 0% 0% duplicate 0% 0% corrupt 0% 0% rate 0bit
$T -j qdisc show dev dum0 > $OUT/tree1_qdisc.json
ip netns del m8b

setup
# a second device with a duplicating leaf alone (the kernel refuses to mix it with other netems in one tree)
$N ip link add dum1 type dummy; $N ip link set dum1 up
$T qdisc add dev dum1 root handle 1: htb default 1
$T class replace dev dum1 parent 1: classid 1:1 htb rate 10gbit quantum 60000
$T class replace dev dum1 parent 1: classid 1:30 htb rate 10gbit quantum 60000
$T qdisc replace dev dum1 parent 1:30 handle 30: netem limit 1000 delay 20ms 5ms 0% loss random 0% 0% reorder 0% 0% duplicate 5% 0% corrupt 0% 0% rate 0bit
$T filter replace dev dum1 parent 1: handle 0x000e0/0x1fff0 protocol ip prio 1 fw flowid 1:30
for k in qdisc class filter; do $T -j $k show dev dum1 > $OUT/tc_${k}_dup.json; done

ip netns del m8b
