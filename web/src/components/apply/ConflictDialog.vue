<script setup lang="ts">
// The concurrent-change dialog (plan §2.17, optimistic locking): the configuration was changed by
// someone else (another tab, an API token, a test) while this browser was editing, so the gateway
// refused the change against the old base (409 revision_conflict). It says who changed what, and
// offers to reload and reapply the user's change on the new revision, or to discard it. There is no
// "overwrite theirs" (plan §2.17: the prototype's three-way dialog is superseded).
import { useQuery } from '@tanstack/vue-query';
import { computed } from 'vue';
import { diffRevision, getActiveRevision } from '../../api/generated/hooks/revisions/revisions';
import LoadError from '../ui/LoadError.vue';
import ModalDialog from '../ui/ModalDialog.vue';

const props = defineProps<{
  /** The revision the user's change was based on. */
  base: number;
  /** The user's change in words. */
  yours: string[];
  busy?: boolean;
  error?: string;
}>();
const open = defineModel<boolean>('open', { required: true });
const emit = defineEmits<{ reapply: []; discard: [] }>();

const active = useQuery({
  queryKey: ['revisions', 'conflict', computed(() => props.base)],
  queryFn: ({ signal }) => getActiveRevision({ signal }),
  enabled: open,
  staleTime: 0,
});
const theirs = useQuery({
  queryKey: ['revisions', 'conflict-diff', computed(() => props.base), computed(() => active.data.value?.id)],
  queryFn: ({ signal }) => diffRevision(active.data.value!.id, { base: props.base }, { signal }),
  enabled: computed(() => open.value && !!active.data.value),
  staleTime: 0,
});

const who = computed(() => {
  const by = active.data.value?.created_by;
  if (!by) return 'someone else';
  if (by.type === 'token') return `token ${by.name ?? by.id}`;
  if (by.type === 'user') return by.name ?? by.id;
  return by.name ?? by.type;
});
</script>

<template>
  <ModalDialog
    v-model:open="open"
    title="The configuration was changed"
    :description="
      active.data.value
        ? `Revision ${active.data.value.id} was applied by ${who} while you were editing (your change is based on revision ${base}).`
        : `Another revision was applied while you were editing (your change is based on revision ${base}).`
    "
    width="600px"
  >
    <div data-testid="conflict-theirs">
      <div class="mb-1.5 text-xs font-semibold tracking-[0.06em] text-text-2">THEIR CHANGE</div>
      <p v-if="theirs.isLoading.value || active.isLoading.value" class="m-0 text-sm text-text-2">Loading what changed…</p>
      <LoadError v-else-if="active.error.value || theirs.error.value" :error="active.error.value ?? theirs.error.value" what="Their change is not loaded" />
      <ul v-else-if="theirs.data.value?.length" class="m-0 flex list-none flex-col gap-1.5 p-0">
        <li v-for="(c, i) in theirs.data.value" :key="i" class="rounded-md border border-border bg-surface-2 px-3 py-2 text-sm">
          <span class="mono mr-2 text-accent-text">{{ c.op === 'added' ? '+' : c.op === 'removed' ? '−' : '~' }}</span>{{ c.summary }}
        </li>
      </ul>
      <p v-else class="m-0 text-sm text-text-2">No change in the configuration's objects.</p>
    </div>
    <div data-testid="conflict-yours">
      <div class="mb-1.5 text-xs font-semibold tracking-[0.06em] text-text-2">YOUR CHANGE</div>
      <ul class="m-0 flex list-none flex-col gap-1.5 p-0">
        <li v-for="(c, i) in yours" :key="i" class="rounded-md border border-border bg-surface-2 px-3 py-2 text-sm">{{ c }}</li>
      </ul>
    </div>
    <p class="m-0 text-[13px] text-text-2">
      Reapplying puts your change on top of revision {{ active.data.value?.id ?? 'the new revision' }}; where both changed the same object, yours
      wins. You see the preview before anything is applied.
    </p>
    <p v-if="error" class="field-error" role="alert">{{ error }}</p>
    <template #footer>
      <button type="button" class="btn" :disabled="busy" @click="emit('discard')">Discard mine</button>
      <button type="button" class="btn btn-primary" :disabled="busy || !active.data.value" @click="emit('reapply')">
        {{ busy ? 'Reloading…' : 'Reload and reapply mine' }}
      </button>
    </template>
  </ModalDialog>
</template>
