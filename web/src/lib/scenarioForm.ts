// The settings of a scenario (plan §2.10; M19): its name and description, the target it acts on, whether it
// records the target's traffic for the whole run, how a fault that matched no packet is reported, and the
// preconditions it skips. The steps and the checks have their own editors; the form leaves them as they are.
import type { PreconditionId, Scenario, Scope } from '../api/generated/model';
import { scopeOf, targetOf } from './faultForm';
import { scenarioProblems } from './scenarios';

export interface ScenarioForm {
  name: string;
  description: string;
  /** `device:<id>`, `group:<id>` or `network:<id>` (`faultForm.targetOf`). */
  target: string;
  capture: boolean;
  zeroHit: 'warning' | 'error';
  skip: PreconditionId[];
}

export type ScenarioField = 'name' | 'description' | 'target' | 'capture' | 'zeroHit' | 'skip';

export const preconditionLabels: Record<PreconditionId, { label: string; hint: string }> = {
  target_online: { label: 'The target is online', hint: 'A device must be online; of a group or a network at least one.' },
  uses_gateway_dns: { label: 'The target uses the gateway\'s DNS', hint: 'Only checked when the scenario has DNS steps or host names.' },
  no_foreign_overlays: { label: 'No overlay of another owner is on the target', hint: 'A fault set by someone else would blur the result.' },
};

export function emptyScenarioForm(target = ''): ScenarioForm {
  return { name: '', description: '', target, capture: false, zeroHit: 'warning', skip: [] };
}

export function scenarioToForm(sc: Scenario): ScenarioForm {
  return {
    name: sc.name,
    description: sc.description ?? '',
    target: targetOf(sc.target as Scope),
    capture: sc.capture === true,
    zeroHit: sc.zero_hit ?? 'warning',
    skip: [...(sc.preconditions?.skip ?? [])],
  };
}

/**
 * The scenario of the form on `base` (the steps and checks it has): a member that has its default and was
 * not there before stays out, so an edit elsewhere does not write the defaults into the scenario.
 */
export function formToScenario(f: ScenarioForm, base?: Scenario): Scenario {
  const out: Scenario = { name: f.name.trim(), target: scopeOf(f.target), steps: base?.steps ?? [] };
  const description = f.description.trim();
  if (description) out.description = description;
  if (f.capture || base?.capture !== undefined) out.capture = f.capture;
  if (f.zeroHit !== 'warning' || base?.zero_hit !== undefined) out.zero_hit = f.zeroHit;
  if (f.skip.length || base?.preconditions !== undefined) out.preconditions = { skip: [...f.skip] };
  if (base?.checks) out.checks = base.checks;
  return out;
}

/** The problems of the form, by field. `others` are the names of the other scenarios. */
export function scenarioFormErrors(f: ScenarioForm, others: string[]): Partial<Record<ScenarioField, string>> {
  const e: Partial<Record<ScenarioField, string>> = {};
  const problems = scenarioProblems({ name: f.name, description: f.description, target: f.target ? scopeOf(f.target) : {}, steps: [{ id: 'x', at: '0s', restore: true }] }, others);
  for (const p of problems) {
    if (p.path === '/name') e.name = p.message;
    else if (p.path === '/description') e.description = p.message;
    else if (p.path === '/target') e.target = p.message;
  }
  return e;
}

/** The field of the form a problem the gateway reported for the scenario names, by the pointer below it. */
export function scenarioFieldOf(rest: string): ScenarioField | undefined {
  const first = rest.split('/').filter(Boolean)[0];
  switch (first) {
    case 'name':
    case 'description':
    case 'target':
    case 'capture':
      return first;
    case 'zero_hit':
      return 'zeroHit';
    case 'preconditions':
      return 'skip';
    default:
      return undefined;
  }
}
