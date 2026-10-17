<script setup lang="ts">
// First-start setup (plan §2.16, M12): the setup token from the container log, the uplink and the
// management interface, the test interfaces and their networks, the admin password, the preflight
// with its hints: for each chosen test interface the preflight's check names the netplan entry, the
// NetworkManager connection or the systemd-networkd file to remove (P3-M12-07, issue #64). The gateway applies revision 1 with commit-confirm; the wizard logs in with the
// new password and asks for the confirmation, so a choice that cuts the browser off rolls back.
import { useQuery } from '@tanstack/vue-query';
import { computed, onBeforeUnmount, reactive, ref, watch } from 'vue';
import { useRouter } from 'vue-router';
import { ApiError, UnreachableError, errorMessage } from '../api/fetcher';
import { confirmRevision } from '../api/generated/hooks/revisions/revisions';
import { completeSetup, getSetup } from '../api/generated/hooks/system/system';
import type { ApplyResult, HostInterface } from '../api/generated/model';
import CountdownBanner from '../components/ui/CountdownBanner.vue';
import StatusDot from '../components/ui/StatusDot.vue';
import SwitchToggle from '../components/ui/SwitchToggle.vue';
import {
  buildSetupRequest,
  managementURL,
  minPassword,
  proposeNetwork,
  validateInterfaces,
  validateNetworks,
  validatePassword,
  type NetworkDraft,
  interfaceDetail,
  interfaceOption,
} from '../lib/setup';
import { useSessionStore } from '../stores/session';
import { useToastStore } from '../stores/toasts';

const router = useRouter();
const session = useSessionStore();
const toasts = useToastStore();

const setup = useQuery({ queryKey: ['setup'], queryFn: ({ signal }) => getSetup({ signal }) });
const interfaces = computed<HostInterface[]>(() => setup.data.value?.interfaces ?? []);
const preflight = computed(() => setup.data.value?.preflight);
// the checks of the host (kernel, modules); the checks of the interfaces are shown where the test interfaces are chosen
const hostChecks = computed(() => (preflight.value?.checks ?? []).filter((c) => !c.interface));
const hostStatus = computed(() =>
  hostChecks.value.some((c) => c.status === 'fail') ? 'fail' : hostChecks.value.some((c) => c.status === 'warn') ? 'warn' : 'ok',
);

const steps = ['Setup token', 'Interfaces', 'Test networks', 'Admin password', 'Review'] as const;
const step = ref(0);

const token = ref('');
const draft = reactive({ uplink: '', gateway: '', management: '', tests: [] as string[], password: '', repeat: '' });
const networks = ref<NetworkDraft[]>([]);
// what the gateway said about a field (the token it refused)
const serverErrors = ref<Record<string, string>>({});
const submitError = ref('');
const submitErrors = ref<string[]>([]);
const busy = ref(false);
const result = ref<ApplyResult>();
const confirmState = ref<'login' | 'ready' | 'unreachable' | 'confirming' | 'expired'>('login');
const confirmError = ref('');
// the countdown ran out on this page; the gateway decides by its own clock whether it rolled back
const countdownOver = ref(false);
let rollbackPoll: ReturnType<typeof setInterval> | undefined;

// defaults from what the gateway saw: its default route's interface as uplink, an addressed other
// interface (or the uplink, the two-port case) as management
watch(
  () => setup.data.value,
  (s) => {
    if (!s || draft.uplink) return;
    const list = s.interfaces ?? [];
    draft.uplink = s.suggested_uplink?.name ?? list[0]?.name ?? '';
    draft.management = list.find((i) => i.name !== draft.uplink && (i.addresses?.length ?? 0) > 0)?.name ?? draft.uplink;
  },
  { immediate: true },
);

const candidates = computed(() => interfaces.value.filter((i) => i.name !== draft.uplink && i.name !== draft.management));

