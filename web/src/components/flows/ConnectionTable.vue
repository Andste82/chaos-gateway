<script setup lang="ts">
// The table of the connections the gateway tracks (plan §2.17 "Flows"; M26): what each carries, where it goes, its state, the bytes it
// moved in both directions and how long it has lasted, with "Explain", which opens the explain card of the Faults screen for the
// connection. The Flows screen shows it whole, with the device and the source; the device detail shows the device's own connections
// without them (a connection that came in through a port forward says where it came from).
import { computed } from 'vue';
import type { Flow } from '../../api/generated/model';
import { destinationText, explainQuery, explainRequest, serviceLabel, sourceText, stateText } from '../../lib/flows';
import { formatAgo, formatBytes } from '../../lib/format';

const props = defineProps<{
  items: Flow[];
  deviceName: (id: string) => string | undefined;
  networkName?: (id: string) => string | undefined;
  now: Date;
  compact?: boolean;
}>();

const cols = computed(() =>
  props.compact
    ? 'grid-cols-[84px_minmax(0,1fr)_96px_64px_64px_76px_64px]'
    : 'grid-cols-[96px_minmax(120px,1fr)_minmax(150px,1fr)_minmax(170px,1.3fr)_120px_80px_80px_88px_72px]',
);

const forwardedHint = 'Came in through a port forward: the source is a peer behind the uplink or the management network.';
</script>

<template>
  <div class="overflow-x-auto" :class="compact ? '' : '-mx-5'">
    <div role="table" aria-label="Connections" :aria-rowcount="items.length + 1" :class="compact ? 'min-w-[600px]' : 'min-w-[1000px]'" data-testid="connection-table">
      <div role="row" class="data-head grid h-10 items-center gap-3 px-4" :class="cols">
        <span role="columnheader">Service</span>
        <span v-if="!compact" role="columnheader">Device</span>
        <span v-if="!compact" role="columnheader">From</span>
        <span role="columnheader">To</span>
        <span role="columnheader">State</span>
        <span role="columnheader"><span aria-hidden="true">↑</span><span class="sr-only">Sent</span></span>
        <span role="columnheader"><span aria-hidden="true">↓</span><span class="sr-only">Received</span></span>
        <span role="columnheader">Age</span>
        <span role="columnheader"><span class="sr-only">Actions</span></span>
      </div>
      <div
        v-for="f in items"
        :key="f.id"
        role="row"
        class="data-row grid items-center gap-3 px-4 py-1.5 text-[13px]"
        :class="cols"
        data-testid="connection-row"
      >
        <span role="cell" data-testid="connection-service">{{ serviceLabel(f) }}</span>
        <span v-if="!compact" role="cell" class="min-w-0 truncate" data-testid="connection-device">
          <RouterLink v-if="f.device" :to="`/devices/${f.device}`">{{ deviceName(f.device) ?? f.device }}</RouterLink>
          <span v-else class="text-text-3">unknown host</span>
          <span v-if="networkName && f.network && networkName(f.network)" class="block truncate text-xs text-text-3">{{ networkName(f.network) }}</span>
        </span>
        <span v-if="!compact" role="cell" class="mono min-w-0 truncate text-text-2" :title="sourceText(f)" data-testid="connection-from">{{ sourceText(f) }}</span>
        <span role="cell" class="min-w-0" data-testid="connection-to">
          <span class="mono block truncate" :title="f.dst_hostname ? `${f.dst_hostname} (${f.dst})` : f.dst">{{ destinationText(f) }}</span>
          <span v-if="f.forwarded" class="flex flex-wrap items-center gap-x-2 text-xs text-text-3">
            <span class="rounded border border-accent-strong px-1.5 text-accent-text" :title="forwardedHint" data-testid="connection-forwarded">FORWARDED</span>
            <span v-if="compact" class="mono">from {{ sourceText(f) }}</span>
          </span>
        </span>
        <span role="cell" class="text-text-2" data-testid="connection-state">{{ stateText(f) }}</span>
        <span role="cell" class="mono" data-testid="connection-up">{{ formatBytes(f.upload?.bytes ?? 0) }}</span>
        <span role="cell" class="mono" data-testid="connection-down">{{ formatBytes(f.download?.bytes ?? 0) }}</span>
        <span role="cell" class="text-text-3" data-testid="connection-age">{{ f.started_at ? formatAgo(f.started_at, now) : '—' }}</span>
        <span role="cell" class="text-right">
          <RouterLink :to="{ path: '/faults', query: explainQuery(explainRequest(f)) }" :aria-label="`Explain the connection to ${destinationText(f)}`">Explain</RouterLink>
        </span>
      </div>
    </div>
  </div>
</template>
