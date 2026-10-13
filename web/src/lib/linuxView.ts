// The technical view (plan §2.17 "Technical view"): the kernel state Chaos Gateway owns, as the gateway
// renders it (`GET /state/linux`), cut down to one object — the lines that name a network's interface or
// subnet, with the table, chain or interface they belong to — or to one chain.
import type { LinuxState } from '../api/generated/model';

export type SectionName = 'nftables' | 'tc' | 'routes' | 'wireguard';

export const sectionLabels: Record<SectionName, string> = { nftables: 'nftables', tc: 'tc', routes: 'Routes', wireguard: 'WireGuard' };

export function sectionsOf(s: LinuxState | undefined): { name: SectionName; text: string }[] {
  if (!s) return [];
  return (['nftables', 'tc', 'routes', 'wireguard'] as const).map((name) => ({ name, text: s[name] }));
}

function escape(s: string): string {
  return s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
}

/** Whether a line names one of the terms as a whole word (br-iot, not br-iot2; 10.10.0.0/24, not 110.10.0.0/24). */
export function names(line: string, terms: string[]): boolean {
  return terms.some((t) => t && new RegExp(`(^|[^\\w.-])${escape(t)}($|[^\\w.-])`).test(line));
}

/**
 * The nftables text cut down to the lines that name a term: each with the table and the chain, set or
 * map around it (their header lines, the base chain's hook line, and their closing braces), in the
 * order nft prints them. `keep` decides on a block's header instead: a chain it accepts is kept whole.
 */
export function nftExcerpt(text: string, terms: string[], keep: (header: string) => boolean = () => false): string {
  const out: string[] = [];
  let table: string | undefined;
  let tableOut = false;
  let block: { header: string[]; out: boolean; whole: boolean } | undefined;
  const emitTable = () => {
    if (!tableOut && table !== undefined) out.push(table);
    tableOut = true;
  };
  const emitBlock = () => {
    emitTable();
    if (block && !block.out) out.push(...block.header);
    if (block) block.out = true;
  };
  for (const line of text.split('\n')) {
    if (/^\S.*\{$/.test(line)) {
      table = line;
      tableOut = false;
      continue;
    }
    if (line === '}') {
      if (tableOut) out.push('}');
      table = undefined;
      continue;
    }
    if (/^ {2}\S.*\{$/.test(line)) {
      block = { header: [line], out: false, whole: keep(line) };
      if (block.whole || names(line, terms)) emitBlock();
      continue;
    }
    if (line === '  }') {
      if (block?.out) out.push(line);
      block = undefined;
      continue;
    }
    // a base chain's hook and a set's type and flags belong to the header
    if (block && !block.out && /^ {4}(type |flags )/.test(line)) {
      block.header.push(line);
      continue;
    }
    if ((block?.whole && line.trim()) || (line.trim() && names(line, terms))) {
      if (block) emitBlock();
      else emitTable();
      out.push(line);
    }
  }
  return out.join('\n');
}

/**
 * A section of indented groups (routes, WireGuard, tc) cut down to the terms: a group whose first line
 * names a term is kept whole, otherwise only its lines that name one.
 */
export function groupExcerpt(text: string, terms: string[]): string {
  const out: string[] = [];
  let headKept = false;
  for (const line of text.split('\n')) {
    if (!line.trim()) continue;
    const indented = /^\s/.test(line) || /^[^:\s]+: {3}/.test(line);
    if (!indented) {
      headKept = names(line, terms);
      if (headKept) out.push(line);
      continue;
    }
    if (headKept || names(line, terms)) out.push(line);
  }
  return out.join('\n');
}

/** Every section cut down to the terms; an empty section says so. */
export function excerpt(s: LinuxState, terms: string[]): { name: SectionName; text: string }[] {
  return sectionsOf(s).map(({ name, text }) => ({ name, text: name === 'nftables' ? nftExcerpt(text, terms) : groupExcerpt(text, terms) }));
}
