<script setup lang="ts">
// A labelled text field (plan §2.17 "Validation"): its problem stands under it and is tied to it
// (aria-invalid, aria-describedby), otherwise its hint. `rows` makes it a text area.
import { useId } from 'vue';

defineProps<{ label: string; error?: string; hint?: string; placeholder?: string; mono?: boolean; rows?: number; disabled?: boolean }>();
const model = defineModel<string>({ required: true });
const uid = useId();
</script>

<template>
  <div>
    <label class="label" :for="uid">{{ label }}</label>
    <textarea
      v-if="rows"
      :id="uid"
      v-model="model"
      class="input !h-auto py-2"
      :class="mono ? 'mono' : ''"
      :rows="rows"
      :placeholder="placeholder"
      :disabled="disabled"
      :aria-invalid="!!error"
      :aria-describedby="error ? `${uid}-error` : hint ? `${uid}-hint` : undefined"
    />
    <input
      v-else
      :id="uid"
      v-model="model"
      class="input"
      :class="mono ? 'mono' : ''"
      :placeholder="placeholder"
      :disabled="disabled"
      :aria-invalid="!!error"
      :aria-describedby="error ? `${uid}-error` : hint ? `${uid}-hint` : undefined"
    />
    <p v-if="error" :id="`${uid}-error`" class="field-error">{{ error }}</p>
    <p v-else-if="hint" :id="`${uid}-hint`" class="mt-1.5 mb-0 text-xs text-text-3">{{ hint }}</p>
  </div>
</template>
