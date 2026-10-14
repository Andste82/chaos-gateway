/// <reference types="node" />
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { describe, expect, it } from 'vitest';

// Plan §2.17 Accessibility: text contrast ≥ 4.5:1. The token table promises it for the three text
// colours on the surfaces they are used on: cards (surface) and raised or selected items and hovered
// rows (surface-2); on the accent tint (a selected step, an active toggle) only text and text-2.
const css = readFileSync(resolve(process.cwd(), 'src/styles.css'), 'utf8');
const token = (name: string): string => {
  const m = new RegExp(`--color-${name}:\\s*(#[0-9a-fA-F]{6});`).exec(css);
  if (!m) throw new Error(`no token ${name}`);
  return m[1]!;
};

function luminance(hex: string): number {
  const [r, g, b] = [1, 3, 5].map((i) => {
    const v = parseInt(hex.slice(i, i + 2), 16) / 255;
    return v <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4;
  });
  return 0.2126 * r! + 0.7152 * g! + 0.0722 * b!;
}

function contrast(a: string, b: string): number {
  const [x, y] = [luminance(a), luminance(b)];
  return (Math.max(x, y) + 0.05) / (Math.min(x, y) + 0.05);
}

describe('colour tokens', () => {
  it('give every text colour at least 4.5:1 on the surfaces it is used on', () => {
    for (const text of ['text', 'text-2', 'text-3']) {
      for (const bg of ['bg', 'surface', 'surface-2']) {
        expect(contrast(token(text), token(bg)), `${text} on ${bg}`).toBeGreaterThanOrEqual(4.5);
      }
    }
    for (const text of ['text', 'text-2']) expect(contrast(token(text), token('accent-tint')), `${text} on accent-tint`).toBeGreaterThanOrEqual(4.5);
  });

  it('make native controls dark and in the accent colour', () => {
    expect(css).toMatch(/color-scheme:\s*dark/);
    expect(css).toMatch(/accent-color:\s*var\(--color-accent\)/);
  });
});
