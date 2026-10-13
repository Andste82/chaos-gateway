<script setup lang="ts">
// The concurrent-change dialog (plan §2.17, optimistic locking): the configuration was changed by
// someone else (another tab, an API token, a test) while this browser was editing, so the gateway
// refused the change against the old base (409 revision_conflict). It says who changed what, and
// offers to reload and reapply the user's change on the new revision, or to discard it. There is no
// "overwrite theirs" (plan §2.17: the prototype's three-way dialog is superseded).
import { useQuery } from '@tanstack/vue-query';
import { computed } from 'vue';
import type { Actor } from '../../api/generated/model';
import { diffRevision, getActiveRevision, listRevisions } from '../../api/generated/hooks/revisions/revisions';
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

function actor(by: Actor | undefined): string {
  if (!by) return 'someone else';
  if (by.type === 'token') return `token ${by.name ?? by.id}`;
  if (by.type === 'user') return by.name ?? by.id;
  return by.name ?? by.type;
}
const who = computed(() => actor(active.data.value?.created_by));

/** More than one revision may have come after the base: the diff covers them all, so all are named. */
const several = computed(() => !!active.data.value && active.data.value.id - props.base > 1);
const between = useQuery({
  queryKey: ['revisions', 'conflict-between', computed(() => props.base), computed(() => active.data.value?.id)],
  queryFn: ({ signal }) => listRevisions({ limit: 100 }, { signal }),
  enabled: computed(() => open.value && several.value),
  staleTime: 0,
});

function list(xs: string[]): string {
  return xs.length > 1 ? `${xs.slice(0, -1).join(', ')} and ${xs.at(-1)}` : (xs[0] ?? '');
}

const description = computed(() => {
  const rev = active.data.value;
  const based = `your change is based on revision ${props.base}`;
  if (!rev) return `Another revision was applied while you were editing (${based}).`;
  if (!several.value) return `Revision ${rev.id} was applied by ${who.value} while you were editing (${based}).`;
  // the revisions applied after the base up to the active one (candidates that were never applied have ids too)
  const applied = (between.data.value?.items ?? []).filter((r) => r.id > props.base && r.id <= rev.id && r.applied_at).sort((a, b) => a.id - b.id);
  if (applied.length > 1) {
    const ids = list(applied.map((r) => String(r.id)));
    const authors = list([...new Set(applied.map((r) => actor(r.created_by)))]);
    return `Revisions ${ids} were applied by ${authors} while you were editing (${based}); their change below covers them all.`;
  }
  if (applied.length === 1 && applied[0]!.id === rev.id) return `Revision ${rev.id} was applied by ${who.value} while you were editing (${based}).`;
  return `Revisions up to ${rev.id} were applied while you were editing, the last by ${who.value} (${based}); their change below covers them all.`;
});
</script>

<template>
  <ModalDialog
    v-model:open="open"
    title="The configuration was changed"
    :description="description"
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
      Reapplying puts your change on top of revision {{ active.data.value?.id ?? 'the new revision' }}: what only they changed stays as they made
      it, and where both changed the same field of an object, yours wins. You see the preview before anything is applied.
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
