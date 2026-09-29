// S4 — TLS responder (Chaos Gateway core, Node.js) and a "real" TLS server for comparison.
//
//   node tls-lab.mjs responder <port> <certdir> <modefile> <eventlog>
//   node tls-lab.mjs real      <port> <certdir>
//
// The responder receives transparently redirected connections (nftables REDIRECT),
// looks up the test mode for the client address and either presents a broken certificate
// or breaks the handshake. It never forwards traffic. A completed handshake means the
// client ACCEPTED the certificate — for broken certificates that is a device bug.
import net from 'node:net';
import tls from 'node:tls';
import fs from 'node:fs';

const [, , role, port, certdir, modefile, eventlog] = process.argv;
const pem = (n) => ({ key: fs.readFileSync(`${certdir}/${n}.key`), cert: fs.readFileSync(`${certdir}/${n}.crt`) });
const log = (o) => fs.appendFileSync(eventlog, JSON.stringify({ t: Date.now() / 1000, ...o }) + '\n');

if (role === 'real') {
  tls.createServer(pem('real-server'), (s) => { s.on('data', (d) => s.write(d)); s.on('error', () => {}); })
    .listen(+port, '0.0.0.0');
} else if (role === 'https') {
  const https = await import('node:https');
  https.createServer(pem('real-server'), (req, res) => res.end('hello from real server\n')).listen(+port, '0.0.0.0');
} else {
  const ctx = {};
  for (const m of ['valid', 'expired', 'notyet', 'wronghost', 'selfsigned']) ctx[m] = tls.createSecureContext(pem(m));
  ctx.untrusted = ctx.valid; // signed by the test CA; untrusted unless the client trusts it

  // SNI from a raw ClientHello (no TLS stack needed for the abort/delay modes)
  const sniOf = (buf) => {
    try {
      let p = 43; p += 1 + buf[p]; p += 2 + buf.readUInt16BE(p); p += 1 + buf[p];
      const end = p + 2 + buf.readUInt16BE(p); p += 2;
      while (p + 4 <= end) {
        const type = buf.readUInt16BE(p), len = buf.readUInt16BE(p + 2);
        if (type === 0) return buf.subarray(p + 9, p + 9 + buf.readUInt16BE(p + 7)).toString();
        p += 4 + len;
      }
    } catch { /* not a ClientHello */ }
    return null;
  };

  net.createServer((sock) => {
    const client = sock.remoteAddress.replace('::ffff:', '');
    const modes = JSON.parse(fs.readFileSync(modefile, 'utf8'));
    const mode = modes[client] ?? 'untrusted';
    const base = { client, mode, local_port: sock.localPort };
    sock.on('error', () => {});

    if (ctx[mode]) {
      // certificate cases: hand the untouched socket to the TLS stack; SNI via callback
      let sni = null;
      const t = new tls.TLSSocket(sock, {
        isServer: true, secureContext: ctx[mode],
        SNICallback: (name, cb) => { sni = name; cb(null, ctx[mode]); },
      });
      t.on('secure', () => { log({ ...base, sni, event: 'handshake_completed_client_accepted' }); t.end(); });
      t.on('error', (e) => log({ ...base, sni, event: 'handshake_failed', error: e.code || e.message }));
      return;
    }
    // handshake-level faults: read the raw ClientHello first
    sock.once('data', (hello) => {
      const sni = sniOf(hello);
      if (mode === 'abort') { log({ ...base, sni, event: 'reset_after_clienthello' }); return sock.resetAndDestroy(); }
      if (mode === 'close') { log({ ...base, sni, event: 'fin_after_clienthello' }); return sock.end(); }
      if (mode === 'stall') { log({ ...base, sni, event: 'stalling' }); return setTimeout(() => sock.destroy(), 30000); }
    });
  }).listen(+port, '0.0.0.0', () => log({ event: 'listening', port: +port }));
}
