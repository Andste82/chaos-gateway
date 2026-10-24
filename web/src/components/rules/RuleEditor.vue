<script setup lang="ts">
// The editor of a configured access rule (plan §2.17 "Access rules"): IF from / to / protocol /
// ports, THEN Allow · Drop · Reject · TCP reset and "also cut existing connections". Every edit goes
// into the configuration draft (the unapplied-changes bar), nothing reaches the gateway before
// Preview & apply. Each message stands under its field; the gateway's refusal of a candidate is
// shown there too. From offers the remote networks too (P3-M13-08); To has the destinations of the
// model (a link's or a client's prefixes are an address or prefix, or the WireGuard network).
import { computed, useId } from 'vue';
import type { AccessAction, AccessRule, Destination, Protocol, RejectType } from '../../api/generated/model';
import { useNames } from '../../composables/gateway';
import { scopeOf, targetOf } from '../../lib/faultForm';
import { describeScope } from '../../lib/faults';
import { accessLabels, formatPorts, parsePorts, rejectLabels, ruleErrors } from '../../lib/rules';
import { remoteScopeOptions } from '../../lib/scopes';
import { useDraftStore } from '../../stores/draft';
import ScopeSelect from '../ui/ScopeSelect.vue';
import SegmentedControl from '../ui/SegmentedControl.vue';
import SwitchToggle from '../ui/SwitchToggle.vue';

const props = defineProps<{ id: string; rule: AccessRule }>();

const draft = useDraftStore();
const { names, devices, networks } = useNames();
const uid = useId();
const fid = (f: string) => `${uid}-${f}`;
const eid = (f: string) => `${uid}-${f}-error`;

function update(change: Partial<AccessRule>) {
  const next: AccessRule = { ...props.rule, ...change };
  for (const k of Object.keys(next) as (keyof AccessRule)[]) if (next[k] === undefined) delete next[k];
  draft.setRule(props.id, next);
}

const sources = computed(() => {
  const n = names.value;
  const out: { value: string; label: string }[] = [{ value: 'global', label: 'Everything' }];
  for (const net of networks.data.value ?? []) out.push({ value: `network:${net.id}`, label: `Network · ${n.network?.(net.id) ?? net.id}` });
  for (const [gid, g] of Object.entries(draft.baseConfig?.groups ?? {})) out.push({ value: `group:${gid}`, label: `Group · ${g.name}` });
  // a configured rule names configured devices only
  for (const d of devices.data.value ?? []) if (d.origin !== 'discovered') out.push({ value: `device:${d.id}`, label: `Device · ${d.name}` });
  // the client networks of a WireGuard client, a link's prefixes or a prefix inside them (P3-M13-08)
  out.push(...remoteScopeOptions(networks.data.value ?? []));
  return out;
});
const sourceName = computed(() => describeScope(props.rule.source, names.value));

const source = computed({
  get: () => targetOf(props.rule.source),
  set: (v: string) => update({ source: scopeOf(v) }),
});

type DestKind = 'any' | 'uplink' | 'network' | 'cidr' | 'hostname';
const destKind = computed<DestKind>({
  get: () => {
    const d = props.rule.destination;
    if (!d) return 'any';
    if (d.uplink) return 'uplink';
    if (d.network !== undefined) return 'network';
    if (d.hostname !== undefined) return 'hostname';
    return 'cidr';
  },
  set: (k: DestKind) => {
    const d: Record<DestKind, Destination | undefined> = {
      any: undefined,
      uplink: { uplink: true },
      network: { network: networks.data.value?.[0]?.id ?? '' },
      cidr: { cidr: '' },
      hostname: { hostname: '' },
    };
    update({ destination: d[k] });
  },
});
const destValue = computed({
  get: () => props.rule.destination?.network ?? props.rule.destination?.cidr ?? props.rule.destination?.hostname ?? '',
  set: (v: string) => update({ destination: { [destKind.value]: v.trim() } as Destination }),
});

const portsText = computed({
  get: () => draft.portsText[props.id] ?? formatPorts(props.rule),
  set: (text: string) => {
    const p = parsePorts(text);
    if (p.error) {
      draft.setPortsText(props.id, text);
      return;
    }
    draft.setPortsText(props.id, undefined);
    update({ ports: p.ports, port_ranges: p.port_ranges });
  },
});

