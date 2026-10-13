<script setup lang="ts">
// The technical view (plan §2.17 "Technical view", M14 "compiled-state view"): what the kernel holds for
// Chaos Gateway, as the gateway reads it back — the nftables table with its counters, the tc trees with
// the counters of their queues, the bridges, policy rules and routes of its tables, and the WireGuard
// interfaces with their peers. Linux names appear here and in the preview's Linux changes only (D14);
// nothing here is needed for normal use. The counters are live: the page reads again every 5 s.
import { useQuery } from '@tanstack/vue-query';
import { computed, ref, useId } from 'vue';
import { getGetLinuxStateQueryKey, getLinuxState } from '../api/generated/hooks/system/system';
import PageHeader from '../components/layout/PageHeader.vue';
import LoadError from '../components/ui/LoadError.vue';
import SegmentedControl from '../components/ui/SegmentedControl.vue';
import { formatClock } from '../lib/format';
import { groupExcerpt, nftExcerpt, sectionLabels, sectionsOf, type SectionName } from '../lib/linuxView';
import { useApiScreen } from '../stores/apiPanel';

const linux = useQuery({ queryKey: getGetLinuxStateQueryKey(), queryFn: ({ signal }) => getLinuxState({ signal }), refetchInterval: 5_000 });
const uid = useId();

useApiScreen('the Linux view', () => [{ method: 'GET', path: '/state/linux', note: 'the kernel state, read back and rendered as text' }]);

const section = ref<SectionName | 'all'>('all');
const filter = ref('');
const options = [
  { value: 'all', label: 'All' },
  ...(['nftables', 'tc', 'routes', 'wireguard'] as const).map((s) => ({ value: s, label: sectionLabels[s] })),
];

const shown = computed(() =>
  sectionsOf(linux.data.value)
    .filter((s) => section.value === 'all' || s.name === section.value)
    .map((s) => {
      const t = filter.value.trim();
      if (!t) return s;
      return { name: s.name, text: s.name === 'nftables' ? nftExcerpt(s.text, [t]) : groupExcerpt(s.text, [t]) };
    }),
);
</script>

<template>
  <PageHeader title="Linux view">
    <template #breadcrumb>
      <nav aria-label="Breadcrumb" class="mb-1 text-[13px] text-text-2"><RouterLink to="/networks">Networks</RouterLink> / Linux view</nav>
    </template>
  </PageHeader>
  <p class="-mt-3 mb-0 text-[13px] text-text-2">
    What the kernel holds for Chaos Gateway, read back now<template v-if="linux.data.value">
      · revision {{ linux.data.value.revision }} · read at {{ formatClock(linux.data.value.read_at) }}</template
    >. Never needed for normal use.
  </p>

  <div class="card flex flex-wrap items-end gap-4">
    <SegmentedControl v-model="section" :options="options" label="Part of the kernel state" />
    <div class="min-w-[260px] flex-1">
      <label class="label" :for="`${uid}-filter`">Show only lines that name</label>
      <input :id="`${uid}-filter`" v-model="filter" class="input mono" placeholder="br-iot, 10.10.0.0/24, wg-lab-hub" />
    </div>
  </div>

  <p v-if="linux.isLoading.value" class="text-text-2">Reading the kernel state…</p>
  <LoadError v-else-if="linux.error.value && !linux.data.value" :error="linux.error.value" what="The kernel state is not read" :retry="linux.refetch" />
  <section v-for="s in shown" v-else :key="s.name" class="card flex flex-col gap-2" :aria-labelledby="`${uid}-${s.name}`" :data-testid="`linux-${s.name}`">
    <h2 :id="`${uid}-${s.name}`" class="card-title">{{ sectionLabels[s.name] }}</h2>
    <pre v-if="s.text.trim()" class="mono m-0 overflow-auto rounded-lg border border-border bg-bg p-3 text-xs leading-relaxed whitespace-pre text-[#d9dee5]">{{ s.text }}</pre>
    <p v-else class="m-0 text-[13px] text-text-2">{{ filter.trim() ? 'No line names it.' : 'Nothing of Chaos Gateway’s.' }}</p>
  </section>
</template>
