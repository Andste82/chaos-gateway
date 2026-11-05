<script setup lang="ts">
// The *Time* panel of the device detail (plan §2.17, §2.18; M34): what the gateway's NTP server did with the device's queries — the
// last query (when, how it was answered), the server the device asked where the gateway knows it, the time it served (and its
// difference from the correct time), the time fault that shaped it, and the time faults that are active for the device now. The
// queries come without an event of the page, so the panel reads them every 5 s.
import { computed } from 'vue';
import { useGetDeviceNtp } from '../../api/generated/hooks/devices/devices';
import type { Overlay } from '../../api/generated/model';
import { useNames, useProfiles } from '../../composables/gateway';
import { useNow } from '../../composables/useNow';
import { overlayChips } from '../../lib/faults';
import { formatAgo, formatClock, formatCount, instantText } from '../../lib/format';
import { activeTimeFaults, ntpAnswerText, ntpTone, ntsNote, offsetText, serverText } from '../../lib/ntp';
import FaultChip from '../ui/FaultChip.vue';
import LoadError from '../ui/LoadError.vue';
import SectionCard from '../ui/SectionCard.vue';

const props = defineProps<{
  device: { id: string; name: string };
  /** The overlays that act on the device (its own and those of its group and network). */
  overlays: readonly Overlay[];
}>();

const now = useNow();
const { names } = useNames();
// the built-in `time-*` profiles are the usual way to start a time fault: their activation is an overlay of the kind `profile`
const profiles = useProfiles();
const ntp = useGetDeviceNtp(() => props.device.id, { query: { refetchInterval: 5_000 } });
const data = computed(() => ntp.data.value);
const last = computed(() => data.value?.last_query);
const faults = computed(() => activeTimeFaults(props.overlays, profiles.data.value ?? []));
/** The activation of a profile is a time fault only if its profile has a time part: that is not known without the profile list. */
const profilesKnown = computed(() => !!profiles.data.value);
/** The fault that shaped the last answer, as the overlays name it (it may have been removed since). */
const lastFault = computed(() => (last.value?.fault ? props.overlays.find((o) => o.id === last.value!.fault) : undefined));
const recent = computed(() => (data.value?.recent ?? []).slice(0, 5));
const nts = computed(() => (data.value ? ntsNote(data.value) : undefined));
const tones = { normal: '', fault: 'text-fault-text', block: 'text-block-text' } as const;
</script>

<template>
  <SectionCard title="Time">
    <LoadError v-if="ntp.error.value && !data" :error="ntp.error.value" what="The NTP queries are not loaded" :retry="ntp.refetch" />
    <p v-else-if="ntp.isPending.value && !data" class="m-0 text-sm text-text-2" role="status">Loading the NTP queries…</p>
    <template v-else-if="data">
      <p v-if="!last && (data.nts_ke || data.dropped)" class="m-0 text-sm text-text-2" data-testid="device-ntp-empty">
        The NTP server has answered no query of {{ device.name }} yet, only the ones counted below.
      </p>
      <p v-else-if="!last" class="m-0 text-sm text-text-2" data-testid="device-ntp-empty">
        The NTP server has seen no query of {{ device.name }} yet. A device asks the gateway when its NTP server is the gateway (DHCP hands it out), or when its queries are redirected.
      </p>
      <dl v-else class="m-0 grid grid-cols-[130px_1fr] gap-x-4 gap-y-2 text-sm" data-testid="device-ntp">
        <dt class="text-text-2">Last query</dt>
        <dd class="m-0">
          <span class="mono" data-testid="device-ntp-time">{{ formatClock(last.time) }}</span>
          <span class="text-text-3"> · {{ formatAgo(last.time, now) }}</span>
        </dd>
        <dt class="text-text-2">Server asked</dt>
        <dd class="mono m-0" data-testid="device-ntp-server">{{ serverText(last.server) }}</dd>
        <dt class="text-text-2">Answer</dt>
        <dd class="m-0" :class="tones[ntpTone(last)]" data-testid="device-ntp-answer">{{ ntpAnswerText(last) }}</dd>
        <template v-if="last.served_time">
          <dt class="text-text-2">Time served</dt>
          <dd class="m-0">
            <span class="mono" data-testid="device-ntp-served">{{ instantText(last.served_time) }}</span>
            <span v-if="last.offset_ms !== undefined" class="mono ml-2" :class="last.fault ? 'text-fault-text' : 'text-text-3'" data-testid="device-ntp-offset"
              >{{ offsetText(last.offset_ms) }}</span
            >
          </dd>
        </template>
        <template v-if="last.fault">
          <dt class="text-text-2">Time fault</dt>
          <dd class="m-0 flex flex-wrap gap-1.5" data-testid="device-ntp-fault">
            <FaultChip v-for="c in lastFault ? overlayChips(lastFault, names.profile, names) : ['a time fault that is gone']" :key="c" :text="c" />
          </dd>
        </template>
      </dl>

      <div class="text-[13px] text-text-2" data-testid="device-ntp-active">
        <span class="mr-2">Active time faults</span>
        <span v-if="!profilesKnown" class="text-text-3" role="status" data-testid="device-ntp-active-unknown">{{
          profiles.error.value ? 'not known: the profiles are not loaded, so the activation of a time profile is not recognized' : 'loading the profiles…'
        }}</span>
        <span v-if="faults.length === 0 && profilesKnown" class="text-text-3" data-testid="device-ntp-active-none">none: the server answers with the correct time</span>
        <span v-else-if="faults.length > 0" class="inline-flex flex-wrap gap-1.5">
          <template v-for="o in faults" :key="o.id">
            <FaultChip v-for="c in overlayChips(o, names.profile, names)" :key="`${o.id}/${c}`" :text="c" />
          </template>
        </span>
      </div>
      <p v-if="data.queries > 0 || data.silenced || data.dropped" class="m-0 text-xs text-text-3" data-testid="device-ntp-counters">
        {{ formatCount(data.queries) }} {{ data.queries === 1 ? 'query' : 'queries' }}<template v-if="data.silenced"> · {{ formatCount(data.silenced) }} left unanswered by a fault</template
        ><template v-if="data.dropped"> · {{ formatCount(data.dropped) }} dropped (sampled, at most one a second)</template>
      </p>
      <p v-if="nts" class="m-0 text-[13px] text-block-text" data-testid="device-ntp-nts">{{ nts }}</p>

      <ol v-if="recent.length > 1" class="mono m-0 flex list-none flex-col gap-0.5 p-0 text-xs" aria-label="Recent NTP queries" data-testid="device-ntp-recent">
        <li v-for="(q, i) in recent" :key="`${q.time}/${i}`" :class="tones[ntpTone(q)]">
          <span class="text-text-3">{{ formatClock(q.time) }}</span>
          <span class="ml-2">{{ ntpAnswerText(q) }}</span>
          <span v-if="q.offset_ms !== undefined && q.served_time" class="ml-2">{{ offsetText(q.offset_ms) }}</span>
        </li>
      </ol>
    </template>
  </SectionCard>
</template>
