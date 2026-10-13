// The QR code of a WireGuard client's configuration (plan §2.2.1 "QR code in the UI"), made in the
// browser from the configuration text it downloaded: the code and the download come from one export,
// so they are the same text, and a client with "export once" is downloaded once only.
import qrcode from 'qrcode-generator';

export interface QrMatrix {
  size: number;
  /** dark[row][col]: a dark module. */
  dark: boolean[][];
}

/** The modules of the QR code of text (byte mode, error correction M, the smallest version that fits). */
export function qrMatrix(text: string): QrMatrix {
  const qr = qrcode(0, 'M');
  qr.addData(text, 'Byte');
  qr.make();
  const size = qr.getModuleCount();
  const dark = Array.from({ length: size }, (_, r) => Array.from({ length: size }, (_, c) => qr.isDark(r, c)));
  return { size, dark };
}

/** The dark modules as one SVG path in module units ("M3 0h1v1h-1z…"), drawn inside a quiet zone of `margin`. */
export function qrPath(m: QrMatrix, margin = 4): string {
  let d = '';
  for (let r = 0; r < m.size; r++) {
    for (let c = 0; c < m.size; c++) {
      if (!m.dark[r]![c]) continue;
      // runs of dark modules in a row become one rectangle
      let w = 1;
      while (c + w < m.size && m.dark[r]![c + w]) w++;
      d += `M${c + margin} ${r + margin}h${w}v1h-${w}z`;
      c += w - 1;
    }
  }
  return d;
}
