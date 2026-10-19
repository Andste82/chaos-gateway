<script setup lang="ts">
// The navigation of plan §2.17. Screens of later milestones are listed, but not yet links, so the
// structure is visible from the start and nothing leads to an empty page.
import { useRouter } from 'vue-router';
import { useSystemInfo } from '../../composables/gateway';
import { useSessionStore } from '../../stores/session';
import AppIcon from '../ui/AppIcon.vue';

interface NavItem {
  label: string;
  icon: string;
  to?: string;
  later?: string;
}

const items: NavItem[] = [
  { label: 'Overview', icon: 'overview', to: '/' },
  { label: 'Devices', icon: 'devices', to: '/devices' },
  { label: 'Networks', icon: 'networks', to: '/networks' },
  { label: 'Access rules', icon: 'rules', to: '/rules' },
  { label: 'Faults', icon: 'faults', to: '/faults' },
  { label: 'Profiles', icon: 'profiles', to: '/profiles' },
  { label: 'Scenarios', icon: 'scenarios', to: '/scenarios' },
  { label: 'Captures', icon: 'captures', later: 'a later milestone' },
  { label: 'Diagnostics', icon: 'diagnostics', later: 'M24' },
  { label: 'Activity', icon: 'activity', later: 'a later milestone' },
];

const info = useSystemInfo();
const session = useSessionStore();
const router = useRouter();

async function logout() {
  await session.logout().catch(() => undefined);
  await router.push('/login');
}
</script>

<template>
  <nav aria-label="Main" class="flex w-[232px] shrink-0 flex-col gap-0.5 border-r border-[#1e252e] bg-sidebar px-3 py-5">
    <div class="flex items-center gap-2.5 px-2.5 pt-1 pb-5">
      <svg width="22" height="22" viewBox="0 0 22 22" fill="none" stroke="#FFA24C" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
        <rect x="1.5" y="1.5" width="19" height="19" rx="4" />
        <path d="M5 12h3l2-5 2.5 9 2-4H17" />
      </svg>
      <span class="text-[13px] font-semibold tracking-[0.1em]">CHAOS GATEWAY</span>
    </div>
    <template v-for="item in items" :key="item.label">
      <RouterLink
        v-if="item.to"
        :to="item.to"
        class="flex h-9 items-center gap-2.5 rounded-md px-2.5 text-sm text-text-2 no-underline hover:bg-surface hover:text-text"
        exact-active-class="!bg-surface-2 !text-text"
        :active-class="item.to === '/' ? '' : '!bg-surface-2 !text-text'"
      >
        <AppIcon :name="item.icon" />{{ item.label }}
      </RouterLink>
      <span
        v-else
        aria-disabled="true"
        :title="`Arrives with ${item.later}`"
        class="flex h-9 items-center gap-2.5 px-2.5 text-sm text-text-3"
      >
        <AppIcon :name="item.icon" />{{ item.label }}
      </span>
    </template>
    <div class="flex-1" />
    <div class="border-t border-[#1e252e] px-2.5 pt-3 text-xs leading-relaxed text-text-3">
      <div v-if="info.data.value" data-testid="host-line">
        {{ info.data.value.hostname }}<br />{{ info.data.value.os }}<template v-if="info.data.value.kernel">
          · kernel {{ info.data.value.kernel }}</template
        >
      </div>
      <button type="button" class="mt-2 inline-flex cursor-pointer items-center gap-2 border-0 bg-transparent p-0 text-xs text-text-2 hover:text-text" @click="logout">
        <AppIcon name="logout" />Log out
      </button>
    </div>
  </nav>
</template>
