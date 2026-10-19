// The "Run scenario" dialog (plan §2.10, §2.15; M19): the parameters as `path=value` lines (what `chaosctl run
// --set` takes) and the request the dialog sends, for the `</> API` panel.
import type { RunOptions, Scenario } from '../api/generated/model';
import type { ApiCall } from '../stores/apiPanel';

export interface ParsedParameters {
  parameters: Record<string, string>;
  error?: string;
}

/**
 * Reads the lines of the parameters field: `path=value`, one per line, empty lines ignored. The value is the text after the
 * first `=`; the gateway reads `true`, `false`, whole numbers and `[…]`/`{…}` as such. A path is a chain of field names
 * (a segment into a list is a position or the id of a step).
 */
export function parseParameters(text: string): ParsedParameters {
  const parameters: Record<string, string> = {};
  const lines = text.split('\n');
  for (const [i, raw] of lines.entries()) {
    const line = raw.trim();
    if (!line) continue;
    const eq = line.indexOf('=');
    if (eq < 1) return { parameters, error: `Line ${i + 1}: write path=value, like target.device=esp32-42.` };
    const path = line.slice(0, eq).trim();
    if (!/^[A-Za-z0-9_.-]+$/.test(path) || path.startsWith('.') || path.endsWith('.') || path.includes('..')) {
      return { parameters, error: `Line ${i + 1}: ${path} is no path: field names and step ids joined by dots.` };
    }
    if (path in parameters) return { parameters, error: `Line ${i + 1}: ${path} is set twice.` };
    parameters[path] = line.slice(eq + 1).trim();
  }
  return { parameters };
}

/** The request the dialog sends: a stored scenario by id, or the scenario with unapplied changes inline. */
export function runRequestCalls(id: string, scenario: Scenario, unapplied: boolean, options: RunOptions, queue: boolean): ApiCall[] {
  const query = queue ? '' : '?queue=false';
  if (unapplied) return [{ method: 'POST', path: `/runs${query}`, body: { inline: scenario, ...options }, note: 'the scenario with the changes that are not applied yet' }];
  return [{ method: 'POST', path: `/scenarios/${id}/runs${query}`, ...(Object.keys(options).length ? { body: options } : {}) }];
}
