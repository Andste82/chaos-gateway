// The profile editor (plan §2.9, §2.17 "Profiles"; P3-M13-03): a custom profile is an object of the
// configuration (`profiles`), made and changed through the draft and the preview like a configured
// fault. The form has the name, the description and the parts the editor makes — an impairment part
// (the fault dialog's impairment fields, `netemForm`) and an MTU part (size and mode); a DNS or TLS
// part a profile already has (defined over the API, available with M20/M21) is kept as it is.
import type { Profile, ProfileParts } from '../api/generated/model';
import { completeSetupBodyConfigurationOneProfilesNameRegExp as nameRegExp } from '../api/generated/zod/system/system';
import { mtuOf, type MtuMode } from './faultForm';
import {
  emptyImpairment,
  impairmentErrors,
  impairmentParams,
  impairmentToForm,
  netemFieldOf,
  type ImpairmentField,
  type ImpairmentForm,
} from './netemForm';
import { builtinProfileNames } from './profiles';

export interface ProfileForm {
  name: string;
  description: string;
  impairmentOn: boolean;
  impairment: ImpairmentForm;
  mtuOn: boolean;
  mtuSize: string;
  mtuMode: MtuMode;
}

export type ProfileField = ImpairmentField | 'name' | 'description' | 'parts' | 'mtuSize' | 'mtuMode';

export function emptyProfileForm(): ProfileForm {
  return { name: '', description: '', impairmentOn: true, impairment: emptyImpairment(), mtuOn: false, mtuSize: '1280', mtuMode: 'icmp' };
}

/** The form of a profile; a part it does not have stays off. */
export function profileToForm(p: Profile): ProfileForm {
  const f = emptyProfileForm();
  f.name = p.name;
  f.description = p.description ?? '';
  f.impairmentOn = !!p.parts.impairment;
  if (p.parts.impairment) impairmentToForm(p.parts.impairment, f.impairment);
  f.mtuOn = !!p.parts.mtu;
  if (p.parts.mtu) {
    f.mtuSize = String(p.parts.mtu.size);
    f.mtuMode = p.parts.mtu.mode ?? 'icmp';
  }
  return f;
}

/** The parts of `base` the editor does not make (DNS, TLS): kept as they are. */
export function keptParts(base: Profile | undefined): Pick<ProfileParts, 'dns' | 'tls'> {
  const out: Pick<ProfileParts, 'dns' | 'tls'> = {};
  if (base?.parts.dns) out.dns = base.parts.dns;
  if (base?.parts.tls) out.tls = base.parts.tls;
  return out;
}

/** The profile of the form, with the parts of `base` the editor does not make. */
export function formToProfile(f: ProfileForm, base?: Profile): Profile {
  const parts: ProfileParts = { ...keptParts(base) };
  if (f.impairmentOn) parts.impairment = impairmentParams(f.impairment);
  if (f.mtuOn) parts.mtu = mtuOf(f.mtuSize, f.mtuMode, base?.parts.mtu);
  return { name: f.name.trim(), ...(f.description.trim() ? { description: f.description.trim() } : {}), parts };
}

const uuidLike = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

/**
 * The problems of the form, by field: the name (the pattern, not a built-in profile's, not another
 * profile's — names are unique, case-insensitive), at least one part, the impairment part's fields
 * (as in the fault dialog) and the MTU size. `others` are the names of the other profiles.
 */
export function profileFormErrors(f: ProfileForm, others: string[], base?: Profile): Partial<Record<ProfileField, string>> {
  const e: Partial<Record<ProfileField, string>> = {};
  const name = f.name.trim();
  const lower = name.toLowerCase();
  if (!name) e.name = 'Give the profile a name.';
  else if (!nameRegExp.test(name) || uuidLike.test(name)) e.name = 'Letters, digits, dot, dash and underscore; starting with a letter or digit; at most 63.';
  else if (builtinProfileNames.includes(lower)) e.name = 'This is the name of a built-in profile.';
  else if (others.some((o) => o.toLowerCase() === lower)) e.name = 'Another profile has this name.';
  if (f.description.trim().length > 500) e.description = 'At most 500 characters.';
  const kept = keptParts(base);
  if (!f.impairmentOn && !f.mtuOn && !kept.dns && !kept.tls) e.parts = 'Give the profile an impairment or an MTU part.';
  if (f.impairmentOn) Object.assign(e, impairmentErrors(f.impairment));
  if (f.mtuOn) {
    const size = Number(f.mtuSize);
    if (!f.mtuSize.trim() || !Number.isInteger(size) || size < 552 || size > 1500) e.mtuSize = 'A packet size from 552 to 1500 bytes.';
  }
  return e;
}

/**
 * The form's field of a problem the gateway reported for a profile, by the pointer below the profile
 * ("name", "parts/impairment/upload/jitter", "parts/mtu/size"); undefined for the profile as a whole.
 */
export function profileFieldOf(rest: string, each = false): ProfileField | undefined {
  const segments = rest.split('/').filter(Boolean);
  if (segments[0] === 'name') return 'name';
  if (segments[0] === 'description') return 'description';
  if (segments[0] !== 'parts') return undefined;
  if (segments.length === 1) return 'parts';
  if (segments[1] === 'mtu') return segments[2] === 'mode' ? 'mtuMode' : 'mtuSize';
  if (segments[1] !== 'impairment') return 'parts';
  const field = netemFieldOf(segments);
  if (!field) return undefined;
  return each && segments.includes('download') ? `download.${field}` : field;
}
