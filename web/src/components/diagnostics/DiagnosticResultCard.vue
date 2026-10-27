<script setup lang="ts">
// The result of a diagnostic (plan §2.12, §2.17 "Diagnostics"; M24): whether it succeeded and, in a sentence, what happened
// or why not; then what the type measured — the steps of a path, the echoes of a ping, the connection, the answer of a DNS
// server, the status and headers of an HTTP response, the TLS handshake with the certificates the server presented. The
// state is a dot and a word, and every failed step names itself, never by colour alone.
import { computed } from 'vue';
import type { DiagnosticRequest, DiagnosticResult } from '../../api/generated/model';
import { describeTarget, diagnosticTypes, formatMs, pathSteps, resultSentence, resultTone } from '../../lib/diagnostics';
import { formatClock } from '../../lib/format';
import StatusDot from '../ui/StatusDot.vue';

const props = defineProps<{ request: DiagnosticRequest; result: DiagnosticResult; at: Date }>();

const state = computed(() => resultTone(props.result));
const typeLabel = computed(() => diagnosticTypes.find((t) => t.value === props.result.type)?.label ?? props.result.type);
const steps = computed(() => (props.result.type === 'path' ? pathSteps(props.result) : []));
const headers = computed(() => Object.entries(props.result.http?.headers ?? {}).sort(([a], [b]) => a.localeCompare(b)));
const when = (iso: string | undefined) => (iso ? iso.slice(0, 10) : '—');
</script>

