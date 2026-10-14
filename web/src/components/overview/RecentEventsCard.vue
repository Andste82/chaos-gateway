<script setup lang="ts">
// Recent events (plan §2.17, Overview): what the event stream delivered since the page was opened,
// newest first; before that, the last entries of the audit log. An event about a device, a network
// or an overlay links to it (every item of the overview links to its detail).
import { computed } from 'vue';
import { useListAudit } from '../../api/generated/hooks/observability/observability';
import { formatClock } from '../../lib/format';
import { eventLink, objectLink } from '../../lib/links';
import { useEventStore } from '../../stores/events';
import LoadError from '../ui/LoadError.vue';
import SectionCard from '../ui/SectionCard.vue';

const events = useEventStore();
const audit = useListAudit({ limit: 8 });

interface Line {
  key: string;
  time: string;
  text: string;
  tone: 'normal' | 'fault' | 'block';
  link?: string;
}

const faultTypes = new Set(['overlay_created', 'overlay_updated', 'overlay_expired', 'dns_fault_triggered']);
const blockTypes = new Set(['apply_failed', 'revision_rolled_back', 'safe_mode', 'network_degraded']);

const lines = computed<Line[]>(() => {
  const live: Line[] = events.recent.slice(0, 12).map((e) => ({
    key: e.id,
    time: formatClock(e.time),
    text: e.message || e.type.replace(/_/g, ' '),
    tone: blockTypes.has(e.type) ? 'block' : faultTypes.has(e.type) ? 'fault' : 'normal',
    link: eventLink(e),
  }));
  if (live.length >= 6) return live;
  const past: Line[] = (audit.data.value?.items ?? []).map((a) => ({
    key: `audit-${a.id}`,
    time: formatClock(a.time),
    text: [a.action.replace(/[._]/g, ' '), a.revision ? `revision ${a.revision}` : '', a.actor ? `by ${a.actor.name ?? a.actor.id}` : '']
      .filter(Boolean)
      .join(' · '),
    tone: 'normal',
    link: objectLink(a.object?.kind, a.object?.id),
  }));
  return [...live, ...past].slice(0, 8);
});
</script>

<template>
  <SectionCard title="Recent events">
    <ol v-if="lines.length" class="mono m-0 list-none p-0 text-xs leading-[1.9]" aria-live="polite" data-testid="recent-events">
      <li v-for="l in lines" :key="l.key">
        <span class="text-text-3">{{ l.time }}</span>
        <RouterLink
          v-if="l.link"
          :to="l.link"
          class="ml-2 hover:underline"
          :class="l.tone === 'fault' ? 'text-fault-text' : l.tone === 'block' ? 'text-block-text' : 'text-text'"
          >{{ l.text }}</RouterLink
        >
        <span v-else class="ml-2" :class="{ 'text-fault-text': l.tone === 'fault', 'text-block-text': l.tone === 'block' }">{{ l.text }}</span>
      </li>
    </ol>
    <LoadError v-else-if="audit.error.value" :error="audit.error.value" what="The past events are not loaded" />
    <p v-else-if="audit.isLoading.value" class="m-0 text-sm text-text-2" role="status">Loading the events…</p>
    <p v-else class="m-0 text-sm text-text-2">Nothing happened yet.</p>
  </SectionCard>
</template>
