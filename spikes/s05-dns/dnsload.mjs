// Minimal DNS load generator: node dnsload.mjs <server> <name> <count> <concurrency>
import dgram from 'node:dgram';
import dns from 'dns-packet';
const [, , server, name, count, conc] = process.argv;
const N = +count, C = +conc;
const s = dgram.createSocket('udp4');
let sent = 0, done = 0, lost = 0; const pending = new Map();
const t0 = process.hrtime.bigint();
const send = () => {
  if (sent >= N) return;
  const id = sent++ & 0xffff;
  pending.set(id, setTimeout(() => { pending.delete(id); lost++; done++; next(); }, 2000));
  s.send(dns.encode({ type: 'query', id, flags: dns.RECURSION_DESIRED, questions: [{ type: 'A', name }] }), 53, server);
};
const next = () => { if (done >= N) finish(); else send(); };
const finish = () => {
  const sec = Number(process.hrtime.bigint() - t0) / 1e9;
  console.log(JSON.stringify({ queries: N, lost, seconds: +sec.toFixed(2), qps: Math.round((N - lost) / sec) }));
  process.exit(0);
};
s.on('message', (m) => {
  const id = dns.decode(m).id; const t = pending.get(id);
  if (!t) return; clearTimeout(t); pending.delete(id); done++; next();
});
s.bind(() => { for (let i = 0; i < C; i++) send(); });
