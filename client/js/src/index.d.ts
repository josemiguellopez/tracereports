// Tipos de tracereports (cliente JavaScript).

export interface Delivery {
  sent: number; retried: number; rejected: number; dropped: number; lost: number; pending: number; unregisteredTests: number;
  /** 1 si el servidor no confirmó el cierre de la ejecución. */
  runNotClosed: number;
}

export interface TraceReportsOptions {
  baseUrl?: string; token?: string; enabled?: boolean; timeoutMs?: number; uploadTimeoutMs?: number;
  flushTimeoutMs?: number; maxQueueItems?: number; maxQueueMB?: number;
}

export interface RunOptions { environment?: string; project?: string; branch?: string; commit?: string; framework?: string }

export interface TestOptions {
  category?: string; description?: string; key?: string; suite?: string; params?: string; worker?: string | number;
  status?: string; onFailure?: (t: TraceTest, err: unknown) => unknown | Promise<unknown>;
}

export type Status = "INFO" | "PASS" | "FAIL" | "WARNING" | "SKIP";

export interface Connection {
  method: string; url: string; status?: number; status_text?: string; mime_type?: string; resource_type?: string;
  failed?: boolean; error_text?: string; started_at?: number; duration_ms?: number | null;
  request_headers?: Record<string, string>; post_data?: string; response_headers?: Record<string, string>;
  response_body?: string; body_size?: number; expected?: boolean;
}

export class TraceReports {
  constructor(opts?: TraceReportsOptions);
  baseUrl: string; runId: number | null; runCreated: boolean; enabled: boolean;
  readonly delivery: Delivery;
  readonly reportUrl: string;
  deliveryProblems(): number;
  startRun(name: string, opts?: RunOptions): Promise<number | null>;
  joinRun(runId: number | string): number | null;
  finishRun(opts?: { interrupted?: boolean }): Promise<unknown>;
  flush(timeoutMs?: number): Promise<number>;
  startTest(name: string, opts?: TestOptions): Promise<TraceTest>;
  test<T>(name: string, fn: (t: TraceTest) => T | Promise<T>): Promise<T>;
  test<T>(name: string, opts: TestOptions, fn: (t: TraceTest) => T | Promise<T>): Promise<T>;
}

export class TraceTest {
  readonly id: number | null; readonly active: boolean; attempts: number;
  log(status: Status, message: string, timestamp?: number): void;
  info(message: string): void; pass(message: string): void; fail(message: string): void; warn(message: string): void; skip(message: string): void;
  screenshot(image: Uint8Array | string | null, message?: string, status?: Status): void;
  expectResponse(status: number | number[], opts?: { url?: string; method?: string }): void;
  network(connections: Connection[], opts?: { maxBodyKB?: number; batchSize?: number }): { stored: number; errors: number };
  dom(snapshot: unknown): void;
  finish(status?: Status | "", opts?: { errorMessage?: string; errorTrace?: string; error?: unknown; attempts?: number }): void;
}

export const DOM_SCRIPT: string;

export function domSnapshotInPage(): unknown;
