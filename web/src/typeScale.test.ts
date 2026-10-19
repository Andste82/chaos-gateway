/// <reference types="node" />
import { readdirSync, readFileSync, statSync } from 'node:fs';
import { join, relative, resolve } from 'node:path';
import { describe, expect, it } from 'vitest';

// Plan §2.17 "Visual system": the type scale is 12, 13, 14 and 16 px for text and 20 to 26 px for headings. A size
// outside it (P4-AUDIT-38: the 11 px of the check badges, which P3-AUDIT-16 had made 12 px) is found here in the
// sources, as a Tailwind size (`text-lg`, `text-[11px]`) or a font-size declaration.
const root = resolve(process.cwd(), 'src');

function sources(dir: string): string[] {
  return readdirSync(dir).flatMap((name) => {
    const path = join(dir, name);
    if (statSync(path).isDirectory()) return name === 'generated' ? [] : sources(path);
    return /\.(vue|ts|css)$/.test(name) && !/\.test\.ts$/.test(name) ? [path] : [];
  });
}

const allowedPx = (px: number): boolean => [12, 13, 14, 16].includes(px) || (px >= 20 && px <= 26);
/** The sizes of Tailwind's named steps that are on the scale: xs 12, sm 14, base 16, xl 20, 2xl 24. */
const allowedNamed = new Set(['xs', 'sm', 'base', 'xl', '2xl']);

/** The sizes a text of the sources sets outside the scale, as "file: text". */
function offScale(file: string, text: string): string[] {
  const out: string[] = [];
  for (const m of text.matchAll(/\btext-\[(\d+(?:\.\d+)?)(px|rem|em)\]/g)) {
    const px = m[2] === 'px' ? Number(m[1]) : Number(m[1]) * 16;
    if (!allowedPx(px)) out.push(`${file}: ${m[0]}`);
  }
  for (const m of text.matchAll(/\btext-(xs|sm|base|lg|xl|[2-9]xl)\b/g)) if (!allowedNamed.has(m[1]!)) out.push(`${file}: ${m[0]}`);
  for (const m of text.matchAll(/font-size:\s*(\d+(?:\.\d+)?)px/g)) if (!allowedPx(Number(m[1]))) out.push(`${file}: ${m[0]}`);
  return out;
}

describe('the type scale', () => {
  it('finds a size outside it', () => {
    expect(offScale('a.vue', '<span class="text-[11px] text-[13px] text-xs">')).toEqual(['a.vue: text-[11px]']);
    expect(offScale('a.vue', 'class="text-lg text-3xl text-sm text-[22px]"')).toEqual(['a.vue: text-lg', 'a.vue: text-3xl']);
    expect(offScale('a.css', 'a { font-size: 15px; } b { font-size: 12px }')).toEqual(['a.css: font-size: 15px']);
  });

  it('is kept by every source of the app', () => {
    const found = sources(root).flatMap((f) => offScale(relative(root, f), readFileSync(f, 'utf8')));
    expect(found).toEqual([]);
  });
});
