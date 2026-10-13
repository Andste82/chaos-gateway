<script setup lang="ts">
// Card (plan §2.17): a titled section of a page; the title labels it for screen readers.
import { useId } from 'vue';

defineProps<{ title: string }>();
const id = useId();
</script>

<template>
  <section class="card flex min-w-0 flex-col gap-3" :aria-labelledby="id">
    <div class="flex items-baseline justify-between gap-3">
      <h2 :id="id" class="card-title">{{ title }}</h2>
      <div v-if="$slots.actions" class="flex items-center gap-3 text-[13px]"><slot name="actions" /></div>
    </div>
    <slot />
  </section>
</template>
