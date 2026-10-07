// Thin fetch wrapper for /api/v1 with CSRF handling and typed errors.

let csrfToken = '';
export function setCsrf(t: string) {
  csrfToken = t;
}

export class ApiError extends Error {
  status: number;
  body: any;
  constructor(status: number, message: string, body: any) {
    super(message);
    this.status = status;
    this.body = body;
  }
}

type Listener = () => void;
const unauthorizedListeners = new Set<Listener>();
export function onUnauthorized(fn: Listener) {
  unauthorizedListeners.add(fn);
  return () => {
    unauthorizedListeners.delete(fn);
  };
}

export async function api<T = any>(method: string, path: string, body?: unknown): Promise<T> {
  const headers: Record<string, string> = {};
  if (body !== undefined) headers['Content-Type'] = 'application/json';
  if (method !== 'GET' && csrfToken) headers['X-CSRF-Token'] = csrfToken;
  const res = await fetch('/api/v1' + path, {
    method,
    headers,
    body: body === undefined ? undefined : JSON.stringify(body),
    credentials: 'same-origin',
  });
  if (res.status === 204) return undefined as T;
  const ct = res.headers.get('Content-Type') || '';
  const data = ct.includes('application/json') ? await res.json().catch(() => null) : await res.text();
  if (!res.ok) {
    if (res.status === 401 && !path.startsWith('/auth/')) unauthorizedListeners.forEach((fn) => fn());
    const msg = (data && typeof data === 'object' && data.error) || (typeof data === 'string' && data) || res.statusText;
    throw new ApiError(res.status, msg, data);
  }
  return data as T;
}

export const get = <T = any>(p: string) => api<T>('GET', p);
export const post = <T = any>(p: string, b?: unknown) => api<T>('POST', p, b ?? {});
export const put = <T = any>(p: string, b?: unknown) => api<T>('PUT', p, b ?? {});
export const del = <T = any>(p: string) => api<T>('DELETE', p);

export function qs(params: Record<string, string | number | boolean | undefined | null>) {
  const u = new URLSearchParams();
  for (const [k, v] of Object.entries(params)) {
    if (v === undefined || v === null || v === '' || v === false) continue;
    u.set(k, v === true ? '1' : String(v));
  }
  const s = u.toString();
  return s ? '?' + s : '';
}

// ---------- types (mirror the Go structs) ----------

export interface User { id: number; username: string; role: 'admin' | 'viewer'; createdAt: number }
export interface AuthState { setupRequired: boolean; user?: User; csrf?: string; version: string }

export interface Port { IP?: string; PrivatePort: number; PublicPort?: number; Type: string }
export interface ContainerRow {
  id: string; name: string; image: string; imageId: string; state: string; status: string;
  health: string; created: number; ports: Port[]; stack: string; self: boolean;
}
export interface Stats {
  cpuPercent: number; memUsage: number; memLimit: number; netRx: number; netTx: number;
  blockRead: number; blockWrite: number; pids: number;
}
export interface ImageRow {
  id: string; shortId: string; ref: string; repo: string; tag: string; digest: string; created: number;
  size: number; usedBy: string[]; running: boolean; dangling: boolean; tagCount: number;
}
export interface DeletePlanItem { ref: string; digest?: string; size: number; action: 'delete' | 'skip'; reason: string; alsoRemoves?: string[] }
export interface DeletePlan { items: DeletePlanItem[]; deleteCount: number; skipCount: number; bytes: number }

export interface StackRow {
  name: string; hasFolder: boolean; dir: string; running: number; total: number; containers: string[];
  modTime: number; status: string; workingDir: string;
}
export interface PortConflict { kind: string; name: string }
export interface PortRow {
  hostIp: string; hostPort: number; containerPort: number; proto: string; container: string; containerId: string;
  state: string; stack: string; status: 'ok' | 'conflict' | 'shared' | 'local'; conflict?: string;
}
export interface Reserved { port: number; proto: string; name: string }
export interface PortsView { ports: PortRow[]; reserved: Reserved[]; hostListeners: { ip: string; port: number; proto: string }[] | null; hostChecked: boolean }

export interface Registry { id: number; name: string; url: string; username: string; hasPassword: boolean; insecure: boolean; managed: boolean; container: string }
export interface RepoRow { name: string; tags: number }
export interface TagRow {
  repo: string; tag: string; digest: string; mediaType: string; size: number; created: string;
  platforms: string[] | null; error?: string; sharedWith: string[];
}

export interface Policy {
  id: number; name: string; enabled: boolean; target: 'local' | 'registry' | 'both'; registryId: number;
  repoPattern: string; tagInclude: string; tagExclude: string; period: 'day' | 'week' | 'month' | 'year';
  keepPerPeriod: number; keepMode: 'newest' | 'oldest'; keepLastN: number; minAgeDays: number; maxAgeDays: number;
  dateSource: 'created' | 'tag'; dateRegex: string; dateLayout: string; skipInUse: boolean; cron: string; timezone: string;
  dryRun: boolean; gcAfter: boolean; notifyEmail: boolean; notifyWebhook: boolean; lastRunAt: number; lastJobId: number;
  nextRun?: number;
}
export interface Decision {
  repo: string; tag: string; digest?: string; created: string; size: number; usedBy?: string[];
  ref: string; date: string; bucket: string; action: 'keep' | 'delete'; reason: string;
}
export interface PlanResult { decisions: Decision[] | null; keepCount: number; deleteCount: number; deleteSize: number }
export interface PolicyPreview { local?: PlanResult; registry?: PlanResult; registryName?: string; warnings: string[]; generatedAt: number }

export interface Job {
  id: number; type: string; title: string; policyId: number; user: string; status: string; done: number; total: number;
  bytes: number; dryRun: boolean; createdAt: number; startedAt: number; finishedAt: number; summary: string; log?: string;
}
export interface JobItem { id: number; jobId: number; target: string; ref: string; digest: string; action: string; reason: string; result: string; error: string; bytes: number }
export interface AuditEntry { id: number; ts: number; user: string; action: string; object: string; details: string; result: string }

export interface Settings {
  reservedPorts: string; timezone: string; smtpHost: string; smtpPort: number; smtpUser: string; smtpPassword?: string;
  hasSmtpPassword: boolean; smtpFrom: string; smtpTo: string; webhookUrl: string; gcDeleteUntagged: boolean; jobHistoryDays: number;
}

export interface Dashboard {
  docker: { connected: boolean; version: string; apiVersion: string; error?: string; host: string };
  containers: { total: number; running: number };
  images: { total: number; dangling: number; unused: number; size: number };
  stacks: number; registries: number; policies: number; portConflicts: number;
  disk: { images: number; containers: number; volumes: number; buildCache: number; reclaimable: number };
  recentJobs: Job[]; version: string;
}
