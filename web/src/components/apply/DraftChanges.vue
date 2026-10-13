<script setup lang="ts">
// The configuration draft in the shell (plan §2.17 "Unapplied changes"): the sticky bar "N changes
// in M rules · not applied yet" with Discard and Preview & apply, the preview-and-apply drawer and
// the concurrent-change dialog. Preview & apply stays disabled while a rule of the draft is not
// valid, while a revision waits for confirmation and while the gateway does not answer.
import { computed, ref } from 'vue';
import { errorMessage } from '../../api/fetcher';
import { getActiveRevision } from '../../api/generated/hooks/revisions/revisions';
import { useGatewayState, useNames } from '../../composables/gateway';
import { useConnectionStore } from '../../stores/connection';
import { useDraftStore } from '../../stores/draft';
import StickyActionBar from '../ui/StickyActionBar.vue';
import ConflictDialog from './ConflictDialog.vue';
import PreviewDrawer from './PreviewDrawer.vue';

const draft = useDraftStore();
const state = useGatewayState();
const connection = useConnectionStore();
const { names } = useNames();

const invalidCount = computed(() => Object.keys(draft.invalid).length);
const pendingConfirm = computed(() => !!state.data.value?.pending_confirm);
const yours = computed(() => draft.changes(names.value).map((c) => `${c.kind === 'rule' ? 'Rule' : 'Fault'} ${c.name} ${c.field}: ${c.from} → ${c.to}`));

const conflictOpen = computed({
  get: () => !!draft.conflict,
  set: (v) => {
    if (!v) draft.conflict = undefined;
  },
});
const reloading = ref(false);
const reloadError = ref('');

async function reapply() {
  reloading.value = true;
  reloadError.value = '';
  try {
    draft.rebase(await getActiveRevision());
    draft.conflict = undefined;
    draft.drawerOpen = true;
  } catch (e) {
    reloadError.value = errorMessage(e);
  } finally {
    reloading.value = false;
  }
}

function discardMine() {
  draft.conflict = undefined;
  draft.discard();
}
</script>

<template>
  <StickyActionBar v-if="draft.dirty && !draft.drawerOpen" label="Unapplied changes">
    <span class="inline-flex items-center gap-2.5" data-testid="unapplied-bar">
      <span class="inline-block h-2 w-2 rounded-full bg-accent" aria-hidden="true" />
      {{ draft.summary(names) }} · not applied yet
      <span v-if="invalidCount" class="text-block-text">· {{ invalidCount === 1 ? 'a rule needs' : `${invalidCount} rules need` }} fixing first</span>
    </span>
    <template #actions>
      <button type="button" class="btn" @click="draft.discard()">Discard</button>
      <button
        type="button"
        class="btn btn-primary"
        :disabled="invalidCount > 0 || pendingConfirm || connection.offline"
        :title="pendingConfirm ? 'Another revision waits for confirmation' : undefined"
        @click="draft.drawerOpen = true"
      >
        Preview &amp; apply
      </button>
    </template>
  </StickyActionBar>
  <PreviewDrawer />
  <ConflictDialog
    v-if="draft.conflict"
    v-model:open="conflictOpen"
    :base="draft.conflict.base"
    :yours="yours"
    :busy="reloading"
    :error="reloadError"
    @reapply="reapply"
    @discard="discardMine"
  />
</template>
