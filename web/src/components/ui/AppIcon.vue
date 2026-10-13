<script setup lang="ts">
// 16 px inline stroke icons, 1.5 px stroke (plan §2.17). Decorative: the text next to them says it.
// `name` is a key of the icon set below.
defineProps<{ name: string; size?: number }>();
</script>

<script lang="ts">
const paths: Record<string, string> = {
  overview:
    '<rect x="2" y="2" width="5" height="5" rx="1"/><rect x="9" y="2" width="5" height="5" rx="1"/><rect x="2" y="9" width="5" height="5" rx="1"/><rect x="9" y="9" width="5" height="5" rx="1"/>',
  devices:
    '<rect x="3.5" y="3.5" width="9" height="9" rx="1.5"/><path d="M6 1.5v2M10 1.5v2M6 12.5v2M10 12.5v2M1.5 6h2M1.5 10h2M12.5 6h2M12.5 10h2"/>',
  networks:
    '<circle cx="8" cy="3" r="1.8"/><circle cx="3" cy="13" r="1.8"/><circle cx="13" cy="13" r="1.8"/><path d="M7 4.5 4 11.5M9 4.5l3 7M5 13h6"/>',
  rules: '<path d="M2 4h12M2 8h12M2 12h12"/><circle cx="5" cy="4" r="1.5"/><circle cx="11" cy="8" r="1.5"/><circle cx="7" cy="12" r="1.5"/>',
  faults: '<path d="M8 1.5 14.5 13.5h-13Z"/><path d="M8 6v3.5M8 11.5v.5"/>',
  profiles: '<path d="M8 2 14 5 8 8 2 5Z"/><path d="M2 8.5 8 11.5 14 8.5M2 11.5 8 14.5 14 11.5"/>',
  scenarios: '<path d="M2 3h4M2 8h7M2 13h5"/><path d="M11 10.5v5l4-2.5Z"/>',
  captures: '<circle cx="8" cy="8" r="6"/><circle cx="8" cy="8" r="2.5"/>',
  diagnostics: '<path d="M1 8h3l2-5 4 10 2-5h3"/>',
  activity: '<path d="M6 3h8M6 8h8M6 13h8"/><circle cx="2.5" cy="3" r="1"/><circle cx="2.5" cy="8" r="1"/><circle cx="2.5" cy="13" r="1"/>',
  search: '<circle cx="7" cy="7" r="4.5"/><path d="m10.5 10.5 3.5 3.5"/>',
  close: '<path d="m4 4 8 8M12 4l-8 8"/>',
  warning: '<path d="M8 1.5 14.5 13.5h-13Z"/><path d="M8 6v3.5M8 11.5v.5"/>',
  offline: '<path d="M2 6.5a9 9 0 0 1 12 0M4.5 9a5.5 5.5 0 0 1 7 0M7 11.5a2 2 0 0 1 2 0"/><path d="M2 2l12 12"/>',
  logout: '<path d="M6 2.5H3v11h3M10 5l3 3-3 3M13 8H6"/>',
  check: '<path d="m3 8.5 3 3 7-7"/>',
  chevron: '<path d="m6 3 5 5-5 5"/>',
};
</script>

<template>
  <svg
    :width="size ?? 16"
    :height="size ?? 16"
    viewBox="0 0 16 16"
    fill="none"
    stroke="currentColor"
    stroke-width="1.5"
    stroke-linecap="round"
    stroke-linejoin="round"
    aria-hidden="true"
    focusable="false"
    v-html="paths[name]"
  />
</template>
