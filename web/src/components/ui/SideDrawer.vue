<script setup lang="ts">
// Side drawer (plan §2.17): a dialog that slides in from the right, e.g. the preview of a change
// ("dry run · nothing changed yet"). Keyboard access as in every dialog: focus trap, Escape, and on
// close the focus back on the control that opened it, or on the page's heading when that is gone.
import { DialogClose, DialogContent, DialogDescription, DialogOverlay, DialogPortal, DialogRoot, DialogTitle } from 'reka-ui';
import { useFocusReturn } from '../../lib/focus';
import AppIcon from './AppIcon.vue';

/** `busy`: an action runs that must finish here; the drawer cannot be closed until it has. */
defineProps<{ title: string; badge?: string; description?: string; busy?: boolean }>();
const open = defineModel<boolean>('open', { required: true });
const focus = useFocusReturn();
</script>

<template>
  <DialogRoot v-model:open="open">
    <DialogPortal>
      <DialogOverlay class="fixed inset-0 z-40 bg-black/50" />
      <DialogContent
        class="fixed top-0 right-0 z-50 flex h-full w-[520px] max-w-full flex-col gap-4 overflow-auto border-l border-border-strong bg-surface p-6 text-text shadow-2xl"
        @open-auto-focus="focus.onOpen"
        @close-auto-focus="focus.onClose"
      >
        <div class="flex items-start justify-between gap-4">
          <div>
            <DialogTitle class="m-0 text-xl font-semibold">{{ title }}</DialogTitle>
            <span
              v-if="badge"
              class="mt-2 inline-block rounded border border-accent-strong bg-accent-tint px-2 py-0.5 text-xs text-accent-text"
              >{{ badge }}</span
            >
            <DialogDescription v-if="description" class="mt-1 text-[13px] text-text-2">{{ description }}</DialogDescription>
          </div>
          <DialogClose class="btn btn-sm" aria-label="Close" :disabled="busy"><AppIcon name="close" /></DialogClose>
        </div>
        <div class="flex-1"><slot /></div>
        <div v-if="$slots.footer" class="flex justify-end gap-2 border-t border-border pt-4"><slot name="footer" /></div>
      </DialogContent>
    </DialogPortal>
  </DialogRoot>
</template>
