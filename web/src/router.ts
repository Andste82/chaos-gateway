import { createRouter, createWebHistory, type Router } from 'vue-router';
import { useSessionStore } from './stores/session';

/**
 * The routes of M12. Every page but the setup wizard and the login lives in the shell. Before the
 * first page the router asks the gateway where the browser stands: an open setup leads to the
 * wizard, no session to the login (and back to the page asked for afterwards).
 */
export function createAppRouter(): Router {
  const router = createRouter({
    history: createWebHistory(),
    routes: [
      { path: '/setup', name: 'setup', component: () => import('./views/SetupWizard.vue') },
      { path: '/login', name: 'login', component: () => import('./views/LoginView.vue') },
      {
        path: '/',
        component: () => import('./components/layout/AppShell.vue'),
        children: [
          { path: '', name: 'overview', component: () => import('./views/OverviewView.vue') },
          { path: 'devices', name: 'devices', component: () => import('./views/DevicesView.vue') },
          { path: 'devices/:id', name: 'device', component: () => import('./views/DeviceDetailView.vue'), props: true },
          { path: ':rest(.*)*', name: 'not-found', component: () => import('./views/NotFoundView.vue') },
        ],
      },
    ],
    scrollBehavior(to) {
      return to.hash ? { el: to.hash } : { top: 0 };
    },
  });

  router.beforeEach(async (to, from) => {
    const session = useSessionStore();
    try {
      await session.bootstrap();
    } catch {
      return true; // the gateway does not answer: App shows that, whatever the route
    }
    switch (session.status) {
      case 'setup':
        return to.name === 'setup' ? true : { name: 'setup' };
      case 'anonymous':
        if (to.name === 'login') return true;
        return { name: 'login', query: to.fullPath !== '/' ? { next: to.fullPath } : {} };
      case 'authenticated':
        // the wizard stays on its page while it waits for the confirmation of the setup
        if (to.name === 'login' || (to.name === 'setup' && from.name !== 'setup')) return { name: 'overview' };
        return true;
      default:
        return true;
    }
  });

  return router;
}