const protocol = computed({
  get: () => props.rule.protocol ?? 'any',
  set: (v: Protocol) => update({ protocol: v, ...(v !== 'tcp' && v !== 'any' ? { cut_existing: undefined } : {}) }),
});

const action = computed({
  get: () => props.rule.action,
  set: (a: string) =>
    update({
      action: a as AccessAction,
      ...(a === 'allow' ? { cut_existing: undefined } : {}),
      ...(a !== 'reject' ? { reject_with: undefined } : {}),
      // a TCP reset selects TCP alone (the gateway refuses it on any other protocol); ports stay valid
      ...(a === 'reset' && props.rule.protocol !== 'tcp' ? { protocol: 'tcp' as Protocol } : {}),
    }),
});

const enabled = computed({
  get: () => props.rule.enabled !== false,
  set: (v: boolean) => update({ enabled: v ? undefined : false }),
});

const errors = computed(() => ({ ...(draft.serverErrors[props.id] ?? {}), ...ruleErrors(props.rule, draft.portsText[props.id]) }) as Record<string, string | undefined>);
/** The fields the editor shows a message under; a message of any other field shows above them. */
const shownFields = ['name', 'rule', 'source', 'destination', 'protocol', 'ports', 'action', 'reject_with', 'cut_existing'];
const otherErrors = computed(() =>
  Object.entries(errors.value)
    .filter(([k, v]) => v && !shownFields.includes(k))
    .map(([k, v]) => `${k.replace(/_/g, ' ')}: ${v}`),
);
const accessOptions = (['allow', 'drop', 'reject', 'reset'] as const).map((a) => ({ value: a, label: accessLabels[a] }));
const blocking = computed(() => props.rule.action !== 'allow');
const cutPossible = computed(() => !props.rule.protocol || props.rule.protocol === 'tcp' || props.rule.protocol === 'any');
</script>

