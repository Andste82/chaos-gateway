// S5 — Chaos Gateway DNS proxy (spike).
//
//   node dnsproxy.mjs <listen-ip> <upstream-ip> <config.json> <events.jsonl>
//
// config.json (re-read on every query):
// { "faults":    { "10.10.0.11": { "mode": "nxdomain|servfail|timeout|delay:500|wrong:1.2.3.4|truncate|ttl:1",
//                                  "names": ["broker.example.com"] } },
//   "selectors": { "broker.example.com": "hs_1" },          // hostname → nftables set
//   "perDevice": true }                                       // set key: client . address
//
// Selector sets are filled BEFORE the answer is sent, so the device's first packet
// to the resolved address already matches.
import dgram from 'node:dgram';
import fs from 'node:fs';
import { execFile } from 'node:child_process';
import dns from 'dns-packet';

const [, , listenIp, upstream, cfgFile, evFile] = process.argv;
const sock = dgram.createSocket('udp4');
const log = (o) => fs.appendFileSync(evFile, JSON.stringify({ t: Date.now() / 1000, ...o }) + '\n');
const added = new Map();           // "set|key" → expiry ms (dedupe nft calls)
let nftCalls = 0;

const nftAdd = (set, key, ttl) => new Promise((resolve) => {
  const id = `${set}|${key}`, now = Date.now();
  if (!process.env.DNS_NODEDUPE && (added.get(id) ?? 0) - now > (ttl * 1000) / 2) return resolve(0);   // still fresh
  const t0 = process.hrtime.bigint();
  nftCalls++;
  execFile('nft', ['add', 'element', 'inet', 'chaosgw', set, `{ ${key} timeout ${Math.max(ttl, 5)}s }`], (err) => {
    if (err) log({ event: 'nft_error', error: err.message });
    added.set(id, now + ttl * 1000);
    resolve(Number(process.hrtime.bigint() - t0) / 1e6);
  });
});

const lower = (s) => s.toLowerCase().replace(/\.$/, '');

sock.on('message', async (msg, rinfo) => {
  let q;
  try { q = dns.decode(msg); } catch { return; }
  const cfg = JSON.parse(fs.readFileSync(cfgFile, 'utf8'));
  const name = lower(q.questions?.[0]?.name ?? '');
  const f = cfg.faults?.[rinfo.address];
  const hit = f && (!f.names || f.names.map(lower).includes(name));
  const reply = (p) => sock.send(dns.encode(p), rinfo.port, rinfo.address);
  const base = { id: q.id, type: 'response', flags: dns.RECURSION_DESIRED | dns.RECURSION_AVAILABLE, questions: q.questions };
  const mode = hit ? f.mode : 'normal';
  log({ event: 'query', client: rinfo.address, name, mode });

  if (mode === 'timeout') return;
  if (mode === 'nxdomain') return reply({ ...base, flags: base.flags | 3 });   // rcode lives in the low flag bits
  if (mode === 'servfail') return reply({ ...base, flags: base.flags | 2 });
  if (mode === 'truncate') return reply({ ...base, flags: base.flags | dns.TRUNCATED_RESPONSE });
  if (mode.startsWith('wrong:')) {
    return reply({ ...base, answers: [{ type: 'A', name: q.questions[0].name, ttl: 30, data: mode.slice(6) }] });
  }
  if (mode.startsWith('delay:')) await new Promise((r) => setTimeout(r, +mode.slice(6)));

  // forward upstream
  const up = dgram.createSocket('udp4');
  const timer = setTimeout(() => up.close(), 3000);
  up.on('message', async (ans) => {
    clearTimeout(timer); up.close();
    let a;
    try { a = dns.decode(ans); } catch { return; }
    if (mode.startsWith('ttl:')) for (const r of a.answers) r.ttl = +mode.slice(4);
    const set = cfg.selectors?.[name];
    if (set) {
      const addrs = a.answers.filter((r) => r.type === 'A');       // CNAME chain already resolved upstream
      const ms = await Promise.all(addrs.map((r) =>
        nftAdd(set, cfg.perDevice ? `${rinfo.address} . ${r.data}` : r.data, r.ttl)));
      log({ event: 'selector_update', name, set, client: rinfo.address, addrs: addrs.map((r) => r.data),
            cname: a.answers.filter((r) => r.type === 'CNAME').map((r) => r.data), nft_ms: ms });
    }
    reply({ ...a, id: q.id });
  });
  up.send(msg, 53, upstream);
});
process.on('SIGUSR2', () => log({ event: 'stats', nftCalls }));
sock.bind(53, listenIp, () => log({ event: 'listening' }));
