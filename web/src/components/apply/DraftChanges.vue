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
import type { Configuration } from '../../api/generated/model';
import { useDraftStore, type DraftChange } from '../../stores/draft';
import { useToastStore } from '../../stores/toasts';
import StickyActionBar from '../ui/StickyActionBar.vue';
import ConflictDialog from './ConflictDialog.vue';
import PreviewDrawer from './PreviewDrawer.vue';

const draft = useDraftStore();
const toasts = useToastStore();
const state = useGatewayState();
const connection = useConnectionStore();
const { names } = useNames();

const invalidCount = computed(() => Object.keys(draft.invalid).length);
const pendingConfirm = computed(() => !!state.data.value?.pending_confirm);
const kinds = { rule: 'Rule', fault: 'Fault', device: 'Device', client: 'WireGuard client', matrix: 'Access' };
const inWords = (c: DraftChange) => `${kinds[c.kind]} ${c.name} ${c.field}: ${c.from} → ${c.to}`;
const yours = computed(() => draft.changes(names.value).map(inWords));
const overlaps = (theirs: Configuration) => draft.overlaps(theirs, names.value).map(inWords);

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
    const rev = await getActiveRevision();
    draft.rebase(rev);
    draft.conflict = undefined;
    // their revision may already hold everything I changed: then there is nothing to apply
    if (draft.dirty) draft.drawerOpen = true;
    else {
      draft.drawerOpen = false;
      toasts.push('info', `Your changes are already in revision ${rev.id}`);
    }
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
    :overlaps="overlaps"
    :busy="reloading"
    :error="reloadError"
    @reapply="reapply"
    @discard="discardMine"
  />
</template>
