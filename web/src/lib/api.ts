// Small fetch wrapper for the server API.

let csrfToken = '';

export class ApiError extends Error {
  constructor(
    public status: number,
    message: string,
  ) {
    super(message);
  }
}

export async function api<T>(method: string, path: string, body?: unknown): Promise<T> {
  const headers: Record<string, string> = {};
  if (body !== undefined) headers['Content-Type'] = 'application/json';
  if (method !== 'GET' && csrfToken) headers['X-CSRF-Token'] = csrfToken;
  const res = await fetch(path, {
    method,
    headers,
    body: body === undefined ? undefined : JSON.stringify(body),
    credentials: 'same-origin',
  });
  let data: unknown = null;
  const text = await res.text();
  if (text) {
    try {
      data = JSON.parse(text);
    } catch {
      data = { error: text };
    }
  }
  if (!res.ok) {
    const msg = (data as { error?: string } | null)?.error ?? `Request failed (${res.status})`;
    throw new ApiError(res.status, msg);
  }
  return data as T;
}

export interface AuthConfig {
  org_name: string;
  oidc_enabled: boolean;
  oidc_provider_name: string;
  auto_login: boolean;
  local_enabled: boolean;
}

export interface Me {
  name: string;
  email: string;
  method: 'oidc' | 'local';
  csrf_token: string;
  expires_at: string;
}

export async function loadMe(): Promise<Me | null> {
  try {
    const me = await api<Me>('GET', '/api/me');
    csrfToken = me.csrf_token;
    return me;
  } catch (e) {
    if (e instanceof ApiError && e.status === 401) return null;
    throw e;
  }
}

export function getAuthConfig(): Promise<AuthConfig> {
  return api<AuthConfig>('GET', '/api/auth/config');
}

export async function logout(): Promise<void> {
  await api('POST', '/api/auth/logout');
  csrfToken = '';
}

export interface SupportSession {
  id: string;
  code: string;
  state: string;
  live: boolean;
  tech_name: string;
  tech_email: string;
  created_at: string;
  code_expires_at: string;
  connected_at: string;
  consented_at: string;
  ended_at: string;
  end_reason: string;
  agent_hostname: string;
  agent_os: string;
  agent_arch: string;
  agent_user: string;
  agent_version: string;
  agent_ip: string;
}

export interface LiveSession extends SupportSession {
  agent_connected: boolean;
  tech_connected: boolean;
  mine: boolean;
}

export interface CreatedSession {
  id: string;
  code: string;
  code_formatted: string;
  code_expires_at: string;
  expires_in: number;
  join_url: string;
}

export interface AuditEvent {
  id: number;
  session_id: string;
  at: string;
  actor: string;
  kind: string;
  detail: string;
  ip: string;
}

export interface Platform {
  id: string;
  os: 'windows' | 'darwin' | 'linux';
  arch: string;
  label: string;
  size: number;
}

export interface CodeCheck {
  code: string;
  code_formatted: string;
  org_name: string;
  tech_name: string;
  code_expires_at: string;
  platforms: Platform[];
}
