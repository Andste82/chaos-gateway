<script setup lang="ts">
// Login (plan §2.16): one admin account; the session is an HTTP-only cookie.
import { ref } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import { ApiError, errorMessage } from '../api/fetcher';
import { useSessionStore } from '../stores/session';

const session = useSessionStore();
const router = useRouter();
const route = useRoute();

const password = ref('');
const error = ref('');
const busy = ref(false);

async function submit() {
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
    else if (e instanceof ApiError && e.status === 429) error.value = 'Too many attempts. Wait a moment and try again.';
    else error.value = errorMessage(e);
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
          :aria-invalid="!!error"
          aria-describedby="login-error"
          autofocus
        />
        <p v-if="error" id="login-error" class="field-error" role="alert">{{ error }}</p>
      </div>
      <button type="submit" class="btn btn-primary" :disabled="busy">{{ busy ? 'Logging in…' : 'Log in' }}</button>
    </form>
  </div>
</template>
