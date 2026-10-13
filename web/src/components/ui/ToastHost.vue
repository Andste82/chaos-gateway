<script setup lang="ts">
// Toasts (plan §2.17): "Revision N applied and verified", "overlay expired". Reka UI's toast
// region is an aria-live list, so each one is announced; they close by themselves or with ×.
import { ToastClose, ToastDescription, ToastProvider, ToastRoot, ToastTitle, ToastViewport } from 'reka-ui';
import { useToastStore, type ToastKind } from '../../stores/toasts';
import AppIcon from './AppIcon.vue';

const store = useToastStore();

const tone: Record<ToastKind, string> = {
  info: 'border-border-strong',
  success: 'border-accent-strong',
  fault: 'border-fault-border',
  error: 'border-block-border',
};
</script>

<template>
  <ToastProvider :duration="6000" label="Notification">
    <ToastRoot
      v-for="t in store.toasts"
      :key="t.id"
      :type="t.kind === 'error' ? 'foreground' : 'background'"
      class="flex items-start gap-3 rounded-[10px] border bg-surface-2 p-3 text-sm text-text shadow-xl"
      :class="tone[t.kind]"
      :data-kind="t.kind"
      @update:open="(open: boolean) => !open && store.dismiss(t.id)"
    >
      <div class="flex-1">
        <ToastTitle class="font-medium" :class="{ 'text-fault-text': t.kind === 'fault', 'text-block-text': t.kind === 'error' }">{{
          t.title
        }}</ToastTitle>
        <ToastDescription v-if="t.detail" class="mt-1 text-[13px] text-text-2">{{ t.detail }}</ToastDescription>
      </div>
      <ToastClose class="cursor-pointer border-0 bg-transparent text-text-2 hover:text-text" aria-label="Dismiss"
        ><AppIcon name="close"
      /></ToastClose>
    </ToastRoot>
    <ToastViewport class="fixed right-6 bottom-6 z-50 m-0 flex w-[380px] max-w-[calc(100vw-32px)] list-none flex-col gap-2 p-0" />
  </ToastProvider>
</template>
