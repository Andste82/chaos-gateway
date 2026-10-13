import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query';
import { createPinia } from 'pinia';
import { createApp } from 'vue';
import App from './App.vue';
import { ApiError, onReachable, onUnauthorized, onUnreachable } from './api/fetcher';
import { createAppRouter } from './router';
import { useConnectionStore } from './stores/connection';
import { useSessionStore } from './stores/session';
import './styles.css';

const pinia = createPinia();
const router = createAppRouter();
const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      // an answer of the API (404, 422) does not change on a retry; an unreachable gateway is
      // retried once, then the page shows the offline state and keeps the last data
      retry: (count, error) => !(error instanceof ApiError) && count < 1,
      staleTime: 2_000,
    },
  },
});

const app = createApp(App).use(pinia).use(router).use(VueQueryPlugin, { queryClient });

const connection = useConnectionStore(pinia);
onReachable(() => connection.answered());
onUnreachable(() => connection.unreachable());
onUnauthorized(() => {
  const session = useSessionStore(pinia);
  if (session.status !== 'authenticated') return;
  session.ended();
  queryClient.clear();
  void router.push({ name: 'login', query: { next: router.currentRoute.value.fullPath } });
});

app.mount('#app');
