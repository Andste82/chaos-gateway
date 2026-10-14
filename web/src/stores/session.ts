import { defineStore } from 'pinia';
import { ref } from 'vue';
import { ApiError, setCsrfToken } from '../api/fetcher';
import { getSession, login as apiLogin, logout as apiLogout } from '../api/generated/hooks/auth/auth';
import { getSetup } from '../api/generated/hooks/system/system';
import type { Session } from '../api/generated/model';

/**
 * Where the browser stands with the gateway (plan §2.16): the first-start setup is open, nobody is
 * logged in, or the admin session is active. Server data stays in the query cache; this is the
 * one piece of state the router needs before it can show any page.
 */
export type SessionStatus = 'unknown' | 'setup' | 'anonymous' | 'authenticated';

const loggedOutHandlers = new Set<() => void>();

/**
 * fn runs when the admin logs out (also when the gateway's answer to the logout fails): what the
 * session read and edited must not stay in the page for the next login (the query cache, the draft;
 * Phase 3 audit). main.ts registers the cleanup.
 */
export function onLoggedOut(fn: () => void): () => void {
  loggedOutHandlers.add(fn);
  return () => loggedOutHandlers.delete(fn);
}

export const useSessionStore = defineStore('session', () => {
  const status = ref<SessionStatus>('unknown');
  const session = ref<Session | undefined>();
  let loading: Promise<void> | undefined;

  function adopt(s: Session) {
    session.value = s;
    setCsrfToken(s.csrf_token);
    status.value = 'authenticated';
  }

  function drop() {
    session.value = undefined;
    setCsrfToken(undefined);
    status.value = 'anonymous';
  }

  async function load(): Promise<void> {
    const setup = await getSetup();
    if (!setup.completed) {
      session.value = undefined;
      status.value = 'setup';
      return;
    }
    try {
      adopt(await getSession());
    } catch (e) {
      if (e instanceof ApiError && e.status === 401) {
        drop();
        return;
      }
      throw e;
    }
  }

  /** Finds out where the browser stands; concurrent callers share one round trip. */
  function bootstrap(force = false): Promise<void> {
    if (!force && status.value !== 'unknown') return Promise.resolve();
    loading ??= load().finally(() => (loading = undefined));
    return loading;
  }

  /** The gateway rolled the first-start setup back: it is open again, and no session belongs to it. */
  function setupReopened() {
    session.value = undefined;
    setCsrfToken(undefined);
    status.value = 'setup';
  }

  async function login(password: string): Promise<void> {
    adopt(await apiLogin({ username: 'admin', password }));
  }

  async function logout(): Promise<void> {
    try {
      await apiLogout();
    } finally {
      drop();
      for (const fn of loggedOutHandlers) fn();
    }
  }

  return { status, session, bootstrap, login, logout, ended: drop, setupReopened };
});
