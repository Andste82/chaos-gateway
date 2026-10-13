import jsQR from 'jsqr';
import { describe, expect, it } from 'vitest';
import { qrMatrix, qrPath } from './qr';

const conf = `[Interface]
PrivateKey = yAnz5TF+lXXJte14tji3zlMNq+hd2rYUIgJBgB3fBmk=
Address = 10.99.0.3/32
DNS = 10.99.0.1

[Peer]
PublicKey = xTIBA5rboUvnH4htodjb6e697QjLERt1NAB4mZqp8Dg=
PresharedKey = /UwcSPg38hW/D9Y3tcS1FOV0K1wuURMbS0sesJEP5ak=
AllowedIPs = 10.99.0.0/24, 10.10.0.0/24
Endpoint = 203.0.113.1:51820
PersistentKeepalive = 25
`;

/** The modules drawn as an RGBA image, `scale` pixels each, inside the quiet zone the SVG has. */
function image(m: ReturnType<typeof qrMatrix>, scale = 4, margin = 4) {
  const n = (m.size + 2 * margin) * scale;
  const data = new Uint8ClampedArray(n * n * 4).fill(255);
  for (let y = 0; y < n; y++) {
    for (let x = 0; x < n; x++) {
      const r = Math.floor(y / scale) - margin;
      const c = Math.floor(x / scale) - margin;
      if (r >= 0 && c >= 0 && r < m.size && c < m.size && m.dark[r]![c]) {
        const i = (y * n + x) * 4;
        data[i] = data[i + 1] = data[i + 2] = 0;
      }
    }
  }
  return { data, n };
}

describe('the QR code of a client configuration', () => {
  it('decodes to the configuration it was made from', () => {
    const m = qrMatrix(conf);
    const { data, n } = image(m);
    expect(jsQR(data, n, n)?.data).toBe(conf);
  });

  it('draws the dark modules as rows of rectangles inside the quiet zone', () => {
    const m = { size: 3, dark: [[true, true, false], [false, false, false], [false, true, true]] };
    expect(qrPath(m, 4)).toBe('M4 4h2v1h-2zM5 6h2v1h-2z');
  });
});
