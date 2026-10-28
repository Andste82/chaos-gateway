import { defineStore } from 'pinia';
import { ref } from 'vue';
import { ApiError, errorMessage } from '../api/fetcher';
import { runDiagnostic } from '../api/generated/hooks/diagnostics/diagnostics';
import type { DiagnosticRequest, DiagnosticResult } from '../api/generated/model';
import { gatewayNow } from '../lib/clock';
import type { RunFailure } from '../lib/diagnostics';
import { useApiPanelStore } from './apiPanel';

export interface DiagnosticRun {
  id: number;
  request: DiagnosticRequest;
  /** When it was started. */
  at: Date;
  /** The diagnostic ran and answered (also when its target failed). */
  result?: DiagnosticResult;
  /** The gateway refused to run it, or did not answer; the sentence. */
  error?: string;
  /**
   * What `error` is: the gateway refused the request (a 4xx about the request), failed on it (a 5xx: it may have run) or
   * did not answer at all (P6-AUDIT-45).
   */
  failure?: RunFailure;
  /** The probe the run was made from, by the name it had when the run started; none for the gateway (P6-AUDIT-44). */
  from?: string;
  /** The gateway's refusal as it named the fields, for the form. */
  fieldErrors?: { path?: string; message?: string }[];
}

const KEEP = 10;

/**
 * The diagnostics of this session (plan §2.17 "Diagnostics"; M24): the runs of the Diagnostics screen, newest first, the
 * last ten. A diagnostic is a request that waits for its answer (up to a minute), so the screen can show the run while
 * it is under way and the page can be left meanwhile. Nothing is kept past the session: the gateway keeps no result.
 */
export const useDiagnosticsStore = defineStore('diagnostics', () => {
  const runs = ref<DiagnosticRun[]>([]);
  const running = ref(0);
  let next = 1;

  async function run(request: DiagnosticRequest, from?: string): Promise<DiagnosticRun> {
    const entry: DiagnosticRun = { id: next++, request, at: gatewayNow(), ...(from ? { from } : {}) };
    running.value++;
    try {
      entry.result = await runDiagnostic(request);
      useApiPanelStore().record('Run a diagnostic', [{ method: 'POST', path: '/diagnostics', body: request }]);
    } catch (e) {
      entry.error = errorMessage(e);
      entry.failure = e instanceof ApiError ? (e.status >= 500 ? 'failed' : 'refused') : 'unreachable';
      const problem = (e as { problem?: { errors?: { path?: string; message?: string }[] } }).problem;
      if (problem?.errors?.length) entry.fieldErrors = problem.errors;
    } finally {
      running.value--;
    }
    runs.value = [entry, ...runs.value].slice(0, KEEP);
    return entry;
  }

  function clear() {
    runs.value = [];
  }

  return { runs, running, run, clear };
});
