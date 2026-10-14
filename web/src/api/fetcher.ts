// Shared fetch wrapper for the generated Vue Query hooks: same-origin API under /api/v1,
// session cookie for authentication with the CSRF header on unsafe methods, RFC 9457
// problem+json errors. It also tells the app whether the backend answers at all (offline state,
// plan §2.17) and when a session ended (back to the login page), and learns the gateway's clock
// from the Date header of every answer.

import { noteGatewayDate } from '../lib/clock';

export const API_BASE = '/api/v1';

export interface Problem {
  code?: string;
  title?: string;
  detail?: string;
  status?: number;
  errors?: { path?: string; code?: string; message?: string }[];
}

export class ApiError extends Error {
  constructor(
    readonly status: number,
    readonly problem: Problem | undefined,
    /** The seconds the gateway asks to wait before the next attempt (its Retry-After header), if it said. */
    readonly retryAfter?: number,
  ) {
    super(problem?.detail ?? problem?.title ?? `HTTP ${status}`);
  }

  get code(): string | undefined {
    return this.problem?.code;
  }
}

/** The request did not reach the gateway (network down, gateway stopped, proxy without backend). */
export class UnreachableError extends Error {
  constructor(cause?: unknown) {
    super('the gateway does not answer', { cause });
  }
}

type Listener = () => void;
const reachableListeners = new Set<Listener>();
const unreachableListeners = new Set<Listener>();
const unauthorizedListeners = new Set<Listener>();
const unavailableListeners = new Set<Listener>();

function subscribe(set: Set<Listener>, fn: Listener): () => void {
  set.add(fn);
  return () => set.delete(fn);
}

/** fn runs after every request the gateway answered. */
export const onReachable = (fn: Listener) => subscribe(reachableListeners, fn);
/** fn runs after every request that did not reach the gateway. */
export const onUnreachable = (fn: Listener) => subscribe(unreachableListeners, fn);
/** fn runs when a request that needed a session was refused with 401: the session is gone. */
export const onUnauthorized = (fn: Listener) => subscribe(unauthorizedListeners, fn);
/**
 * fn runs when the gateway refused a request with 503 `unavailable`: its executor is not
 * connected, it is in safe mode, or the first-start setup is open (again, after a rollback).
 */
export const onUnavailable = (fn: Listener) => subscribe(unavailableListeners, fn);

let csrfToken: string | undefined;

/** The CSRF token of the session (GET /auth/session); sent as X-CSRF-Token on unsafe methods. */
export function setCsrfToken(token: string | undefined): void {
  csrfToken = token;
}

const safeMethods = new Set(['GET', 'HEAD', 'OPTIONS']);
// the endpoints where a 401 is an answer about the credentials sent, not an ended session
const credentialPaths = ['/auth/login', '/auth/session', '/setup'];

function emit(set: Set<Listener>): void {
  for (const fn of set) fn();
}

export const customFetch = async <T>(url: string, options: RequestInit = {}): Promise<T> => {
  const full = url.startsWith(API_BASE + '/') ? url : API_BASE + url;
  const method = (options.method ?? 'GET').toUpperCase();
  const init: RequestInit = { credentials: 'same-origin', ...options };
  const needsType = typeof options.body === 'string' && !new Headers(options.headers).has('Content-Type');
  if ((!safeMethods.has(method) && csrfToken) || needsType) {
    // a generated call with two request types (createRevision) sets none: JSON unless told otherwise
    const headers = new Headers(options.headers);
    if (needsType) headers.set('Content-Type', 'application/json');
    if (!safeMethods.has(method) && csrfToken) headers.set('X-CSRF-Token', csrfToken);
    init.headers = headers;
  }
  let response: Response;
  const sentAt = Date.now();
  try {
    response = await fetch(full, init);
  } catch (e) {
    if (e instanceof DOMException && e.name === 'AbortError') throw e;
    emit(unreachableListeners);
    throw new UnreachableError(e);
  }
  // a gateway (proxy) error without the API behind it is no answer of the API
  if (response.status === 502 || response.status === 504) {
    emit(unreachableListeners);
    throw new UnreachableError(`HTTP ${response.status}`);
  }
  emit(reachableListeners);
  noteGatewayDate(response.headers.get('Date'), sentAt, Date.now());
  const text = await response.text();
  let body: unknown;
  if (text) {
    try {
      body = JSON.parse(text);
    } catch {
      body = text;
    }
  }
  if (!response.ok) {
    const problem = typeof body === 'object' && body !== null ? (body as Problem) : undefined;
    if (response.status === 401 && !credentialPaths.some((p) => full.startsWith(API_BASE + p))) {
      emit(unauthorizedListeners);
    }
    if (response.status === 503 && problem?.code === 'unavailable') emit(unavailableListeners);
    throw new ApiError(response.status, problem, retryAfterSeconds(response.headers.get('Retry-After')));
  }
  return body as T;
};

/** The seconds of a Retry-After header in its delay-seconds form; undefined without one or for an HTTP date. */
function retryAfterSeconds(v: string | null): number | undefined {
  if (v === null || !/^\s*\d+\s*$/.test(v)) return undefined;
  return Number(v);
}

/** The message to show for an error of a request. */
export function errorMessage(e: unknown): string {
  if (e instanceof ApiError || e instanceof UnreachableError) return e.message;
  if (e instanceof Error) return e.message;
  return String(e);
}