// the test networks follow the chosen test interfaces, keeping what was typed for each
watch(
  () => [...draft.tests],
  (tests) => {
    const kept = networks.value.filter((n) => tests.includes(n.iface));
    for (const iface of tests) {
      if (!kept.some((n) => n.iface === iface)) kept.push(proposeNetwork(iface, kept, takenPrefixes.value));
    }
    networks.value = tests.map((t) => kept.find((n) => n.iface === t)!);
  },
);
watch(
  () => [draft.uplink, draft.management],
  () => (draft.tests = draft.tests.filter((t) => t !== draft.uplink && t !== draft.management)),
);

const byName = (name: string) => interfaces.value.find((i) => i.name === name);
// the host's default route leaves through the proposed uplink; another one needs its router named
const uplinkHasDefault = computed(() => draft.uplink === setup.data.value?.suggested_uplink?.name);
const takenPrefixes = computed(() =>
  [byName(draft.uplink), byName(draft.management)].flatMap((i) => i?.addresses ?? []),
);
// the preflight's check of every chosen test interface the operating system configures: its text names the netplan
// file and entry (or the NetworkManager connection, the systemd-networkd file) and what to remove (plan §3.4, P3-M12-07)
const hints = computed(() =>
  draft.tests
    .map((t) => preflight.value?.checks.find((c) => c.interface === t && c.status !== 'ok'))
    .filter((c): c is NonNullable<typeof c> => !!c),
);

/** The problems of a step's fields, by field. */
function problems(s: number): Record<string, string> {
  if (s === 0) return token.value.trim() ? {} : { token: 'Enter the setup token from the container log.' };
  if (s === 1) {
    return validateInterfaces(
      { uplink: draft.uplink, gateway: draft.gateway, management: draft.management, networks: networks.value },
      setup.data.value?.suggested_uplink?.name,
      byName(draft.uplink)?.addresses ?? [],
    );
  }
  if (s === 2) return validateNetworks(networks.value, takenPrefixes.value);
  if (s === 3) return validatePassword(draft.password, draft.repeat);
  return {};
}

// Validation (plan §2.17): the message under the field, the button disabled until the step is
// valid. A step's messages appear once its button was pressed (not while the first field is still
// being typed) and then follow every change.
const attempted = reactive<boolean[]>([false, false, false, false, false]);
const errors = computed<Record<string, string>>(() => ({
  ...(attempted[step.value] ? problems(step.value) : {}),
  ...serverErrors.value,
}));
const stepInvalid = computed(() =>
  step.value < 4 ? Object.keys(errors.value).length > 0 : [0, 1, 2, 3].some((s) => Object.keys(problems(s)).length > 0),
);
// the gateway refused the token: the message stays until the token changes
watch(token, () => (serverErrors.value = {}));

function validate(s: number): boolean {
  attempted[s] = true;
  return Object.keys(problems(s)).length === 0 && Object.keys(serverErrors.value).length === 0;
}

function next() {
  if (validate(step.value)) step.value++;
}

function back() {
  step.value--;
}

async function apply() {
  submitError.value = '';
  submitErrors.value = [];
  for (let s = 0; s < 4; s++) {
    if (!validate(s)) {
      step.value = s;
      return;
    }
  }
  busy.value = true;
  try {
    const body = buildSetupRequest(
      {
        uplink: draft.uplink,
        gateway: draft.gateway,
        management: draft.management,
        networks: networks.value,
        password: draft.password,
        repeat: draft.repeat,
      },
      interfaces.value,
    );
    result.value = await completeSetup(body, { headers: { 'X-Setup-Token': token.value.trim() } });
    await afterSetup();
  } catch (e) {
    if (e instanceof ApiError && e.status === 401) {
      step.value = 0;
      serverErrors.value = { token: 'The setup token is not valid. Copy it again from the container log.' };
    } else if (e instanceof ApiError && e.code === 'setup_completed') {
      submitError.value = 'The setup was finished meanwhile. Log in instead.';
    } else {
      submitError.value = errorMessage(e);
      submitErrors.value = (e instanceof ApiError ? (e.problem?.errors ?? []) : []).map(
        (x) => `${x.path ?? ''} ${x.message ?? x.code ?? ''}`.trim(),
      );
    }
  } finally {
    busy.value = false;
  }
}

