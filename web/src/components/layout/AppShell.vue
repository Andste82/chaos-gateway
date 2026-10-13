<script setup lang="ts">
// The layout of a logged-in page (plan §2.17): navigation, the page, and the states every screen
// must handle above it — safe mode (red, full width), offline (live data greyed out with the time
// it was last updated) and commit-confirm (orange countdown). The event stream lives as long as
// the shell.
import { useQueryClient } from '@tanstack/vue-query';
import { computed, onMounted, onUnmounted, ref } from 'vue';
import { errorMessage } from '../../api/fetcher';
import { confirmRevision } from '../../api/generated/hooks/revisions/revisions';
import { startEventStream } from '../../composables/eventStream';
import { useGatewayState } from '../../composables/gateway';
import { formatClock } from '../../lib/format';
import { useConnectionStore } from '../../stores/connection';
import { useToastStore } from '../../stores/toasts';
import AppBanner from '../ui/AppBanner.vue';
import CountdownBanner from '../ui/CountdownBanner.vue';
import SidebarNav from './SidebarNav.vue';

const queryClient = useQueryClient();
const connection = useConnectionStore();
const toasts = useToastStore();
const state = useGatewayState();

let stop: (() => void) | undefined;
onMounted(() => (stop = startEventStream(queryClient)));
onUnmounted(() => stop?.());

const pending = computed(() => state.data.value?.pending_confirm);
const confirming = ref(false);

async function confirm() {
  const rev = pending.value?.revision;
  if (rev === undefined) return;
  confirming.value = true;
  try {
    await confirmRevision(rev);
    toasts.push('success', `Revision ${rev} confirmed`, 'It is the last known good revision now.');
    await queryClient.invalidateQueries({ queryKey: ['state'] });
  } catch (e) {
    toasts.push('error', `Revision ${rev} could not be confirmed`, errorMessage(e));
  } finally {
    confirming.value = false;
  }
}

function expired() {
  // the gateway rolls back at the deadline; the event says so, the state shows the result
  void queryClient.invalidateQueries({ queryKey: ['state'] });
}
</script>

<template>
  <div class="flex h-screen overflow-hidden bg-bg">
    <SidebarNav />
    <div class="flex min-w-0 flex-1 flex-col">
      <AppBanner v-if="state.data.value?.safe_mode" kind="safe-mode" title="Safe mode: forwarding is disabled.">
        {{ state.data.value.safe_mode_reason || 'The configuration could not be applied at start.' }}
        Only management access works.
        <a href="https://github.com/Andste82/chaos-gateway/blob/main/docs/plan.md#39-updates-and-recovery" target="_blank" rel="noopener"
          >How to recover</a
        >
      </AppBanner>
      <AppBanner v-if="connection.offline" kind="offline" title="The gateway does not answer.">
        Live data is not updated.
        <span data-testid="last-updated">Last updated {{ connection.lastUpdated ? formatClock(connection.lastUpdated) : 'never' }}.</span>
      </AppBanner>
      <CountdownBanner
        v-if="pending?.deadline"
        :deadline="pending.deadline"
        :text="`Revision ${pending.revision} is applied and waits for confirmation; without it the gateway rolls back.`"
        @expired="expired"
      >
        <button type="button" class="btn btn-primary btn-sm" :disabled="confirming || connection.offline" @click="confirm">
          {{ confirming ? 'Confirming…' : 'Confirm' }}
        </button>
      </CountdownBanner>
      <main
        class="flex min-h-0 flex-1 flex-col gap-5 overflow-auto px-8 py-6"
        :class="{ stale: connection.offline }"
        :aria-busy="connection.offline"
        data-testid="page"
      >
        <RouterView />
      </main>
    </div>
  </div>
</template>
