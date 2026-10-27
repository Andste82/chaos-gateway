<script setup lang="ts">
// A device's recent DNS queries (plan §2.17 "Device detail"; M26, `docs/open-items.md` P3-M12-02): the newest the DNS proxy logged of
// it (`GET /dns/queries?device=`), with the name asked, the type, the answer and the time it took. An answer a DNS fault changed
// is orange and says so; one that is no answer with an address (NXDOMAIN, SERVFAIL, a timeout) is red, with its code in words.
import { computed } from 'vue';
import { useDnsQueries } from '../../composables/gateway';
import { useNow } from '../../composables/useNow';
import { dnsAnswerText, dnsQueryTone } from '../../lib/flows';
import { formatAgo, formatClock } from '../../lib/format';
import LoadError from '../ui/LoadError.vue';
import SectionCard from '../ui/SectionCard.vue';

const props = defineProps<{ device: { id: string; name: string } }>();

const now = useNow();
const queries = useDnsQueries(() => ({ device: props.device.id, limit: 10 }));
const items = computed(() => queries.data.value ?? []);

const tones = { normal: '', fault: 'text-fault-text', block: 'text-block-text' } as const;
/** What stands under the answer: the response code when it is no success and not what the answer says already, and that a DNS fault changed the answer. */
const note = (q: { rcode: string; answers?: string[]; fault?: string }) =>
  [q.rcode !== 'NOERROR' && dnsAnswerText(q) !== q.rcode ? q.rcode : '', q.fault ? 'changed by a DNS fault' : ''].filter(Boolean).join(' · ');
</script>

<template>
  <SectionCard title="Recent DNS queries">
    <LoadError v-if="queries.error.value && !queries.data.value" :error="queries.error.value" what="The DNS queries are not loaded" :retry="queries.refetch" />
    <p v-else-if="queries.isPending.value && !queries.data.value" class="m-0 text-sm text-text-2" role="status">Loading the DNS queries…</p>
    <p v-else-if="items.length === 0" class="m-0 text-sm text-text-2" data-testid="device-dns-empty">
      The DNS proxy has logged no query of {{ device.name }} yet. It logs the queries of devices whose resolver is the gateway.
    </p>
    <div v-else class="overflow-x-auto">
      <div role="table" aria-label="Recent DNS queries" class="min-w-[560px]" data-testid="device-dns-queries">
        <div role="row" class="data-head grid h-10 grid-cols-[84px_minmax(0,1.4fr)_52px_minmax(0,1.4fr)_64px] items-center gap-3 px-1">
          <span role="columnheader">Time</span>
          <span role="columnheader">Name</span>
          <span role="columnheader">Type</span>
          <span role="columnheader">Answer</span>
          <span role="columnheader">Took</span>
        </div>
        <div
          v-for="(q, i) in items"
          :key="`${q.time}/${q.name}/${q.type}/${i}`"
          role="row"
          class="data-row grid grid-cols-[84px_minmax(0,1.4fr)_52px_minmax(0,1.4fr)_64px] items-center gap-3 px-1 py-1.5 text-[13px]"
          :data-tone="dnsQueryTone(q)"
          data-testid="device-dns-query"
        >
          <span role="cell" class="text-text-2"
            ><span class="mono">{{ formatClock(q.time) }}</span
            ><span class="block text-xs text-text-3">{{ formatAgo(q.time, now) }}</span></span
          >
          <span role="cell" class="mono min-w-0 truncate" :title="q.name">{{ q.name }}</span>
          <span role="cell" class="mono text-text-2">{{ q.type }}</span>
          <span role="cell" class="min-w-0" :class="tones[dnsQueryTone(q)]">
            <span class="mono block truncate" :title="dnsAnswerText(q)" data-testid="device-dns-answer">{{ dnsAnswerText(q) }}</span>
            <span class="flex flex-wrap items-center gap-x-2 text-xs">
              <span v-if="note(q)" data-testid="device-dns-note">{{ note(q) }}</span>
              <span v-if="q.cached" class="text-text-3">cached</span>
            </span>
          </span>
          <span role="cell" class="mono text-text-2">{{ q.duration_ms !== undefined ? `${q.duration_ms < 10 ? q.duration_ms.toFixed(1) : Math.round(q.duration_ms)} ms` : '—' }}</span>
        </div>
      </div>
    </div>
  </SectionCard>
</template>