/** Log in with the new password; then the revision can be confirmed. */
async function afterSetup() {
  confirmError.value = '';
  try {
    await session.login(draft.password);
    confirmState.value = 'ready';
  } catch (e) {
    // the gateway answers only on its management network now: this address may be gone
    confirmState.value = e instanceof UnreachableError ? 'unreachable' : 'login';
    confirmError.value = errorMessage(e);
  }
}

async function confirm() {
  const rev = result.value?.revision;
  if (rev === undefined) return;
  confirmState.value = 'confirming';
  try {
    await confirmRevision(rev);
    stopRollbackPoll();
    toasts.push('success', 'Setup complete', `Revision ${rev} is confirmed and active.`);
    await router.replace('/');
  } catch (e) {
    if (e instanceof ApiError && (await rolledBack())) return;
    confirmState.value = 'ready';
    confirmError.value = errorMessage(e);
  }
}

/**
 * Whether the gateway rolled the setup back: the setup is open again. Only the gateway knows; a
 * countdown that ran out here does not prove it (the clocks, the network), so Confirm stays until
 * the gateway says so.
 */
async function rolledBack(): Promise<boolean> {
  try {
    if ((await getSetup()).completed) return false;
  } catch {
    return false;
  }
  stopRollbackPoll();
  confirmState.value = 'expired';
  confirmError.value = '';
  // the session of the login after the apply belongs to a setup that is gone
  session.setupReopened();
  return true;
}

/**
 * After a rollback: the wizard again, with a new token from the log and the interface and network
 * choices kept (the rollback is often about one of them, which can then be changed).
 */
function startAgain() {
  result.value = undefined;
  confirmState.value = 'login';
  confirmError.value = '';
  countdownOver.value = false;
  token.value = '';
  draft.password = '';
  draft.repeat = '';
  serverErrors.value = {};
  attempted.fill(false);
  step.value = 0;
  // the preflight and the interfaces may have changed meanwhile
  void setup.refetch();
}

function countdownEnded() {
  countdownOver.value = true;
  void rolledBack();
  rollbackPoll ??= setInterval(() => void rolledBack(), 2000);
}

function stopRollbackPoll() {
  clearInterval(rollbackPoll);
  rollbackPoll = undefined;
}
onBeforeUnmount(stopRollbackPoll);

const mgmtURL = computed(() => managementURL(byName(draft.management), window.location));
</script>

