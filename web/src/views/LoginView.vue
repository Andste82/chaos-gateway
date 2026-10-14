<script setup lang="ts">
// Login (plan §2.16): one admin account; the session is an HTTP-only cookie.
import { computed, onBeforeUnmount, ref } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import { ApiError, errorMessage } from '../api/fetcher';
import { formatRemaining } from '../lib/format';
import { useSessionStore } from '../stores/session';

const session = useSessionStore();
const router = useRouter();
const route = useRoute();

const password = ref('');
const error = ref('');
const busy = ref(false);

// After repeated failures the gateway refuses logins for a while (429 with Retry-After: 5 s, doubling up
// to 15 min): the form says how long and offers no new attempt until then.
const lockedUntil = ref<number>();
const now = ref(Date.now());
let ticker: ReturnType<typeof setInterval> | undefined;
const lockedMs = computed(() => (lockedUntil.value === undefined ? 0 : Math.max(0, lockedUntil.value - now.value)));

function lock(seconds: number) {
  lockedUntil.value = Date.now() + seconds * 1000;
  now.value = Date.now();
  clearInterval(ticker);
  ticker = setInterval(() => {
    now.value = Date.now();
    if (lockedMs.value > 0) return;
    clearInterval(ticker);
    lockedUntil.value = undefined;
    error.value = '';
  }, 250);
}
onBeforeUnmount(() => clearInterval(ticker));

const shownError = computed(() => (lockedMs.value > 0 ? `Too many failed attempts. Try again in ${formatRemaining(lockedMs.value)}.` : error.value));

async function submit() {
  if (lockedMs.value > 0) return;
  error.value = '';
  if (!password.value) {
    error.value = 'Enter the admin password.';
    return;
  }
  busy.value = true;
  try {
    await session.login(password.value);
    const next = typeof route.query.next === 'string' && route.query.next.startsWith('/') ? route.query.next : '/';
    await router.replace(next);
  } catch (e) {
    if (e instanceof ApiError && e.status === 401) error.value = 'Wrong password.';
    else if (e instanceof ApiError && e.status === 429 && e.retryAfter !== undefined) lock(e.retryAfter);
    else error.value = errorMessage(e); // a 429 without Retry-After: its detail says how long to wait
  } finally {
    busy.value = false;
  }
}
</script>

<template>
  <div class="flex min-h-screen items-center justify-center bg-bg p-8">
    <form class="card flex w-[380px] max-w-full flex-col gap-4" novalidate @submit.prevent="submit">
      <div>
        <h1 class="m-0 text-xl font-semibold">Log in to Chaos Gateway</h1>
        <p class="mt-1 mb-0 text-[13px] text-text-2">User <span class="mono">admin</span></p>
      </div>
      <div>
        <label class="label" for="password">Password</label>
        <input
          id="password"
          v-model="password"
          class="input"
          type="password"
          autocomplete="current-password"
          :aria-invalid="!!shownError"
          :aria-describedby="shownError ? 'login-error' : undefined"
          autofocus
        />
        <p v-if="shownError" id="login-error" class="field-error" role="alert">{{ shownError }}</p>
      </div>
      <button type="submit" class="btn btn-primary" :disabled="busy || lockedMs > 0">{{ busy ? 'Logging in…' : 'Log in' }}</button>
    </form>
  </div>
</template>
