import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query';
import { createPinia } from 'pinia';
import { createApp } from 'vue';
import App from './App.vue';
import { ApiError, onReachable, onUnauthorized, onUnavailable, onUnreachable } from './api/fetcher';
import { getSetup } from './api/generated/hooks/system/system';
import { createAppRouter } from './router';
import { useConnectionStore } from './stores/connection';
import { useSessionStore } from './stores/session';
import { useToastStore } from './stores/toasts';
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

// The gateway rolled the first-start setup back (revision 1 was not confirmed in time) while the
// browser was in the shell: it reopened the setup, dropped every session and refuses the API with
// 503 `unavailable`. Only GET /setup tells that apart from safe mode or a missing executor. The
// wizard follows a rollback on its own page itself.
let checkingSetup = false;
onUnavailable(() => {
  const session = useSessionStore(pinia);
  if (session.status !== 'authenticated' || checkingSetup || router.currentRoute.value.name === 'setup') return;
  checkingSetup = true;
  void getSetup()
    .then((setup) => {
      if (setup.completed || session.status !== 'authenticated') return;
      session.setupReopened();
      queryClient.clear();
      useToastStore(pinia).push(
        'error',
        'The setup was rolled back',
        'It was not confirmed in time. The setup is open again; a new setup token is in the container log.',
      );
      return router.replace({ name: 'setup' });
    })
    .catch(() => undefined)
    .finally(() => (checkingSetup = false));
});

app.mount('#app');