<template>
  <div class="min-h-screen bg-bg px-6 py-10">
    <div class="mx-auto flex max-w-3xl flex-col gap-6">
      <header>
        <h1 class="m-0 text-2xl font-semibold">Set up Chaos Gateway</h1>
        <p class="mt-1 mb-0 text-[13px] text-text-2">First start: choose the interfaces, the test networks and the admin password.</p>
      </header>

      <!-- after the apply: confirm, or the gateway rolls back -->
      <section v-if="result" class="card flex flex-col gap-4" aria-labelledby="confirm-title" data-testid="setup-confirm">
        <h2 id="confirm-title" class="m-0 text-xl font-semibold">Revision {{ result.revision }} is applied</h2>
        <CountdownBanner
          v-if="result.confirm_deadline && confirmState !== 'expired'"
          :deadline="result.confirm_deadline"
          text="Confirm that you can still reach the gateway, or it rolls the setup back."
          @expired="countdownEnded"
        >
          <button
            v-if="confirmState === 'ready' || confirmState === 'confirming'"
            type="button"
            class="btn btn-primary btn-sm"
            :disabled="confirmState === 'confirming'"
            @click="confirm"
          >
            {{ confirmState === 'confirming' ? 'Confirming…' : 'Confirm setup' }}
          </button>
          <button v-else-if="confirmState === 'login'" type="button" class="btn btn-sm" @click="afterSetup">Log in again</button>
        </CountdownBanner>
        <p v-if="countdownOver && confirmState !== 'expired'" class="m-0 text-sm" role="status" data-testid="countdown-over">
          The countdown has run out here. The gateway rolls back by its own clock; until it has, Confirm still works.
        </p>
        <p v-if="confirmState === 'unreachable'" class="m-0 text-sm" role="alert">
          The gateway now answers only on its management network.
          <template v-if="mgmtURL">Open <a :href="mgmtURL">{{ mgmtURL }}</a>, log in with the new password and confirm the revision</template
          ><template v-else>Open it on its management address, log in and confirm the revision</template> before the countdown ends.
        </p>
        <p v-if="confirmState === 'expired'" class="m-0 text-sm text-block-text" role="alert">
          The setup was not confirmed in time and was rolled back. The setup is open again with a new token in the container log.
        </p>
        <div v-if="confirmState === 'expired'">
          <button type="button" class="btn btn-primary" @click="startAgain">Start again</button>
        </div>
        <p v-if="confirmError && confirmState !== 'unreachable'" class="field-error" role="alert">{{ confirmError }}</p>
      </section>

      <template v-else>
        <ol class="m-0 flex list-none gap-2 p-0" aria-label="Steps">
          <li
            v-for="(s, i) in steps"
            :key="s"
            class="flex-1 rounded-md border px-3 py-2 text-[13px]"
            :class="i === step ? 'border-accent-strong bg-accent-tint text-text' : 'border-border text-text-2'"
            :aria-current="i === step ? 'step' : undefined"
          >
            <span class="mono" :class="i === step ? 'text-text-2' : 'text-text-3'">{{ i + 1 }}</span> {{ s }}
          </li>
        </ol>

        <p v-if="setup.isLoading.value" class="text-text-2">Reading the interfaces of this machine…</p>
        <p v-else-if="setup.error.value" class="field-error" role="alert">{{ errorMessage(setup.error.value) }}</p>

        <form v-else class="card flex flex-col gap-5" novalidate @submit.prevent="step < 4 ? next() : apply()">
          <!-- 1: token and preflight -->
          <template v-if="step === 0">
            <div>
              <label class="label" for="token">Setup token</label>
              <input
                id="token"
                v-model="token"
                class="input mono"
                autocomplete="off"
                spellcheck="false"
                :aria-invalid="!!errors.token"
                :aria-describedby="errors.token ? 'token-help token-error' : 'token-help'"
              />
              <p id="token-help" class="mt-1.5 mb-0 text-[13px] text-text-3">
                Printed to the log of the API container at first start: <span class="mono">docker compose logs api</span>
              </p>
              <p v-if="errors.token" id="token-error" class="field-error" role="alert">{{ errors.token }}</p>
            </div>
            <div v-if="preflight" data-testid="preflight">
              <h2 class="card-title mb-2">Preflight</h2>
              <StatusDot
                :tone="hostStatus === 'ok' ? 'normal' : hostStatus === 'warn' ? 'fault' : 'block'"
                :label="hostStatus === 'ok' ? 'This host is ready' : hostStatus === 'warn' ? 'Warnings' : 'Problems found'"
              />
              <ul class="mt-2 mb-0 flex list-none flex-col gap-2 p-0">
                <li v-for="c in hostChecks.filter((c) => c.status !== 'ok')" :key="c.id" class="text-sm">
                  <StatusDot :tone="c.status === 'warn' ? 'fault' : 'block'" :label="c.status === 'warn' ? 'Warning' : 'Failed'" />
                  {{ c.message }}
                  <pre v-if="c.fix" class="mono mt-1 mb-0 overflow-auto rounded bg-surface-2 p-2 text-xs whitespace-pre-wrap text-text-2">{{ c.fix }}</pre>
                </li>
              </ul>
            </div>
          </template>

          <!-- 2: interfaces -->
          <template v-if="step === 1">
            <div class="grid grid-cols-2 gap-4">
              <div>
                <label class="label" for="uplink">Uplink (towards the Internet)</label>
                <select
                  id="uplink"
                  v-model="draft.uplink"
                  class="input"
                  :aria-invalid="!!errors.uplink"
                  :aria-describedby="errors.uplink ? 'uplink-error' : undefined"
                >
                  <option v-for="i in interfaces" :key="i.name" :value="i.name">{{ interfaceOption(i) }}</option>
                </select>
                <p class="mono mt-1 mb-0 text-xs text-text-2" data-testid="uplink-detail">{{ interfaceDetail(interfaces.find((i) => i.name === draft.uplink)) }}</p>
                <p v-if="errors.uplink" id="uplink-error" class="field-error" role="alert">{{ errors.uplink }}</p>
                <label class="label mt-3" for="gateway">Router on the uplink{{ uplinkHasDefault ? ' (optional)' : '' }}</label>
                <input
                  id="gateway"
                  v-model="draft.gateway"
                  class="input mono"
                  :placeholder="uplinkHasDefault ? 'the default route of the host' : 'e.g. 192.168.1.1'"
                  :aria-invalid="!!errors.gateway"
                  :aria-describedby="errors.gateway ? 'gateway-error' : undefined"
                />
                <p v-if="errors.gateway" id="gateway-error" class="field-error" role="alert">{{ errors.gateway }}</p>
              </div>
              <div>
                <label class="label" for="management">Management (where you reach the UI)</label>
                <select
                  id="management"
                  v-model="draft.management"
                  class="input"
                  :aria-invalid="!!errors.management"
                  :aria-describedby="errors.management ? 'management-error' : undefined"
                >
                  <option v-for="i in interfaces" :key="i.name" :value="i.name">{{ interfaceOption(i) }}</option>
                </select>
                <p class="mono mt-1 mb-0 text-xs text-text-2" data-testid="management-detail">
                  {{ interfaceDetail(interfaces.find((i) => i.name === draft.management)) }}
                </p>
                <p v-if="errors.management" id="management-error" class="field-error" role="alert">{{ errors.management }}</p>
              </div>
            </div>
            <fieldset class="m-0 border-0 p-0" :aria-describedby="errors.tests ? 'tests-error' : undefined">
              <legend class="label">Test interfaces (each becomes a test network)</legend>
              <p v-if="candidates.length === 0" class="m-0 text-sm text-text-2">No other interface is left for a test network.</p>
              <label
                v-for="i in candidates"
                :key="i.name"
                class="flex cursor-pointer items-center gap-3 border-b border-border py-2 text-sm"
              >
                <input v-model="draft.tests" type="checkbox" :value="i.name" />
                <span class="mono">{{ i.name }}</span>
                <span class="mono text-text-2">{{ i.mac }}</span>
                <span class="text-text-3">{{ i.driver ?? i.bus ?? '' }}</span>
                <StatusDot :tone="i.link_up === false ? 'offline' : 'normal'" :label="i.link_up === false ? 'no link' : 'link'" />
                <span v-if="i.os_managed" class="text-fault-text">configured by the OS</span>
              </label>
              <p v-if="errors.tests" id="tests-error" class="field-error" role="alert">{{ errors.tests }}</p>
            </fieldset>
            <div v-if="hints.length" class="flex flex-col gap-2 rounded-md border border-fault-border bg-fault-tint p-3 text-sm text-fault-text" data-testid="netplan-hints">
              <p class="m-0">A test interface belongs to Chaos Gateway alone; the operating system must not configure it:</p>
              <div v-for="h in hints" :key="h.id">
                <p class="m-0">{{ h.message }}</p>
                <pre v-if="h.fix" class="mono mt-1 mb-0 overflow-auto rounded bg-surface-2 p-2 text-xs whitespace-pre-wrap text-text-2">{{ h.fix }}</pre>
              </div>
              <button type="button" class="btn btn-sm self-start" :disabled="setup.isFetching.value" @click="setup.refetch()">Check again</button>
            </div>
          </template>

          <!-- 3: test networks -->
          <template v-if="step === 2">
            <div v-for="(n, i) in networks" :key="n.iface" class="grid grid-cols-[110px_1fr_1fr_auto] items-start gap-4">
              <div class="pt-8 text-sm"><span class="mono">{{ n.iface }}</span></div>
              <div>
                <label class="label" :for="`net-name-${i}`">Network name</label>
                <input
                  :id="`net-name-${i}`"
                  v-model="n.name"
                  class="input"
                  :aria-invalid="!!errors[`${i}.name`]"
                  :aria-describedby="errors[`${i}.name`] ? `net-name-${i}-error` : undefined"
                />
                <p v-if="errors[`${i}.name`]" :id="`net-name-${i}-error`" class="field-error" role="alert">{{ errors[`${i}.name`] }}</p>
              </div>
              <div>
                <label class="label" :for="`net-addr-${i}`">Gateway address</label>
                <input
                  :id="`net-addr-${i}`"
                  v-model="n.address"
                  class="input mono"
                  :aria-invalid="!!errors[`${i}.address`]"
                  :aria-describedby="errors[`${i}.address`] ? `net-addr-${i}-error` : undefined"
                />
                <p v-if="errors[`${i}.address`]" :id="`net-addr-${i}-error`" class="field-error" role="alert">{{ errors[`${i}.address`] }}</p>
              </div>
              <div class="pt-8"><SwitchToggle v-model="n.dhcp" :label="`DHCP on ${n.iface}`" /></div>
            </div>
          </template>

          <!-- 4: admin password -->
          <template v-if="step === 3">
            <div>
              <label class="label" for="password">Admin password</label>
              <input
                id="password"
                v-model="draft.password"
                class="input"
                type="password"
                autocomplete="new-password"
                :aria-invalid="!!errors.password"
                :aria-describedby="errors.password ? 'password-help password-error' : 'password-help'"
              />
              <p id="password-help" class="mt-1.5 mb-0 text-[13px] text-text-3">At least {{ minPassword }} characters. User name: <span class="mono">admin</span>.</p>
              <p v-if="errors.password" id="password-error" class="field-error" role="alert">{{ errors.password }}</p>
            </div>
            <div>
              <label class="label" for="repeat">Repeat the password</label>
              <input
                id="repeat"
                v-model="draft.repeat"
                class="input"
                type="password"
                autocomplete="new-password"
                :aria-invalid="!!errors.repeat"
                :aria-describedby="errors.repeat ? 'repeat-error' : undefined"
              />
              <p v-if="errors.repeat" id="repeat-error" class="field-error" role="alert">{{ errors.repeat }}</p>
            </div>
          </template>

          <!-- 5: review -->
          <template v-if="step === 4">
            <dl class="m-0 grid grid-cols-[180px_1fr] gap-x-4 gap-y-2 text-sm" data-testid="setup-review">
              <dt class="text-text-2">Uplink</dt>
              <dd class="mono m-0">{{ draft.uplink }} · router {{ draft.gateway.trim() || 'from the default route' }}</dd>
              <dt class="text-text-2">Management</dt>
              <dd class="mono m-0">{{ draft.management }}</dd>
              <template v-for="n in networks" :key="n.iface">
                <dt class="text-text-2">Test network {{ n.name }}</dt>
                <dd class="mono m-0">{{ n.iface }} · {{ n.address }} · DHCP {{ n.dhcp ? 'on' : 'off' }}</dd>
              </template>
            </dl>
            <p class="m-0 text-[13px] text-text-2">
              Applying creates revision 1. Afterwards the UI is reachable only on the management network, and the setup must be
              confirmed within the countdown, or it is rolled back.
            </p>
            <div v-if="submitError" class="rounded-md border border-block-border bg-block-tint p-3 text-sm text-block-text" role="alert">
              <p class="m-0">{{ submitError }}</p>
              <ul v-if="submitErrors.length" class="mt-2 mb-0">
                <li v-for="x in submitErrors" :key="x" class="mono text-xs">{{ x }}</li>
              </ul>
            </div>
          </template>

          <div class="flex justify-between gap-2 border-t border-border pt-4">
            <button v-if="step > 0" type="button" class="btn" :disabled="busy" @click="back">Back</button>
            <span v-else />
            <button type="submit" class="btn btn-primary" :disabled="busy || stepInvalid">
              {{ step < 4 ? 'Next' : busy ? 'Applying…' : 'Apply setup' }}
            </button>
          </div>
        </form>
      </template>
    </div>
  </div>
</template>
