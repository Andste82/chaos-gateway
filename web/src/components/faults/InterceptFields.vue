<script setup lang="ts">
// The fields of an interception case (plan §2.8, §2.17 "Interception UI", M23c): the proxy's settings for the case (the key log, QUIC,
// the policy for the servers' certificates) and the list of HTTP rules in the order they are applied, each with a match (host, path
// prefix, method), an action and the fields of its action; a `modify` rule lists the headers it sets or removes and the texts it replaces.
// The "Add fault" dialog and the `tls` step of the step dialog show them below the TLS fields when the case is `intercept`. Each message
// stands under its field (`errors` by field name: `interceptUpstream`, `rule.<n>.<field>`).
import { computed, useId } from 'vue';
import {
  actionText,
  emptyRule,
  headerProblemRow,
  httpActions,
  matchText,
  maxRules,
  methods,
  replaceProblemRow,
  ruleKey,
  unreachableRules,
  upstreamPolicies,
  type HeaderRow,
  type HttpRuleForm,
  type InterceptForm,
  type RuleField,
} from '../../lib/interceptForm';

const props = defineProps<{ errors: Partial<Record<string, string>> }>();
const form = defineModel<InterceptForm>({ required: true });
const uid = useId();
const fid = (name: string) => `${uid}-${name}`;
const eid = (name: string) => `${uid}-${name}-error`;
const rid = (i: number, f: string) => `${uid}-r${i}-${f}`;
const rerr = (i: number, f: RuleField) => ruleKey(i, f);

const policy = computed(() => upstreamPolicies.find((p) => p.value === form.value.upstream));
const unreachable = computed(() => unreachableRules(form.value.rules));

function addRule() {
  if (form.value.rules.length < maxRules) form.value.rules.push(emptyRule());
}
function removeRule(i: number) {
  form.value.rules.splice(i, 1);
}
function move(i: number, by: -1 | 1) {
  const rules = form.value.rules;
  const j = i + by;
  if (j < 0 || j >= rules.length) return;
  const [r] = rules.splice(i, 1);
  rules.splice(j, 0, r!);
}
const actionHint = (r: HttpRuleForm) => httpActions.find((a) => a.value === r.action)?.hint;

const headerKinds = [
  { key: 'requestHeaders', label: 'Request headers', what: 'request' },
  { key: 'responseHeaders', label: 'Response headers', what: 'response' },
] as const;
function addHeader(r: HttpRuleForm, key: 'requestHeaders' | 'responseHeaders') {
  r[key].push({ name: '', value: '', remove: false } satisfies HeaderRow);
}
// the messages of a modify rule are tied to the controls they speak of: the message of a list of headers or of the texts to replace
// describes each input of the list and marks the row it names invalid (every row when the gateway named only the list); the message
// that the rule changes nothing describes the rule's Action (P5-AUDIT-38)
function rowInvalid(i: number, f: 'requestHeaders' | 'responseHeaders' | 'replaces', j: number): boolean {
  if (!props.errors[rerr(i, f)]) return false;
  const rule = form.value.rules[i]!;
  const row = f === 'replaces' ? replaceProblemRow(rule.replaces) : headerProblemRow(rule[f]);
  return row < 0 || row === j;
}
const rowDescribedBy = (i: number, f: RuleField) => (props.errors[rerr(i, f)] ? `${rid(i, f)}-error` : undefined);
const actionDescribedBy = (i: number, r: HttpRuleForm) =>
  [rid(i, 'action-hint'), r.action === 'modify' && props.errors[rerr(i, 'modify')] ? `${rid(i, 'modify')}-error` : ''].filter(Boolean).join(' ');
</script>