<template>
  <div class="flex flex-col gap-3" data-testid="diagnostic-result" :data-ok="result.ok">
    <div class="flex flex-wrap items-center gap-x-4 gap-y-1">
      <StatusDot :tone="state.tone" :label="state.label" data-testid="diagnostic-state" />
      <span class="text-sm font-medium">{{ typeLabel }} <span class="mono text-text-2">{{ describeTarget(request) }}</span></span>
      <span v-if="result.address" class="text-[13px] text-text-2" data-testid="diagnostic-address">resolved to <span class="mono">{{ result.address }}</span></span>
      <span class="ml-auto text-xs text-text-3">{{ formatClock(at) }} · {{ formatMs(result.duration_ms) }} · from the {{ result.from ?? 'gateway' }}</span>
    </div>
    <p class="m-0 text-sm" :class="result.ok ? 'text-text' : 'text-block-text'" data-testid="diagnostic-sentence">{{ resultSentence(result) }}</p>

    <ol v-if="steps.length" class="m-0 grid list-none gap-2 p-0 sm:grid-cols-4" aria-label="The steps of the path" data-testid="diagnostic-steps">
      <li
        v-for="s in steps"
        :key="s.step"
        class="rounded-md border px-3 py-2"
        :class="s.state === 'failed' ? 'border-block-text' : 'border-border'"
        :data-step="s.step"
        :data-state="s.state"
      >
        <div class="flex items-center gap-2 text-sm font-medium">
          <span aria-hidden="true">{{ s.state === 'ok' ? '✓' : s.state === 'failed' ? '✗' : '–' }}</span>
          {{ s.label }}
          <span class="text-xs font-normal text-text-3">{{ s.state === 'ok' ? 'passed' : s.state === 'failed' ? 'failed' : 'not run' }}</span>
        </div>
        <p v-if="s.detail" class="mt-1 mb-0 text-xs break-words text-text-2">{{ s.detail }}</p>
      </li>
    </ol>

    <dl v-if="result.ping" class="m-0 grid grid-cols-[repeat(auto-fill,minmax(150px,1fr))] gap-x-6 gap-y-2 text-sm" data-testid="diagnostic-ping">
      <div><dt class="label">Sent</dt><dd class="m-0 mono">{{ result.ping.sent }}</dd></div>
      <div><dt class="label">Received</dt><dd class="m-0 mono">{{ result.ping.received }}</dd></div>
      <div><dt class="label">Loss</dt><dd class="m-0 mono">{{ result.ping.loss_percent }} %</dd></div>
      <template v-if="result.ping.rtt_min_ms !== undefined">
        <div><dt class="label">Minimum</dt><dd class="m-0 mono">{{ formatMs(result.ping.rtt_min_ms) }}</dd></div>
        <div><dt class="label">Average</dt><dd class="m-0 mono">{{ formatMs(result.ping.rtt_avg_ms) }}</dd></div>
        <div><dt class="label">Maximum</dt><dd class="m-0 mono">{{ formatMs(result.ping.rtt_max_ms) }}</dd></div>
        <div><dt class="label">Deviation</dt><dd class="m-0 mono">{{ formatMs(result.ping.rtt_mdev_ms) }}</dd></div>
      </template>
    </dl>

    <dl v-if="result.connect" class="m-0 grid grid-cols-[repeat(auto-fill,minmax(180px,1fr))] gap-x-6 gap-y-2 text-sm" data-testid="diagnostic-connect">
      <div><dt class="label">Reachable</dt><dd class="m-0">{{ result.connect.reachable ? 'yes' : 'no' }}</dd></div>
      <div v-if="result.connect.rtt_ms !== undefined"><dt class="label">Round trip</dt><dd class="m-0 mono">{{ formatMs(result.connect.rtt_ms) }}</dd></div>
      <div v-if="result.connect.detail"><dt class="label">Detail</dt><dd class="m-0">{{ result.connect.detail }}</dd></div>
    </dl>

    <div v-if="result.dns" data-testid="diagnostic-dns">
      <dl class="m-0 grid grid-cols-[repeat(auto-fill,minmax(180px,1fr))] gap-x-6 gap-y-2 text-sm">
        <div v-if="result.dns.name"><dt class="label">Name</dt><dd class="m-0 mono">{{ result.dns.name }}</dd></div>
        <div v-if="result.dns.server"><dt class="label">Server</dt><dd class="m-0 mono">{{ result.dns.server }}</dd></div>
        <div v-if="result.dns.rcode"><dt class="label">Response code</dt><dd class="m-0 mono">{{ result.dns.rcode }}</dd></div>
        <div v-if="result.dns.rtt_ms !== undefined"><dt class="label">Round trip</dt><dd class="m-0 mono">{{ formatMs(result.dns.rtt_ms) }}</dd></div>
      </dl>
      <ul v-if="result.dns.answers?.length" class="mt-2 mb-0 list-none p-0 text-sm" aria-label="Answers">
        <li v-for="a in result.dns.answers" :key="a" class="mono">{{ a }}</li>
      </ul>
    </div>

    <div v-if="result.http" data-testid="diagnostic-http">
      <dl class="m-0 grid grid-cols-[repeat(auto-fill,minmax(180px,1fr))] gap-x-6 gap-y-2 text-sm">
        <div><dt class="label">Status</dt><dd class="m-0 mono">{{ result.http.status }}</dd></div>
        <div><dt class="label">Time to the response</dt><dd class="m-0 mono">{{ formatMs(result.http.rtt_ms) }}</dd></div>
      </dl>
      <table v-if="headers.length" class="mt-3 w-full border-collapse text-[13px]" aria-label="Response headers">
        <tbody>
          <tr v-for="[k, v] in headers" :key="k" class="border-t border-border align-top">
            <th scope="row" class="w-[220px] py-1.5 pr-3 text-left font-normal text-text-2">{{ k }}</th>
            <td class="mono py-1.5 break-all">{{ v }}</td>
          </tr>
        </tbody>
      </table>
    </div>

    <div v-if="result.tls" data-testid="diagnostic-tls">
      <dl class="m-0 grid grid-cols-[repeat(auto-fill,minmax(180px,1fr))] gap-x-6 gap-y-2 text-sm">
        <div><dt class="label">Version</dt><dd class="m-0 mono">{{ result.tls.version }}</dd></div>
        <div><dt class="label">Cipher</dt><dd class="m-0 mono break-all">{{ result.tls.cipher }}</dd></div>
        <div v-if="result.tls.alpn"><dt class="label">ALPN</dt><dd class="m-0 mono">{{ result.tls.alpn }}</dd></div>
        <div>
          <dt class="label">Certificate</dt>
          <dd class="m-0" data-testid="diagnostic-verified">{{ result.tls.verified ? 'verifies' : `does not verify: ${result.tls.verify_error ?? ''}` }}</dd>
        </div>
      </dl>
      <ol v-if="result.tls.certificates?.length" class="mt-3 mb-0 flex list-none flex-col gap-2 p-0" aria-label="Certificates the server presented">
        <li v-for="(c, i) in result.tls.certificates" :key="i" class="rounded-md border border-border px-3 py-2 text-[13px]" data-testid="diagnostic-certificate">
          <div class="text-text-2">{{ i === 0 ? 'Server certificate' : `Certificate ${i + 1} of the chain` }}</div>
          <div class="mono break-all">{{ c.subject }}</div>
          <div class="text-text-2">issued by <span class="mono break-all">{{ c.issuer }}</span></div>
          <div class="text-text-2">valid {{ when(c.not_before) }} to {{ when(c.not_after) }}<template v-if="c.dns_names?.length"> · for <span class="mono">{{ c.dns_names.join(', ') }}</span></template></div>
        </li>
      </ol>
    </div>
  </div>
</template>
