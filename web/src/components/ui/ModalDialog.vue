<script setup lang="ts">
// Modal dialog (plan §2.17): Reka UI's dialog (focus trap, Escape, aria-modal, labelled by its
// title), styled as a card. The footer slot holds the actions; a form's own action row uses the
// `dialog-actions` class, so it stays in view while a tall form scrolls; the row brings the dialog's
// bottom padding, so the dialog drops its own while it has one (#80). On close the focus goes back
// to the control that opened the dialog, or to the page's heading when that control is gone.
import { DialogClose, DialogContent, DialogDescription, DialogOverlay, DialogPortal, DialogRoot, DialogTitle } from 'reka-ui';
import { useFocusReturn } from '../../lib/focus';
import AppIcon from './AppIcon.vue';

defineProps<{ title: string; description?: string; width?: string }>();
const open = defineModel<boolean>('open', { required: true });
const focus = useFocusReturn();
</script>

<template>
  <DialogRoot v-model:open="open">
    <DialogPortal>
      <DialogOverlay class="fixed inset-0 z-40 bg-black/60" />
      <DialogContent
        class="fixed top-1/2 left-1/2 z-50 flex max-h-[calc(100vh-48px)] -translate-x-1/2 -translate-y-1/2 flex-col gap-4 overflow-auto rounded-[10px] border border-border-strong bg-surface p-6 text-text shadow-2xl has-[.dialog-actions]:pb-0"
        :style="{ width: width ?? '520px', maxWidth: 'calc(100vw - 32px)' }"
        @open-auto-focus="focus.onOpen"
        @close-auto-focus="focus.onClose"
      >
        <div class="flex items-start justify-between gap-4">
          <div>
            <DialogTitle class="m-0 text-xl font-semibold">{{ title }}</DialogTitle>
            <DialogDescription v-if="description" class="mt-1 text-[13px] text-text-2">{{ description }}</DialogDescription>
          </div>
          <DialogClose class="btn btn-sm" aria-label="Close"><AppIcon name="close" /></DialogClose>
        </div>
        <slot />
        <div v-if="$slots.footer" class="dialog-actions"><slot name="footer" /></div>
      </DialogContent>
    </DialogPortal>
  </DialogRoot>
</template>
