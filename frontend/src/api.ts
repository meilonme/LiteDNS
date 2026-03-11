export type ProviderName = 'aliyun' | 'cloudflare'
export type TaskStatus = 'running' | 'paused'
export type LogType = 'ddns_task' | 'public_ip_check' | 'operation'

export interface LoginResult {
  token: string
  expires_at: string
  must_change_password: boolean
}

export interface MeInfo {
  id: number
  username: string
  must_change_password: boolean
  last_login_at?: string
}

export interface VendorItem {
  id: number
  name: string
  provider: ProviderName
  api_key: string
  extra?: unknown
  created_at: string
  updated_at: string
}

export interface DomainItem {
  id: number
  vendor_id: number
  remote_domain_id?: string
  domain_name: string
  last_synced_at?: string
  created_at: string
  updated_at: string
}

export interface RecordItem {
  id: number
  domain_id: number
  remote_record_id: string
  host: string
  type: string
  value: string
  ttl: number
  proxied: boolean
  line?: string
  updated_at: string
}

export interface DomainSyncSummary {
  added: number
  updated: number
  deleted: number
}

export interface TaskItem {
  id: number
  domain_id: number
  host: string
  record_type: string
  interval_sec: number
  status: TaskStatus
  last_ip?: string
  last_check_at?: string
  last_success_at?: string
  consecutive_failures: number
  next_run_at: string
  last_error?: string
  version: number
  created_at: string
  updated_at: string
}

export interface SystemLogItem {
  id: number
  type: LogType
  result: string
  ddns_task_id?: number
  actor?: string
  action?: string
  target_type?: string
  target_id?: string
  old_ip?: string
  new_ip?: string
  error_msg?: string
  latency_ms?: number
  detail_json?: string
  created_at: string
}

export interface SettingsItem {
  sync_ttl_sec: number
  'logs.retention_days': number
  'ddns.default_interval_sec': number
  public_ip_check: boolean
  ip_check_interval_sec: number
  ip_sources: string[]
  public_ip?: string
  public_ip_last_checked_at?: string
}

interface ApiSuccess<T> {
  code: 'OK'
  message: string
  data: T
}

interface ApiFailure {
  code: string
  message: string
  request_id?: string
}

type RequestMethod = 'GET' | 'POST' | 'PUT' | 'DELETE'

function trimSlash(v: string): string {
  return v.endsWith('/') ? v.slice(0, -1) : v
}

function buildQuery(params: Record<string, unknown>): string {
  const entries = Object.entries(params).filter(([, value]) => value !== undefined && value !== null && value !== '')
  if (entries.length === 0) {
    return ''
  }
  const query = new URLSearchParams()
  for (const [k, v] of entries) {
    query.set(k, String(v))
  }
  return `?${query.toString()}`
}

export class ApiClient {
  private readonly base: string
  private token = ''

  constructor(base?: string) {
    const envBase = (base ?? import.meta.env.VITE_API_BASE ?? '/api/v1') as string
    this.base = trimSlash(envBase)
  }

  setToken(token: string): void {
    this.token = token
  }

  private async request<T>(method: RequestMethod, path: string, payload?: unknown): Promise<T> {
    const headers: Record<string, string> = {
      Accept: 'application/json'
    }
    if (this.token) {
      headers.Authorization = `Bearer ${this.token}`
    }

    let body: string | undefined
    if (payload !== undefined) {
      headers['Content-Type'] = 'application/json'
      body = JSON.stringify(payload)
    }

    const response = await fetch(`${this.base}${path}`, {
      method,
      headers,
      body
    })

    const text = await response.text()
    let parsed: ApiSuccess<T> | ApiFailure | null = null
    try {
      parsed = text ? (JSON.parse(text) as ApiSuccess<T> | ApiFailure) : null
    } catch {
      parsed = null
    }

    if (!response.ok) {
      const message = parsed && 'message' in parsed ? parsed.message : `HTTP ${response.status}`
      throw new Error(message)
    }

    if (!parsed || !('code' in parsed) || parsed.code !== 'OK' || !('data' in parsed)) {
      const message = parsed && 'message' in parsed ? parsed.message : 'invalid api response'
      throw new Error(message)
    }

    return parsed.data
  }

  login(username: string, password: string): Promise<LoginResult> {
    return this.request('POST', '/auth/login', { username, password })
  }