<template>
  <div class="flex flex-col gap-4" data-testid="intercept-fields">
    <fieldset class="m-0 flex flex-col gap-3 rounded-md border border-border p-3">
      <legend class="px-1 text-[13px] text-text-2">Proxy settings for this case</legend>
      <label class="flex items-start gap-2 text-sm">
        <input v-model="form.keylog" type="checkbox" class="mt-1" />
        <span>
          Give the TLS secrets to the captures that run
          <span class="block text-[13px] text-text-2">A capture of the device made while it talks can then be decrypted in Wireshark. Such a capture is marked SENSITIVE; the proxy writes the log whatever this says.</span>
        </span>
      </label>
      <label class="flex items-start gap-2 text-sm">
        <input v-model="form.blockQuic" type="checkbox" class="mt-1" />
        <span>
          Block QUIC (UDP 443)
          <span class="block text-[13px] text-text-2">A client that tries QUIC / HTTP/3 falls back to TCP, which the proxy can read. QUIC itself is never intercepted: without this a client that keeps to QUIC is not seen.</span>
        </span>
      </label>
      <div>
        <label class="label" :for="fid('upstream')">Servers' certificates</label>
        <select
          :id="fid('upstream')"
          v-model="form.upstream"
          class="input"
          :aria-invalid="!!errors.interceptUpstream"
          :aria-describedby="errors.interceptUpstream ? eid('upstream') : fid('upstream-hint')"
        >
          <option v-for="p in upstreamPolicies" :key="p.value" :value="p.value">{{ p.label }}</option>
        </select>
        <p v-if="errors.interceptUpstream" :id="eid('upstream')" class="field-error">{{ errors.interceptUpstream }}</p>
        <p v-else :id="fid('upstream-hint')" class="m-0 mt-1 text-[13px] text-text-2" data-testid="upstream-hint">
          How the proxy treats the certificate of the real server it opens its own connection to. {{ policy?.hint }}
        </p>
      </div>
    </fieldset>

    <div class="flex flex-col gap-3" data-testid="http-rules">
      <div>
        <h3 class="m-0 text-sm font-semibold">HTTP rules</h3>
        <p class="m-0 mt-1 text-[13px] text-text-2" :id="fid('rules-hint')">
          Applied in the order written to every request of the intercepted connections (HTTP/1.1, HTTP/2 and the handshake of a WebSocket) that the rule selects. Block, answer with a status code and abort end the rules for a request; the other rules go on with the next. A connection that is no HTTP (MQTT over TLS) is met by the rules that name no path and no method.
        </p>
      </div>
      <p v-if="errors.interceptRules" class="field-error m-0" role="alert">{{ errors.interceptRules }}</p>
      <p v-if="form.rules.length === 0" class="m-0 rounded-md border border-dashed border-border-strong p-3 text-[13px] text-text-2" data-testid="no-rules">
        No rules: the proxy only watches, and the exchanges are listed on the Interception screen. Add a rule to answer, delay, throttle, abort or change what the device and the server say.
      </p>
      <ol class="m-0 flex list-none flex-col gap-3 p-0">
        <li v-for="(rule, i) in form.rules" :key="i" data-testid="http-rule">
          <div role="group" :aria-label="`Rule ${i + 1}`" class="flex flex-col gap-3 rounded-md border border-border bg-surface-2 p-3">
            <div class="flex items-center justify-between gap-2">
              <div class="min-w-0 text-[13px]">
                <span class="mono text-text-3">{{ i + 1 }}</span>
                <span class="ml-2 text-text" data-testid="rule-summary">{{ matchText(rule) }} → {{ actionText(rule) }}</span>
              </div>
              <div class="flex shrink-0 gap-1">
                <button type="button" class="btn btn-sm" :disabled="i === 0" :aria-label="`Move rule ${i + 1} up`" @click="move(i, -1)">Up</button>
                <button type="button" class="btn btn-sm" :disabled="i === form.rules.length - 1" :aria-label="`Move rule ${i + 1} down`" @click="move(i, 1)">Down</button>
                <button type="button" class="btn btn-sm" :aria-label="`Remove rule ${i + 1}`" @click="removeRule(i)">Remove</button>
              </div>
            </div>
            <p v-if="unreachable.has(i)" class="m-0 text-[13px] text-fault-text" data-testid="rule-unreachable">
              This rule never acts: an earlier rule ends the rules for every request.
            </p>

            <div class="grid grid-cols-3 gap-3">
              <div>
                <label class="label" :for="rid(i, 'host')">Host</label>
                <input
                  :id="rid(i, 'host')"
                  v-model="rule.host"
                  class="input mono"
                  placeholder="any host"
                  :aria-invalid="!!errors[rerr(i, 'host')]"
                  :aria-describedby="errors[rerr(i, 'host')] ? `${rid(i, 'host')}-error` : undefined"
                />
                <p v-if="errors[rerr(i, 'host')]" :id="`${rid(i, 'host')}-error`" class="field-error">{{ errors[rerr(i, 'host')] }}</p>
              </div>
              <div>
                <label class="label" :for="rid(i, 'path')">Path starts with</label>
                <input
                  :id="rid(i, 'path')"
                  v-model="rule.path"
                  class="input mono"
                  placeholder="any path"
                  :aria-invalid="!!errors[rerr(i, 'path')]"
                  :aria-describedby="errors[rerr(i, 'path')] ? `${rid(i, 'path')}-error` : undefined"
                />
                <p v-if="errors[rerr(i, 'path')]" :id="`${rid(i, 'path')}-error`" class="field-error">{{ errors[rerr(i, 'path')] }}</p>
              </div>
              <div>
                <label class="label" :for="rid(i, 'method')">Method</label>
                <input
                  :id="rid(i, 'method')"
                  v-model="rule.method"
                  class="input mono"
                  placeholder="any method"
                  :list="rid(i, 'methods')"
                  :aria-invalid="!!errors[rerr(i, 'method')]"
                  :aria-describedby="errors[rerr(i, 'method')] ? `${rid(i, 'method')}-error` : undefined"
                />
                <datalist :id="rid(i, 'methods')"><option v-for="m in methods" :key="m" :value="m" /></datalist>
                <p v-if="errors[rerr(i, 'method')]" :id="`${rid(i, 'method')}-error`" class="field-error">{{ errors[rerr(i, 'method')] }}</p>
              </div>
            </div>

            <div class="grid grid-cols-2 gap-3">
              <div>
                <label class="label" :for="rid(i, 'action')">Action</label>
                <select
                  :id="rid(i, 'action')"
                  v-model="rule.action"
                  class="input"
                  :aria-invalid="rule.action === 'modify' && !!errors[rerr(i, 'modify')]"
                  :aria-describedby="actionDescribedBy(i, rule)"
                >
                  <option v-for="a in httpActions" :key="a.value" :value="a.value">{{ a.label }}</option>
                </select>
              </div>
              <div v-if="rule.action === 'status'">
                <label class="label" :for="rid(i, 'status')">Status code</label>
                <input
                  :id="rid(i, 'status')"
                  v-model="rule.status"
                  class="input mono"
                  inputmode="numeric"
                  :aria-invalid="!!errors[rerr(i, 'status')]"
                  :aria-describedby="errors[rerr(i, 'status')] ? `${rid(i, 'status')}-error` : undefined"
                />
                <p v-if="errors[rerr(i, 'status')]" :id="`${rid(i, 'status')}-error`" class="field-error">{{ errors[rerr(i, 'status')] }}</p>
              </div>
              <div v-else-if="rule.action === 'delay'">
                <label class="label" :for="rid(i, 'delay')">Delay</label>
                <input
                  :id="rid(i, 'delay')"
                  v-model="rule.delay"
                  class="input mono"
                  :aria-invalid="!!errors[rerr(i, 'delay')]"
                  :aria-describedby="errors[rerr(i, 'delay')] ? `${rid(i, 'delay')}-error` : undefined"
                />
                <p v-if="errors[rerr(i, 'delay')]" :id="`${rid(i, 'delay')}-error`" class="field-error">{{ errors[rerr(i, 'delay')] }}</p>
              </div>
              <div v-else-if="rule.action === 'throttle'">
                <label class="label" :for="rid(i, 'rate')">Rate</label>
                <input
                  :id="rid(i, 'rate')"
                  v-model="rule.rate"
                  class="input mono"
                  :aria-invalid="!!errors[rerr(i, 'rate')]"
                  :aria-describedby="errors[rerr(i, 'rate')] ? `${rid(i, 'rate')}-error` : undefined"
                />
                <p v-if="errors[rerr(i, 'rate')]" :id="`${rid(i, 'rate')}-error`" class="field-error">{{ errors[rerr(i, 'rate')] }}</p>
              </div>
            </div>
            <p :id="rid(i, 'action-hint')" class="m-0 -mt-1 text-[13px] text-text-2" data-testid="action-hint">{{ actionHint(rule) }}</p>

            <template v-if="rule.action === 'modify'">
              <p v-if="errors[rerr(i, 'modify')]" :id="`${rid(i, 'modify')}-error`" class="field-error m-0">{{ errors[rerr(i, 'modify')] }}</p>
              <div v-for="k in headerKinds" :key="k.key" class="flex flex-col gap-2" role="group" :aria-label="`${k.label} of rule ${i + 1}`">
                <div class="flex items-center justify-between">
                  <span class="text-[13px] text-text-2">{{ k.label }}</span>
                  <button type="button" class="btn btn-sm" :aria-label="`Add a ${k.what} header to rule ${i + 1}`" @click="addHeader(rule, k.key)">Add header</button>
                </div>
                <div v-for="(h, j) in rule[k.key]" :key="j" class="grid grid-cols-[1fr_1.4fr_auto_auto] items-center gap-2">
                  <input
                    v-model="h.name"
                    class="input mono"
                    placeholder="Header"
                    :aria-label="`${k.label} of rule ${i + 1}, header ${j + 1} name`"
                    :aria-invalid="rowInvalid(i, k.key, j)"
                    :aria-describedby="rowDescribedBy(i, k.key)"
                  />
                  <input
                    v-model="h.value"
                    class="input mono"
                    placeholder="value"
                    :disabled="h.remove"
                    :aria-label="`${k.label} of rule ${i + 1}, header ${j + 1} value`"
                    :aria-invalid="rowInvalid(i, k.key, j)"
                    :aria-describedby="rowDescribedBy(i, k.key)"
                  />
                  <label class="flex items-center gap-1.5 text-[13px] text-text-2">
                    <input
                      v-model="h.remove"
                      type="checkbox"
                      :aria-label="`${k.label} of rule ${i + 1}, header ${j + 1}: take the header away`"
                      :aria-describedby="rowDescribedBy(i, k.key)"
                    />Remove
                  </label>
                  <button type="button" class="btn btn-sm" :aria-label="`Remove ${k.what} header ${j + 1} of rule ${i + 1}`" @click="rule[k.key].splice(j, 1)">×</button>
                </div>
                <p v-if="errors[rerr(i, k.key)]" :id="`${rid(i, k.key)}-error`" class="field-error m-0">{{ errors[rerr(i, k.key)] }}</p>
              </div>
              <div class="flex flex-col gap-2" role="group" :aria-label="`Texts replaced in the response of rule ${i + 1}`">
                <div class="flex items-center justify-between">
                  <span class="text-[13px] text-text-2">Replace text in the body of the response</span>
                  <button type="button" class="btn btn-sm" :aria-label="`Add a replacement to rule ${i + 1}`" @click="rule.replaces.push({ search: '', replace: '' })">Add replacement</button>
                </div>
                <div v-for="(x, j) in rule.replaces" :key="j" class="grid grid-cols-[1fr_1fr_auto] items-center gap-2">
                  <input
                    v-model="x.search"
                    class="input mono"
                    placeholder="search for"
                    :aria-label="`Replacement ${j + 1} of rule ${i + 1}: search for`"
                    :aria-invalid="rowInvalid(i, 'replaces', j)"
                    :aria-describedby="rowDescribedBy(i, 'replaces')"
                  />
                  <input
                    v-model="x.replace"
                    class="input mono"
                    placeholder="replace with"
                    :aria-label="`Replacement ${j + 1} of rule ${i + 1}: replace with`"
                    :aria-invalid="rowInvalid(i, 'replaces', j)"
                    :aria-describedby="rowDescribedBy(i, 'replaces')"
                  />
                  <button type="button" class="btn btn-sm" :aria-label="`Remove replacement ${j + 1} of rule ${i + 1}`" @click="rule.replaces.splice(j, 1)">×</button>
                </div>
                <p v-if="errors[rerr(i, 'replaces')]" :id="`${rid(i, 'replaces')}-error`" class="field-error m-0">{{ errors[rerr(i, 'replaces')] }}</p>
                <p v-if="rule.replaces.length" class="m-0 text-[13px] text-text-2">The text is searched as UTF-8 in the body of the response, which the proxy holds whole in memory to change it.</p>
              </div>
            </template>
          </div>
        </li>
      </ol>
      <div>
        <button type="button" class="btn btn-sm" :disabled="form.rules.length >= maxRules" @click="addRule">Add rule</button>
      </div>
    </div>
  </div>
</template>
