<script setup lang="ts">
// The root: the page of the route, the toasts, and — when the gateway did not answer the very first
// question (setup or session) — a page that says so and tries again.
import { ref } from 'vue';
import { useRouter } from 'vue-router';
import ToastHost from './components/ui/ToastHost.vue';
import { useConnectionStore } from './stores/connection';
import { useSessionStore } from './stores/session';

const session = useSessionStore();
const connection = useConnectionStore();
const router = useRouter();
const retrying = ref(false);

async function retry() {
  retrying.value = true;
  try {
    await session.bootstrap(true);
    await router.replace(router.currentRoute.value.fullPath);
  } catch {
    // still unreachable
  } finally {
    retrying.value = false;
  }
}
</script>

<template>
  <div v-if="session.status === 'unknown' && connection.offline" class="flex min-h-screen items-center justify-center p-8">
    <div class="card max-w-md text-center" role="alert" data-testid="unreachable">
      <h1 class="m-0 text-xl font-semibold">Chaos Gateway</h1>
      <p class="text-text-2">The gateway does not answer. Check that it runs and that this address is on its management network.</p>
      <button type="button" class="btn btn-primary" :disabled="retrying" @click="retry">{{ retrying ? 'Trying…' : 'Try again' }}</button>
    </div>
  </div>
  <RouterView v-else />
  <ToastHost />
</template>