<template>
  <div class="flex flex-col gap-3" data-testid="rule-editor">
    <div class="grid grid-cols-[minmax(0,1fr)_auto] items-end gap-3">
      <div>
        <label class="label" :for="fid('name')">Name</label>
        <input
          :id="fid('name')"
          class="input"
          :value="rule.name ?? ''"
          :aria-invalid="!!errors.name"
          :aria-describedby="errors.name ? eid('name') : undefined"
          @input="update({ name: ($event.target as HTMLInputElement).value || undefined })"
        />
      </div>
      <SwitchToggle v-model="enabled" :label="enabled ? 'Enabled' : 'Disabled'" class="mb-2" />
    </div>
    <p v-if="errors.name" :id="eid('name')" class="field-error -mt-2">{{ errors.name }}</p>
    <p v-if="errors.rule" class="field-error -mt-1" role="alert">{{ errors.rule }}</p>
    <p v-for="m in otherErrors" :key="m" class="field-error -mt-1" role="alert" data-testid="rule-other-error">{{ m }}</p>

    <div class="text-xs font-semibold tracking-[0.06em] text-text-2">IF TRAFFIC</div>
    <div class="grid grid-cols-2 gap-3">
      <div>
        <label class="label" :for="fid('from')">From</label>
        <ScopeSelect
          :id="fid('from')"
          v-model="source"
          :options="sources"
          :current-label="sourceName"
          :invalid="!!errors.source"
          :describedby="errors.source ? eid('from') : undefined"
        />
        <p v-if="errors.source" :id="eid('from')" class="field-error">{{ errors.source }}</p>
      </div>
      <div>
        <label class="label" :for="fid('to')">To</label>
        <select :id="fid('to')" v-model="destKind" class="input">
          <option value="any">Anywhere</option>
          <option value="uplink">Uplink (Internet)</option>
          <option value="network">A network</option>
          <option value="cidr">An address or prefix</option>
          <option value="hostname">A host name (DNS-derived)</option>
        </select>
      </div>
      <div v-if="destKind === 'network' || destKind === 'cidr' || destKind === 'hostname'" class="col-span-2">
        <label class="label" :for="fid('dest')">{{ { network: 'Network', cidr: 'Address or prefix', hostname: 'Host name' }[destKind] }}</label>
        <select
          v-if="destKind === 'network'"
          :id="fid('dest')"
          v-model="destValue"
          class="input"
          :aria-invalid="!!errors.destination"
          :aria-describedby="errors.destination ? eid('dest') : undefined"
        >
          <option v-for="n in networks.data.value ?? []" :key="n.id" :value="n.id">{{ names.network?.(n.id) ?? n.id }}</option>
        </select>
        <input
          v-else
          :id="fid('dest')"
          class="input mono"
          :value="destValue"
          :placeholder="destKind === 'cidr' ? '203.0.113.0/24' : 'broker.example.com'"
          :aria-invalid="!!errors.destination"
          :aria-describedby="errors.destination ? eid('dest') : undefined"
          @input="destValue = ($event.target as HTMLInputElement).value"
        />
        <p v-if="errors.destination" :id="eid('dest')" class="field-error">{{ errors.destination }}</p>
      </div>
      <div>
        <label class="label" :for="fid('proto')">Protocol</label>
        <select
          :id="fid('proto')"
          v-model="protocol"
          class="input"
          :aria-invalid="!!errors.protocol"
          :aria-describedby="errors.protocol ? eid('proto') : undefined"
        >
          <option value="any">Any</option>
          <option value="tcp">TCP</option>
          <option value="udp">UDP</option>
          <option value="icmp">ICMP</option>
        </select>
        <p v-if="errors.protocol" :id="eid('proto')" class="field-error">{{ errors.protocol }}</p>
      </div>
      <div>
        <label class="label" :for="fid('ports')">Ports</label>
        <input
          :id="fid('ports')"
          v-model.lazy="portsText"
          class="input mono"
          placeholder="any"
          :aria-invalid="!!errors.ports"
          :aria-describedby="errors.ports ? eid('ports') : undefined"
          @input="draft.setPortsText(id, ($event.target as HTMLInputElement).value)"
        />
        <p v-if="errors.ports" :id="eid('ports')" class="field-error">{{ errors.ports }}</p>
      </div>
    </div>
    <p v-if="destKind === 'hostname'" class="m-0 text-xs leading-relaxed text-text-2" data-testid="hostname-hint">
      Host name matched via DNS answers (best effort): devices using other resolvers are not covered; other sites on the same IP are affected too.
    </p>

    <div class="mt-1 text-xs font-semibold tracking-[0.06em] text-text-2">THEN</div>
    <SegmentedControl
      v-model="action"
      label="Access"
      :options="accessOptions"
    />
    <p v-if="errors.action" class="field-error -mt-1" role="alert">{{ errors.action }}</p>
    <div v-if="rule.action === 'reject'">
      <label class="label" :for="fid('reject')">Answer with ICMP</label>
      <select
        :id="fid('reject')"
        class="input"
        :aria-invalid="!!errors.reject_with"
        :aria-describedby="errors.reject_with ? eid('reject') : undefined"
        :value="rule.reject_with ?? 'port_unreachable'"
        @change="update({ reject_with: (($event.target as HTMLSelectElement).value as RejectType) === 'port_unreachable' ? undefined : (($event.target as HTMLSelectElement).value as RejectType) })"
      >
        <option v-for="(label, k) in rejectLabels" :key="k" :value="k">{{ label }}</option>
      </select>
      <p v-if="errors.reject_with" :id="eid('reject')" class="field-error">{{ errors.reject_with }}</p>
    </div>
    <div v-if="blocking">
      <label class="flex items-center gap-2 text-[13px]" :class="{ 'text-text-2': !cutPossible }">
        <input
          type="checkbox"
          :checked="!!rule.cut_existing"
          :disabled="!cutPossible"
          :aria-invalid="!!errors.cut_existing"
          :aria-describedby="[eid('cut-hint'), errors.cut_existing ? eid('cut') : ''].filter(Boolean).join(' ')"
          @change="update({ cut_existing: ($event.target as HTMLInputElement).checked || undefined })"
        />
        Also cut existing connections
      </label>
      <!-- under the label, not beside it, and saying why when it is off -->
      <p :id="eid('cut-hint')" class="m-0 mt-0.5 ml-6 text-[13px] text-text-2" data-testid="cut-hint">
        {{ cutPossible ? 'Otherwise only new connections are affected.' : 'Only TCP connections can be cut; this rule matches another protocol.' }}
      </p>
      <p v-if="errors.cut_existing" :id="eid('cut')" class="field-error">{{ errors.cut_existing }}</p>
    </div>
  </div>
</template>
