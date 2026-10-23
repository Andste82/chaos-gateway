/** The canonical text of 16 UUID bytes. */
function uuidText(b: Uint8Array): string {
  const h = [...b].map((x) => x.toString(16).padStart(2, '0')).join('');
  return `${h.slice(0, 8)}-${h.slice(8, 12)}-${h.slice(12, 16)}-${h.slice(16, 20)}-${h.slice(20)}`;
}

/**
 * A random UUID (version 4) for a new configured object. `crypto.randomUUID` exists only in a
 * secure context; the gateway's UI may be opened over plain HTTP on a lab network.
 */
export function newId(): string {
  if (typeof crypto.randomUUID === 'function') return crypto.randomUUID();
  const b = crypto.getRandomValues(new Uint8Array(16));
  b[6] = (b[6]! & 0x0f) | 0x40;
  b[8] = (b[8]! & 0x3f) | 0x80;
  return uuidText(b);
}

/** SHA-1 of a byte string: `crypto.subtle` is missing outside a secure context (and is async). */
function sha1(msg: Uint8Array): Uint8Array {
  const words = new Uint32Array((((msg.length + 8) >> 6) + 1) * 16);
  for (let i = 0; i < msg.length; i++) words[i >> 2]! |= msg[i]! << (24 - (i % 4) * 8);
  words[msg.length >> 2]! |= 0x80 << (24 - (msg.length % 4) * 8);
  words[words.length - 1] = msg.length * 8;
  const h = [0x67452301, 0xefcdab89, 0x98badcfe, 0x10325476, 0xc3d2e1f0];
  const w = new Uint32Array(80);
  const rotl = (x: number, n: number) => (x << n) | (x >>> (32 - n));
  for (let off = 0; off < words.length; off += 16) {
    for (let t = 0; t < 80; t++) w[t] = t < 16 ? words[off + t]! : rotl(w[t - 3]! ^ w[t - 8]! ^ w[t - 14]! ^ w[t - 16]!, 1);
    let [a, b, c, d, e] = h as [number, number, number, number, number];
    for (let t = 0; t < 80; t++) {
      const f = t < 20 ? (b & c) | (~b & d) : t < 40 || t >= 60 ? b ^ c ^ d : (b & c) | (b & d) | (c & d);
      const k = t < 20 ? 0x5a827999 : t < 40 ? 0x6ed9eba1 : t < 60 ? 0x8f1bbcdc : 0xca62c1d6;
      const next = (rotl(a, 5) + f + e + k + w[t]!) | 0;
      e = d;
      d = c;
      c = rotl(b, 30);
      b = a;
      a = next;
    }
    h[0] = (h[0]! + a) | 0;
    h[1] = (h[1]! + b) | 0;
    h[2] = (h[2]! + c) | 0;
    h[3] = (h[3]! + d) | 0;
    h[4] = (h[4]! + e) | 0;
  }
  const out = new Uint8Array(20);
  const view = new DataView(out.buffer);
  h.forEach((v, i) => view.setUint32(i * 4, v >>> 0));
  return out;
}

/** The namespace the gateway derives the UUID of a discovered device from (`discoveredNamespace`, internal/engine/devices.go). */
const discoveredNamespace = '6b9a1d2e4f3c5a779c0e0d2f6a1b3c45';

/**
 * The UUID the gateway gives the discovered device with this MAC address: version 5 over "mac/<mac>", as
 * `DeviceID` in internal/engine/devices.go. Adopting a discovered device keeps that id, so a configured
 * device whose id is the one of a MAC it is seen with comes back under it once it is removed (#78).
 */
export function discoveredDeviceId(mac: string): string {
  const name = new TextEncoder().encode(`mac/${mac.toLowerCase()}`);
  const msg = new Uint8Array(16 + name.length);
  for (let i = 0; i < 16; i++) msg[i] = parseInt(discoveredNamespace.slice(i * 2, i * 2 + 2), 16);
  msg.set(name, 16);
  const b = sha1(msg).slice(0, 16);
  b[6] = (b[6]! & 0x0f) | 0x50;
  b[8] = (b[8]! & 0x3f) | 0x80;
  return uuidText(b);
}
