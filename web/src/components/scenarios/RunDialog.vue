<script setup lang="ts">
// Run a scenario (plan §2.10, §2.15; M19): the same as `chaosctl run … --set`. Parameters override fields of
// the scenario by dotted path (`target.device=esp32-42`, `steps.slow.fault.latency=300ms`), one per line. A
// scenario in the configuration is run by its name; a scenario with changes that are not applied yet is sent
// with the request (`inline`, stored with the run only, no revision is made), so a draft can be tried before it
// is applied. A busy target queues the run unless "Queue" is off, which refuses it. The run starts at once or waits in
// the queue; the dialog closes and the page follows it.
import { useQueryClient } from '@tanstack/vue-query';
import { computed, ref, useId, watch } from 'vue';
import { ApiError, errorMessage } from '../../api/fetcher';
import { createRun, runScenario } from '../../api/generated/hooks/runs/runs';
import type { Run, RunOptions, Scenario } from '../../api/generated/model';
import { useGatewayState } from '../../composables/gateway';
import { parseParameters, runRequestCalls } from '../../lib/runForm';
import { useApiPanelStore } from '../../stores/apiPanel';
import { useConnectionStore } from '../../stores/connection';
import { useToastStore } from '../../stores/toasts';
import ModalDialog from '../ui/ModalDialog.vue';
import DraftApi from './DraftApi.vue';

const props = defineProps<{
  scenario: { id: string; config: Scenario; /** The scenario has changes that are not applied yet: it is sent with the request. */ unapplied: boolean };
}>();
const open = defineModel<boolean>('open', { required: true });
const emit = defineEmits<{ started: [run: Run] }>();

const state = useGatewayState();
const connection = useConnectionStore();
const queryClient = useQueryClient();
const toasts = useToastStore();
const apiPanel = useApiPanelStore();
const uid = useId();

const parameters = ref('');
const queue = ref(true);
const busy = ref(false);
const error = ref('');
const paramError = ref('');

watch(open, (o) => {
  if (!o) {
    apiPanel.draft = undefined;
    return;
  }
  parameters.value = '';
  queue.value = true;
  error.value = '';
  paramError.value = '';
});

const parsed = computed(() => parseParameters(parameters.value));
const options = computed<RunOptions>(() => (Object.keys(parsed.value.parameters).length ? { parameters: parsed.value.parameters } : {}));
const blocked = computed(() => !!state.data.value?.pending_confirm || connection.offline);

// what Run sends, for the `</> API` panel
watch(
  [open, options, queue, () => props.scenario],
  () => {
    if (!open.value) return;
    apiPanel.draft = {
      title: 'Run scenario',
      calls: runRequestCalls(props.scenario.id, props.scenario.config, props.scenario.unapplied, options.value, queue.value),
    };
  },
  { deep: true, immediate: true },
);

async function submit() {
  if (busy.value) return;
  if (parsed.value.error) {
    paramError.value = parsed.value.error;
    return;
  }
  busy.value = true;
  error.value = '';
  paramError.value = '';
  try {
    const params = queue.value ? undefined : { queue: false };
    const run = props.scenario.unapplied
      ? await createRun({ inline: props.scenario.config, ...options.value }, params)
      : await runScenario(props.scenario.id, options.value, params);
    apiPanel.record('Run scenario', runRequestCalls(props.scenario.id, props.scenario.config, props.scenario.unapplied, options.value, queue.value));
    toasts.push('success', run.status === 'queued' ? `${run.scenario.name} is queued` : `${run.scenario.name} is running`);
    open.value = false;
    await queryClient.invalidateQueries({ queryKey: ['runs'] });
    emit('started', run);
  } catch (e) {
    if (e instanceof ApiError && e.status === 422 && e.problem?.errors?.length) {
      const atParam = e.problem.errors.filter((x) => x.path?.startsWith('/parameters'));
      if (atParam.length) paramError.value = atParam.map((x) => `${x.path?.replace('/parameters/', '')}: ${x.message}`).join(' ');
      const rest = e.problem.errors.filter((x) => !x.path?.startsWith('/parameters'));
      error.value = rest.length ? rest.map((x) => `${x.path ? x.path + ': ' : ''}${x.message}`).join(' ') : atParam.length ? '' : errorMessage(e);
    } else {
      error.value = errorMessage(e);
    }
  } finally {
    busy.value = false;
  }
}
</script>

<template>
  <ModalDialog v-model:open="open" title="Run scenario" width="600px">
    <form class="flex flex-col gap-4" novalidate data-testid="run-dialog" @submit.prevent="submit">
      <p class="m-0 text-sm" data-testid="run-dialog-what">
        Runs <span class="mono">{{ scenario.config.name }}</span
        >.
        <template v-if="scenario.unapplied">
          It has changes that are not applied yet: the run uses this version, sent with the request and stored with the run only. No revision is made.
        </template>
        <template v-else>The run executes the scenario as the active revision has it.</template>
      </p>
      <div>
        <label class="label" :for="`${uid}-params`">Parameters</label>
        <textarea
          :id="`${uid}-params`"
          v-model="parameters"
          class="input mono !h-auto py-2"
          rows="3"
          placeholder="target.device=esp32-42&#10;steps.slow.fault.latency=300ms"
          :aria-invalid="!!paramError"
          :aria-describedby="paramError ? `${uid}-params-error` : `${uid}-params-hint`"
        />
        <p v-if="paramError" :id="`${uid}-params-error`" class="field-error" data-testid="run-param-error">{{ paramError }}</p>
        <p v-else :id="`${uid}-params-hint`" class="mt-1.5 mb-0 text-xs text-text-3">
          Optional, one <span class="mono">path=value</span> per line, like <span class="mono">chaosctl run --set</span>. A step is named by its id.
        </p>
      </div>
      <label class="flex items-start gap-2 text-sm">
        <input v-model="queue" type="checkbox" class="mt-1" />
        <span>
          Wait in the queue when the target is busy
          <span class="block text-[13px] text-text-2">Off: a busy target refuses the run.</span>
        </span>
      </label>

      <DraftApi what="Run" />

      <p v-if="error" class="field-error m-0" role="alert" data-testid="run-error">{{ error }}</p>
      <p v-if="blocked" class="m-0 text-[13px] text-fault-text">
        {{ connection.offline ? 'The gateway does not answer.' : 'A revision waits for confirmation; run the scenario after it is resolved.' }}
      </p>

      <div class="dialog-actions">
        <button type="button" class="btn" @click="open = false">Cancel</button>
        <button type="submit" class="btn btn-primary" :disabled="busy || blocked">{{ busy ? 'Starting…' : 'Run' }}</button>
      </div>
    </form>
  </ModalDialog>
</template>
