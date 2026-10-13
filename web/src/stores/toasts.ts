import { defineStore } from 'pinia';
import { ref } from 'vue';

export type ToastKind = 'info' | 'success' | 'fault' | 'error';

export interface Toast {
  id: number;
  kind: ToastKind;
  title: string;
  detail?: string;
}

/** The toasts of plan §2.17 ("Revision N applied and verified", "overlay expired"). */
export const useToastStore = defineStore('toasts', () => {
  const toasts = ref<Toast[]>([]);
  let next = 1;

  function push(kind: ToastKind, title: string, detail?: string): number {
    const id = next++;
    toasts.value = [...toasts.value, { id, kind, title, detail }].slice(-5);
    return id;
  }

  function dismiss(id: number) {
    toasts.value = toasts.value.filter((t) => t.id !== id);
  }

  return { toasts, push, dismiss };
});