  logout(): Promise<Record<string, never>> {
    return this.request('POST', '/auth/logout')
  }

  changePassword(oldPassword: string, newPassword: string): Promise<Record<string, never>> {
    return this.request('POST', '/auth/change-password', {
      old_password: oldPassword,
      new_password: newPassword
    })
  }

  me(): Promise<MeInfo> {
    return this.request('GET', '/auth/me')
  }

  listVendors(): Promise<VendorItem[]> {
    return this.request('GET', '/vendors')
  }

  createVendor(payload: {
    name: string
    provider: ProviderName
    api_key: string
    api_secret: string
    extra?: string
  }): Promise<VendorItem> {
    return this.request('POST', '/vendors', payload)
  }

  updateVendor(id: number, payload: {
    name?: string
    provider?: ProviderName
    api_key?: string
    api_secret?: string
    extra?: string
  }): Promise<VendorItem> {
    return this.request('PUT', `/vendors/${id}`, payload)
  }

  deleteVendor(id: number): Promise<Record<string, never>> {
    return this.request('DELETE', `/vendors/${id}`)
  }

  verifyVendor(id: number): Promise<{ verified: boolean }> {
    return this.request('POST', `/vendors/${id}/verify`)
  }

  listDomains(vendorId?: number): Promise<DomainItem[]> {
    const query = buildQuery({ vendor_id: vendorId })
    return this.request('GET', `/domains${query}`)
  }

  syncDomain(id: number): Promise<DomainSyncSummary> {
    return this.request('POST', `/domains/${id}/sync`)
  }

  listRecords(domainId: number): Promise<RecordItem[]> {
    return this.request('GET', `/domains/${domainId}/records`)
  }

  createRecord(domainId: number, payload: {
    host: string
    type: string
    value: string
    ttl: number
    proxied: boolean
    line?: string
  }): Promise<RecordItem> {
    return this.request('POST', `/domains/${domainId}/records`, payload)
  }

  updateRecord(id: number, payload: {
    value?: string
    ttl?: number
    proxied?: boolean
    line?: string
  }): Promise<RecordItem> {
    return this.request('PUT', `/records/${id}`, payload)
  }

  deleteRecord(id: number): Promise<Record<string, never>> {
    return this.request('DELETE', `/records/${id}`)
  }

  listTasks(filter: { status?: TaskStatus | ''; domain_id?: number | '' }): Promise<TaskItem[]> {
    const query = buildQuery(filter)
    return this.request('GET', `/ddns/tasks${query}`)
  }

  createTask(payload: {
    domain_id: number
    host: string
    record_type: 'A' | 'AAAA'
    interval_sec?: number
  }): Promise<TaskItem> {
    return this.request('POST', '/ddns/tasks', payload)
  }

  updateTask(id: number, payload: { interval_sec?: number; status?: TaskStatus }): Promise<TaskItem> {
    return this.request('PUT', `/ddns/tasks/${id}`, payload)
  }

  deleteTask(id: number): Promise<Record<string, never>> {
    return this.request('DELETE', `/ddns/tasks/${id}`)
  }

  pauseTask(id: number): Promise<TaskItem> {
    return this.request('POST', `/ddns/tasks/${id}/pause`)
  }

  resumeTask(id: number): Promise<TaskItem> {
    return this.request('POST', `/ddns/tasks/${id}/resume`)
  }

  runTaskOnce(id: number): Promise<{ executed: boolean }> {
    return this.request('POST', `/ddns/tasks/${id}/run-once`)
  }

  listLogs(filter: { type?: LogType | ''; result?: string; ddns_task_id?: number | ''; start?: string; end?: string }): Promise<SystemLogItem[]> {
    const query = buildQuery(filter)
    return this.request('GET', `/logs${query}`)
  }

  getSettings(): Promise<SettingsItem> {
    return this.request('GET', '/settings')
  }

  updateSettings(payload: {
    sync_ttl_sec?: number
    'logs.retention_days'?: number
    'ddns.default_interval_sec'?: number
    public_ip_check?: boolean
    ip_check_interval_sec?: number
    ip_sources?: string[]
  }): Promise<SettingsItem> {
    return this.request('PUT', '/settings', payload)
  }

  runPublicIPCheckOnce(): Promise<{ executed: boolean }> {
    return this.request('POST', '/settings/public-ip-check/run-once')
  }
}
