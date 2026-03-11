<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import {
  ApiClient,
  type DomainItem,
  type LogType,
  type MeInfo,
  type ProviderName,
  type RecordItem,
  type SettingsItem,
  type SystemLogItem,
  type TaskItem,
  type TaskStatus,
  type VendorItem
} from './api'

type TabKey = 'dashboard' | 'domains' | 'dns' | 'ddns' | 'providers' | 'logs' | 'settings'

type NoticeType = 'success' | 'error' | 'info'

const api = new ApiClient()
const activeTab = ref<TabKey>('dashboard')
const loading = ref(false)
const initialized = ref(false)

const notice = reactive<{ show: boolean; type: NoticeType; text: string }>({
  show: false,
  type: 'info',
  text: ''
})
const showRecordModal = ref(false)
const showTaskModal = ref(false)
const showVendorModal = ref(false)

const token = ref(localStorage.getItem('litedns_token') ?? '')
if (token.value) {
  api.setToken(token.value)
}

const loginForm = reactive({
  username: 'admin',
  password: ''
})

const me = ref<MeInfo | null>(null)
const changePasswordForm = reactive({
  oldPassword: '',
  newPassword: ''
})

const vendors = ref<VendorItem[]>([])
const vendorForm = reactive({
  id: 0,
  name: '',
  provider: 'aliyun' as ProviderName,
  api_key: '',
  api_secret: '',
  extra: ''
})

const domains = ref<DomainItem[]>([])
const domainFilterVendorId = ref<number | ''>('')
const selectedDomainId = ref<number>(0)
const records = ref<RecordItem[]>([])
const dashboardTotalRecordCount = ref(0)
const recordForm = reactive({
  id: 0,
  host: '@',
  type: 'A',
  value: '',
  ttl: 600,
  proxied: false,
  line: ''
})

const taskFilter = reactive<{ status: TaskStatus | ''; domain_id: number | '' }>({
  status: '',
  domain_id: ''
})
const tasks = ref<TaskItem[]>([])
const taskForm = reactive({
  id: 0,
  domain_id: 0,
  host: '@',
  record_type: 'A' as 'A' | 'AAAA',
  interval_sec: 300,
  status: 'running' as TaskStatus
})

const logFilter = reactive({
  type: '' as LogType | '',
  ddns_task_id: '' as number | '',
  result: '',
  start: '',
  end: ''
})
const systemLogs = ref<SystemLogItem[]>([])

const settingsState = reactive({
  sync_ttl_sec: 600,
  logs_retention_days: 90,
  ddns_default_interval_sec: 300,
  public_ip_check: true,
  ip_check_interval_sec: 300,
  ip_sources_text: '',
  public_ip: '',
  public_ip_last_checked_at: ''
})

const isAuthed = computed(() => Boolean(token.value))
const selectedDomain = computed(() => domains.value.find((d) => d.id === selectedDomainId.value) ?? null)
const domainCount = computed(() => domains.value.length)
const providerCount = computed(() => vendors.value.length)
const runningTaskCount = computed(() => tasks.value.filter((t) => t.status === 'running').length)
const dashboardDomains = computed(() => domains.value.slice(0, 5))
const todayText = computed(() => new Date().toLocaleDateString('zh-CN', { year: 'numeric', month: '2-digit', day: '2-digit' }))

const recordStats = computed(() => {
  let a = 0
  let cname = 0
  let mx = 0
  let txt = 0
  for (const item of records.value) {
    const typ = item.type.toUpperCase()
    if (typ === 'A') {
      a += 1
    } else if (typ === 'CNAME') {
      cname += 1
    } else if (typ === 'MX') {
      mx += 1
    } else if (typ === 'TXT') {
      txt += 1
    }
  }
  return { a, cname, mx, txt }
})

const mergedLogLines = computed(() => {
  const lines: Array<{ ts: number; text: string; level: string; levelClass: string; timeRaw: string }> = []

  for (const item of systemLogs.value) {
    const ts = Date.parse(item.created_at)
    let level = 'LOG'
    let levelClass = 'text-slate-400'
    let text = '系统日志'

    if (item.type === 'ddns_task') {
      level = item.result === 'success' ? 'SYNC' : 'WARN'
      levelClass = item.result === 'success' ? 'text-blue-400' : 'text-amber-400'
      text =
        item.result === 'success'
          ? `DDNS任务(${item.ddns_task_id || '-'}) ${item.action || 'update'}: ${item.old_ip || '-'} -> ${item.new_ip || '-'} (${item.latency_ms || 0}ms)`
          : `DDNS任务(${item.ddns_task_id || '-'}) 失败: ${item.error_msg || 'unknown error'}`
    } else if (item.type === 'public_ip_check') {
      level = 'CHECK'
      levelClass = item.result === 'success' ? 'text-emerald-400' : 'text-amber-400'
      try {
        const detail = item.detail_json ? (JSON.parse(item.detail_json) as Record<string, unknown>) : {}
        const source = String(detail.source ?? '-')
        const publicIP = String(detail.public_ip ?? '-')
        if (item.result === 'success') {
          text = `公网IP检查成功: ${publicIP} (source=${source})`
        } else {
          text = `公网IP检查失败: ${item.error_msg || 'unknown error'}`
        }
      } catch {
        text = item.result === 'success' ? '公网IP检查成功' : `公网IP检查失败: ${item.error_msg || 'unknown error'}`
      }
    } else if (item.type === 'operation') {
      level = 'OPER'
      levelClass = 'text-slate-400'
      text = `用户(${item.actor || '-'}) 执行 ${item.action || '-'} -> ${item.target_type || '-'}#${item.target_id || '-'}`
    }

    lines.push({
      ts: Number.isNaN(ts) ? 0 : ts,
      timeRaw: item.created_at,
      level,
      levelClass,
      text
    })
  }

  return lines.sort((a, b) => b.ts - a.ts).slice(0, 200)
})

function setNotice(type: NoticeType, text: string): void {
  notice.show = true
  notice.type = type
  notice.text = text
  window.setTimeout(() => {
    notice.show = false
  }, 3200)
}

function beginLoading(): void {
  loading.value = true
}

function endLoading(): void {
  loading.value = false
}

function resetVendorForm(): void {
  vendorForm.id = 0
  vendorForm.name = ''
  vendorForm.provider = 'aliyun'
  vendorForm.api_key = ''
  vendorForm.api_secret = ''
  vendorForm.extra = ''
}

function resetRecordForm(): void {
  recordForm.id = 0
  recordForm.host = '@'
  recordForm.type = 'A'
  recordForm.value = ''
  recordForm.ttl = 600
  recordForm.proxied = false
  recordForm.line = ''
}

function resetTaskForm(): void {
  taskForm.id = 0
  taskForm.domain_id = selectedDomainId.value || (domains.value[0]?.id ?? 0)
  taskForm.host = '@'
  taskForm.record_type = 'A'
  taskForm.interval_sec = 300
  taskForm.status = 'running'
}

function parseExtraToString(value: unknown): string {
  if (value === undefined || value === null) {
    return ''
  }
  if (typeof value === 'string') {
    return value
  }
  try {
    return JSON.stringify(value, null, 2)
  } catch {
    return String(value)
  }
}

function formatTime(raw?: string): string {
  if (!raw) {
    return '-'
  }
  const d = new Date(raw)
  if (Number.isNaN(d.getTime())) {
    return raw
  }
  return d.toLocaleString()
}

function vendorName(vendorId: number): string {
  return vendors.value.find((v) => v.id === vendorId)?.name ?? `#${vendorId}`
}

function taskDomainName(domainId: number): string {
  return domains.value.find((d) => d.id === domainId)?.domain_name ?? `#${domainId}`
}

function recordTypeClass(type: string): string {
  const t = type.toUpperCase()
  if (t === 'A') return 'text-blue-600'
  if (t === 'CNAME') return 'text-indigo-600'
  if (t === 'MX') return 'text-purple-600'
  if (t === 'TXT') return 'text-slate-600'
  return 'text-slate-700'
}

function openDomainDns(domainId: number): void {
  selectedDomainId.value = domainId
  activeTab.value = 'dns'
  resetRecordForm()
  void loadRecords()
}

function openCreateRecordModal(): void {
  resetRecordForm()
  showRecordModal.value = true
}

function openEditRecordModal(item: RecordItem): void {
  editRecord(item)
  showRecordModal.value = true
}

function closeRecordModal(): void {
  showRecordModal.value = false
}

function openCreateTaskModal(): void {
  resetTaskForm()
  showTaskModal.value = true
}

function openEditTaskModal(item: TaskItem): void {
  editTask(item)
  showTaskModal.value = true
}

function closeTaskModal(): void {
  showTaskModal.value = false
}

function openCreateVendorModal(): void {
  resetVendorForm()
  showVendorModal.value = true
}

function openEditVendorModal(item: VendorItem): void {
  editVendor(item)
  showVendorModal.value = true
}

function closeVendorModal(): void {
  showVendorModal.value = false
}

async function loadMe(): Promise<void> {
  me.value = await api.me()
}

async function loadVendors(): Promise<void> {
  vendors.value = await api.listVendors()
}

async function loadDomains(): Promise<void> {
  const vendorId = domainFilterVendorId.value === '' ? undefined : domainFilterVendorId.value
  domains.value = await api.listDomains(vendorId)

  if (!domains.value.find((d) => d.id === selectedDomainId.value)) {
    selectedDomainId.value = domains.value[0]?.id ?? 0
  }
}

async function loadRecords(): Promise<void> {
  if (!selectedDomainId.value) {
    records.value = []
    return
  }
  records.value = await api.listRecords(selectedDomainId.value)
}

async function loadDashboardTotalRecordCount(): Promise<void> {
  if (domains.value.length === 0) {
    dashboardTotalRecordCount.value = 0
    return
  }

  const results = await Promise.allSettled(domains.value.map((d) => api.listRecords(d.id)))
  dashboardTotalRecordCount.value = results.reduce((sum, result) => {
    if (result.status !== 'fulfilled') {
      return sum
    }
    return sum + result.value.length
  }, 0)
}

async function loadTasks(): Promise<void> {
  tasks.value = await api.listTasks(taskFilter)
}

async function loadLogs(): Promise<void> {
  systemLogs.value = await api.listLogs({
    type: logFilter.type,
    result: logFilter.result,
    ddns_task_id: logFilter.type === 'ddns_task' ? logFilter.ddns_task_id : '',
    start: logFilter.start,
    end: logFilter.end
  })
}

function onLogTypeChange(): void {
  if (logFilter.type !== 'ddns_task') {
    logFilter.ddns_task_id = ''
  }
}

function applySettings(data: SettingsItem): void {
  settingsState.sync_ttl_sec = Number(data.sync_ttl_sec ?? 600)
  settingsState.logs_retention_days = Number(data['logs.retention_days'] ?? 90)
  settingsState.ddns_default_interval_sec = Number(data['ddns.default_interval_sec'] ?? 300)
  settingsState.public_ip_check = Boolean(data.public_ip_check ?? true)
  settingsState.ip_check_interval_sec = Number(data.ip_check_interval_sec ?? 300)
  settingsState.ip_sources_text = (data.ip_sources ?? []).join('\n')
  settingsState.public_ip = typeof data.public_ip === 'string' ? data.public_ip : ''
  settingsState.public_ip_last_checked_at = typeof data.public_ip_last_checked_at === 'string' ? data.public_ip_last_checked_at : ''
}

async function loadSettings(): Promise<void> {
  const settings = await api.getSettings()
  applySettings(settings)
}

async function loadConsoleData(): Promise<void> {
  await Promise.all([loadVendors(), loadDomains(), loadTasks(), loadSettings()])
  await Promise.all([loadRecords(), loadLogs(), loadDashboardTotalRecordCount()])
  resetTaskForm()
}

async function handleLogin(): Promise<void> {
  if (!loginForm.username || !loginForm.password) {
    setNotice('error', '请输入用户名和密码')
    return
  }
  beginLoading()
  try {
    const login = await api.login(loginForm.username, loginForm.password)
    token.value = login.token
    api.setToken(login.token)
    localStorage.setItem('litedns_token', login.token)
    await loadMe()
    await loadConsoleData()
    loginForm.password = ''
    setNotice('success', '登录成功')
  } catch (error) {
    setNotice('error', (error as Error).message)
  } finally {
    endLoading()
  }
}

async function handleLogout(): Promise<void> {
  beginLoading()
  try {
    await api.logout()
  } catch {
    // ignore logout request failures on client side
  } finally {
    token.value = ''
    api.setToken('')
    localStorage.removeItem('litedns_token')
    me.value = null
    vendors.value = []
    domains.value = []
    records.value = []
    tasks.value = []
    systemLogs.value = []
    showRecordModal.value = false
    showTaskModal.value = false
    showVendorModal.value = false
    endLoading()
  }
}

async function handleChangePassword(): Promise<void> {
  if (!changePasswordForm.oldPassword || !changePasswordForm.newPassword) {
    setNotice('error', '请填写完整密码信息')
    return
  }
  beginLoading()
  try {
    await api.changePassword(changePasswordForm.oldPassword, changePasswordForm.newPassword)
    changePasswordForm.oldPassword = ''
    changePasswordForm.newPassword = ''
    await loadMe()
    setNotice('success', '密码已更新')
  } catch (error) {
    setNotice('error', (error as Error).message)
  } finally {
    endLoading()
  }
}

function editVendor(item: VendorItem): void {
  vendorForm.id = item.id
  vendorForm.name = item.name
  vendorForm.provider = item.provider
  vendorForm.api_key = item.api_key
  vendorForm.api_secret = ''
  vendorForm.extra = parseExtraToString(item.extra)
}

async function saveVendor(): Promise<void> {
  if (!vendorForm.name || !vendorForm.api_key || (!vendorForm.id && !vendorForm.api_secret)) {
    setNotice('error', '供应商名称、API Key、API Secret 不能为空')
    return
  }

  beginLoading()
  try {
    if (vendorForm.id) {
      const payload: {
        name?: string
        provider?: ProviderName
        api_key?: string
        api_secret?: string
        extra?: string
      } = {
        name: vendorForm.name,
        provider: vendorForm.provider,
        api_key: vendorForm.api_key
      }
      if (vendorForm.api_secret.trim()) {
        payload.api_secret = vendorForm.api_secret.trim()
      }
      if (vendorForm.extra.trim()) {
        payload.extra = vendorForm.extra.trim()
      }
      await api.updateVendor(vendorForm.id, payload)
      setNotice('success', '供应商已更新')
    } else {
      await api.createVendor({
        name: vendorForm.name,
        provider: vendorForm.provider,
        api_key: vendorForm.api_key,
        api_secret: vendorForm.api_secret,
        extra: vendorForm.extra.trim() || undefined
      })
      setNotice('success', '供应商已创建')
    }
    resetVendorForm()
    await loadVendors()
    await loadDomains()
    showVendorModal.value = false
  } catch (error) {
    setNotice('error', (error as Error).message)
  } finally {
    endLoading()
  }
}

async function verifyVendor(id: number): Promise<void> {
  beginLoading()
  try {
    await api.verifyVendor(id)
    setNotice('success', '供应商凭证验证通过')
  } catch (error) {
    setNotice('error', (error as Error).message)
  } finally {
    endLoading()
  }
}

async function removeVendor(id: number): Promise<void> {
  if (!window.confirm('确定删除该供应商吗？')) {
    return
  }
  beginLoading()
  try {
    await api.deleteVendor(id)
    await loadVendors()
    await loadDomains()
    await loadDashboardTotalRecordCount()
    setNotice('success', '供应商已删除')
  } catch (error) {
    setNotice('error', (error as Error).message)
  } finally {
    endLoading()
  }
}

async function triggerDomainSync(domainId: number): Promise<void> {
  beginLoading()
  try {
    const summary = await api.syncDomain(domainId)
    await loadDomains()
    if (selectedDomainId.value === domainId) {
      await loadRecords()
    }
    await loadDashboardTotalRecordCount()
    setNotice('success', `同步完成：新增 ${summary.added} / 更新 ${summary.updated} / 删除 ${summary.deleted}`)
  } catch (error) {
    setNotice('error', (error as Error).message)
  } finally {
    endLoading()
  }
}

function editRecord(item: RecordItem): void {
  recordForm.id = item.id
  recordForm.host = item.host
  recordForm.type = item.type
  recordForm.value = item.value
  recordForm.ttl = item.ttl
  recordForm.proxied = item.proxied
  recordForm.line = item.line ?? ''
}

async function saveRecord(): Promise<void> {
  if (!selectedDomainId.value) {
    setNotice('error', '请先选择域名')
    return
  }
  if (!recordForm.host || !recordForm.type || !recordForm.value) {
    setNotice('error', '记录 host/type/value 不能为空')
    return
  }

  beginLoading()
  try {
    if (recordForm.id) {
      await api.updateRecord(recordForm.id, {
        value: recordForm.value,
        ttl: recordForm.ttl,
        proxied: recordForm.proxied,
        line: recordForm.line || undefined
      })
      setNotice('success', '记录已更新')
    } else {
      await api.createRecord(selectedDomainId.value, {
        host: recordForm.host,
        type: recordForm.type,
        value: recordForm.value,
        ttl: recordForm.ttl,
        proxied: recordForm.proxied,
        line: recordForm.line || undefined
      })
      setNotice('success', '记录已创建')
    }
    resetRecordForm()
    await loadRecords()
    await loadDashboardTotalRecordCount()
    showRecordModal.value = false
  } catch (error) {
    setNotice('error', (error as Error).message)
  } finally {
    endLoading()
  }
}

async function removeRecord(id: number): Promise<void> {
  if (!window.confirm('确定删除该 DNS 记录吗？')) {
    return
  }
  beginLoading()
  try {
    await api.deleteRecord(id)
    if (recordForm.id === id) {
      resetRecordForm()
    }
    await loadRecords()
    await loadDashboardTotalRecordCount()
    setNotice('success', '记录已删除')
  } catch (error) {
    setNotice('error', (error as Error).message)
  } finally {
    endLoading()
  }
}

function editTask(item: TaskItem): void {
  taskForm.id = item.id
  taskForm.domain_id = item.domain_id
  taskForm.host = item.host
  taskForm.record_type = item.record_type as 'A' | 'AAAA'
  taskForm.interval_sec = item.interval_sec
  taskForm.status = item.status
}

async function saveTask(): Promise<void> {
  if (!taskForm.domain_id || !taskForm.host) {
    setNotice('error', '任务 domain_id 和 host 不能为空')
    return
  }

  beginLoading()
  try {
    if (taskForm.id) {
      await api.updateTask(taskForm.id, {
        interval_sec: taskForm.interval_sec,
        status: taskForm.status
      })
      setNotice('success', '任务已更新')
    } else {
      await api.createTask({
        domain_id: taskForm.domain_id,
        host: taskForm.host,
        record_type: taskForm.record_type,
        interval_sec: taskForm.interval_sec
      })
      setNotice('success', '任务已创建')
    }
    resetTaskForm()
    await loadTasks()
    showTaskModal.value = false
  } catch (error) {
    setNotice('error', (error as Error).message)
  } finally {
    endLoading()
  }
}

async function pauseTask(id: number): Promise<void> {
  beginLoading()
  try {
    await api.pauseTask(id)
    await loadTasks()
    setNotice('success', '任务已暂停')
  } catch (error) {
    setNotice('error', (error as Error).message)
  } finally {
    endLoading()
  }
}

async function resumeTask(id: number): Promise<void> {
  beginLoading()
  try {
    await api.resumeTask(id)
    await loadTasks()
    setNotice('success', '任务已恢复')
  } catch (error) {
    setNotice('error', (error as Error).message)
  } finally {
    endLoading()
  }
}

async function runTaskOnce(id: number): Promise<void> {
  beginLoading()
  try {
    await api.runTaskOnce(id)
    await Promise.all([loadTasks(), loadLogs()])
    setNotice('success', '任务已触发执行')
  } catch (error) {
    setNotice('error', (error as Error).message)
  } finally {
    endLoading()
  }
}

async function removeTask(id: number): Promise<void> {
  if (!window.confirm('确定删除该 DDNS 任务吗？')) {
    return
  }
  beginLoading()
  try {
    await api.deleteTask(id)
    if (taskForm.id === id) {
      resetTaskForm()
    }
    if (logFilter.ddns_task_id === id) {
      logFilter.ddns_task_id = ''
      await loadLogs()
    }
    await loadTasks()
    setNotice('success', '任务已删除')
  } catch (error) {
    setNotice('error', (error as Error).message)
  } finally {
    endLoading()
  }
}

async function saveSettings(): Promise<void> {
  beginLoading()
  try {
    const ipSources = settingsState.ip_sources_text
      .split('\n')
      .map((x) => x.trim())
      .filter((x) => x.length > 0)

    const updated = await api.updateSettings({
      sync_ttl_sec: settingsState.sync_ttl_sec,
      'logs.retention_days': settingsState.logs_retention_days,
      'ddns.default_interval_sec': settingsState.ddns_default_interval_sec,
      public_ip_check: settingsState.public_ip_check,
      ip_check_interval_sec: settingsState.ip_check_interval_sec,
      ip_sources: ipSources
    })
    applySettings(updated)
    setNotice('success', '设置已更新')
  } catch (error) {
    setNotice('error', (error as Error).message)
  } finally {
    endLoading()
  }
}

async function runPublicIPCheckNow(): Promise<void> {
  if (!settingsState.public_ip_check) {
    setNotice('error', '请先启用 public_ip_check')
    return
  }

  beginLoading()
  try {
    await api.runPublicIPCheckOnce()
    await Promise.all([loadSettings(), loadLogs()])
    setNotice('success', '公网 IP 已立即检查')
  } catch (error) {
    setNotice('error', (error as Error).message)
  } finally {
    endLoading()
  }
}

async function refreshCurrentTab(): Promise<void> {
  beginLoading()
  try {
    if (activeTab.value === 'dashboard') {
      await Promise.all([loadVendors(), loadDomains(), loadTasks()])
      await Promise.all([loadRecords(), loadDashboardTotalRecordCount()])
    } else if (activeTab.value === 'domains') {
      await loadDomains()
    } else if (activeTab.value === 'dns') {
      await loadDomains()
      await loadRecords()
    } else if (activeTab.value === 'ddns') {
      await loadTasks()
    } else if (activeTab.value === 'providers') {
      await loadVendors()
    } else if (activeTab.value === 'logs') {
      await loadLogs()
    } else if (activeTab.value === 'settings') {
      await loadSettings()
    }
  } catch (error) {
    setNotice('error', (error as Error).message)
  } finally {
    endLoading()
  }
}

onMounted(async () => {
  if (!token.value) {
    initialized.value = true
    return
  }

  beginLoading()
  try {
    await loadMe()
    await loadConsoleData()
  } catch {
    token.value = ''
    api.setToken('')
    localStorage.removeItem('litedns_token')
  } finally {
    initialized.value = true
    endLoading()
  }
})
</script>

<template>
  <main class="app-font min-h-screen bg-[#f8fafc] text-slate-800">
    <section v-if="!initialized" class="grid min-h-screen place-items-center p-6">
      <div class="glass-card rounded-xl px-6 py-4 text-sm font-semibold text-slate-600">正在初始化控制台...</div>
    </section>

    <section v-else-if="!isAuthed" class="grid min-h-screen place-items-center p-6">
      <div class="glass-card w-full max-w-md rounded-2xl p-8 space-y-4">
        <div>
          <h1 class="text-2xl font-bold text-slate-900">LiteDNS</h1>
          <p class="mt-1 text-sm text-slate-500">现代化域名管理系统</p>
        </div>

        <label class="block space-y-1">
          <span class="text-sm text-slate-600">用户名</span>
          <input
            v-model="loginForm.username"
            type="text"
            autocomplete="username"
            placeholder="admin"
            class="w-full rounded-lg border border-slate-200 px-3 py-2 text-sm outline-none focus:ring-2 focus:ring-blue-500"
          />
        </label>

        <label class="block space-y-1">
          <span class="text-sm text-slate-600">密码</span>
          <input
            v-model="loginForm.password"
            type="password"
            autocomplete="current-password"
            placeholder="请输入密码"
            class="w-full rounded-lg border border-slate-200 px-3 py-2 text-sm outline-none focus:ring-2 focus:ring-blue-500"
            @keydown.enter="handleLogin"
          />
        </label>

        <button
          class="w-full rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white transition-all hover:bg-blue-700 disabled:cursor-not-allowed disabled:opacity-60"
          :disabled="loading"
          @click="handleLogin"
        >
          {{ loading ? '登录中...' : '登录系统' }}
        </button>
      </div>
    </section>

    <section v-else class="flex h-screen overflow-hidden">
      <aside class="w-64 border-r border-slate-200 bg-white flex flex-col">
        <div class="p-6 border-b border-slate-100">
          <div class="flex items-center gap-3">
            <div class="w-10 h-10 rounded-xl bg-blue-600 text-white flex items-center justify-center font-bold text-sm">LD</div>
            <div>
              <h1 class="text-xl font-bold text-slate-900 tracking-tight">LiteDNS</h1>
              <p class="text-xs text-slate-500">控制台 v1.0</p>
            </div>
          </div>
        </div>

        <nav class="p-4 flex-1 overflow-y-auto">
          <button class="sidebar-item w-full flex items-center gap-3 px-4 py-3 rounded-lg text-sm font-medium" :class="{ active: activeTab === 'dashboard' }" @click="activeTab = 'dashboard'">
            <span>📊</span> 仪表盘
          </button>

          <p class="px-4 text-xs font-semibold text-slate-400 uppercase tracking-wider mt-6 mb-2">域名资产</p>
          <button class="sidebar-item w-full flex items-center gap-3 px-4 py-3 rounded-lg text-sm font-medium" :class="{ active: activeTab === 'domains' }" @click="activeTab = 'domains'">
            <span>🌐</span> 域名管理
          </button>
          <button class="sidebar-item w-full flex items-center gap-3 px-4 py-3 rounded-lg text-sm font-medium" :class="{ active: activeTab === 'dns' }" @click="activeTab = 'dns'">
            <span>🧩</span> 域名解析
          </button>
          <button class="sidebar-item w-full flex items-center gap-3 px-4 py-3 rounded-lg text-sm font-medium" :class="{ active: activeTab === 'ddns' }" @click="activeTab = 'ddns'">
            <span>🔄</span> DDNS 任务
          </button>

          <p class="px-4 text-xs font-semibold text-slate-400 uppercase tracking-wider mt-6 mb-2">系统管理</p>
          <button class="sidebar-item w-full flex items-center gap-3 px-4 py-3 rounded-lg text-sm font-medium" :class="{ active: activeTab === 'providers' }" @click="activeTab = 'providers'">
            <span>👥</span> 供应商账号
          </button>
          <button class="sidebar-item w-full flex items-center gap-3 px-4 py-3 rounded-lg text-sm font-medium" :class="{ active: activeTab === 'logs' }" @click="activeTab = 'logs'">
            <span>🧾</span> 系统日志
          </button>
          <button class="sidebar-item w-full flex items-center gap-3 px-4 py-3 rounded-lg text-sm font-medium" :class="{ active: activeTab === 'settings' }" @click="activeTab = 'settings'">
            <span>⚙️</span> 系统设置
          </button>
        </nav>

        <div class="p-4 border-t border-slate-100">
          <div class="bg-slate-50 rounded-xl p-4 flex items-center gap-3">
            <div class="w-10 h-10 rounded-full bg-blue-50 text-blue-600 flex items-center justify-center font-semibold">{{ (me?.username || 'A').slice(0, 1).toUpperCase() }}</div>
            <div class="flex-1 min-w-0">
              <p class="text-sm font-semibold truncate">{{ me?.username || '管理员' }}</p>
              <p class="text-xs text-slate-500 truncate">admin@litedns.com</p>
            </div>
            <button class="text-slate-400 hover:text-red-500" :disabled="loading" @click="handleLogout">退出</button>
          </div>
        </div>
      </aside>

      <main class="flex-1 flex flex-col min-w-0 overflow-hidden">
        <header class="h-16 bg-white border-b border-slate-200 flex items-center justify-between px-8 z-10">
          <div class="flex items-center gap-4 flex-1">
            <div class="relative w-96 max-w-full">
              <span class="absolute left-3 top-1/2 -translate-y-1/2 text-slate-400">🔎</span>
              <input class="w-full bg-slate-50 border-none rounded-full py-2 pl-10 pr-4 text-sm focus:ring-2 focus:ring-blue-500 outline-none transition-all" placeholder="快速搜索域名、记录或日志..." type="text" />
            </div>
          </div>

          <div class="flex items-center gap-6">
            <div class="flex items-center gap-2 px-3 py-1 bg-green-50 text-green-600 rounded-full text-xs font-semibold">
              <span class="w-2 h-2 bg-green-500 rounded-full animate-pulse"></span>
              系统状态正常
            </div>
            <div class="h-6 w-px bg-slate-200"></div>
            <span class="text-sm font-medium text-slate-500">{{ todayText }}</span>
            <button
              class="px-3 py-1.5 rounded-lg bg-slate-100 text-slate-600 text-sm font-medium hover:bg-slate-200 transition-colors disabled:opacity-60"
              :disabled="loading"
              @click="refreshCurrentTab"
            >
              刷新
            </button>
          </div>
        </header>

        <div class="flex-1 overflow-y-auto p-8" id="content-area">
          <div v-if="notice.show" class="mb-6 rounded-lg border px-4 py-3 text-sm font-medium" :class="notice.type === 'success' ? 'bg-green-50 border-green-200 text-green-700' : notice.type === 'error' ? 'bg-red-50 border-red-200 text-red-700' : 'bg-blue-50 border-blue-200 text-blue-700'">
            {{ notice.text }}
          </div>

          <div v-if="me?.must_change_password" class="glass-card p-6 rounded-2xl mb-6">
            <h3 class="text-base font-bold text-slate-900 mb-3">首次登录必须修改密码</h3>
            <div class="grid grid-cols-1 md:grid-cols-3 gap-3">
              <input v-model="changePasswordForm.oldPassword" type="password" placeholder="旧密码" class="rounded-lg border border-slate-200 px-3 py-2 text-sm outline-none focus:ring-2 focus:ring-blue-500" />
              <input v-model="changePasswordForm.newPassword" type="password" placeholder="新密码（至少8位）" class="rounded-lg border border-slate-200 px-3 py-2 text-sm outline-none focus:ring-2 focus:ring-blue-500" />
              <button class="rounded-lg bg-blue-600 text-white text-sm font-medium hover:bg-blue-700 transition-colors disabled:opacity-60" :disabled="loading" @click="handleChangePassword">更新密码</button>
            </div>
          </div>

          <div v-if="activeTab === 'dashboard'" class="space-y-8" id="page-dashboard">
            <div class="flex items-end justify-between">
              <div>
                <h2 class="text-2xl font-bold text-slate-900">仪表盘概览</h2>
                <p class="text-slate-500 mt-1">欢迎回来，这是您域名的实时运行状态及统计数据。</p>
              </div>
              <button class="bg-blue-600 hover:bg-blue-700 text-white px-4 py-2 rounded-lg text-sm font-medium flex items-center gap-2 shadow-lg shadow-blue-100 transition-all" @click="activeTab = 'domains'">
                + 新增域名
              </button>
            </div>

            <div class="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-4 gap-6">
              <div class="glass-card p-6 rounded-2xl shadow-sm hover:shadow-md transition-shadow group">
                <div class="flex items-start justify-between mb-4">
                  <div class="w-12 h-12 bg-blue-50 rounded-xl flex items-center justify-center text-blue-600">🌐</div>
                  <span class="bg-green-100 text-green-600 text-xs px-2 py-1 rounded-md">实时</span>
                </div>
                <p class="text-slate-500 text-sm font-medium">托管域名总数</p>
                <h3 class="text-3xl font-bold text-slate-900 mt-1">{{ domainCount }}</h3>
              </div>

              <div class="glass-card p-6 rounded-2xl shadow-sm hover:shadow-md transition-shadow group">
                <div class="flex items-start justify-between mb-4">
                  <div class="w-12 h-12 bg-indigo-50 rounded-xl flex items-center justify-center text-indigo-600">🧩</div>
                  <span class="bg-blue-100 text-blue-600 text-xs px-2 py-1 rounded-md">域名</span>
                </div>
                <p class="text-slate-500 text-sm font-medium">解析记录数</p>
                <h3 class="text-3xl font-bold text-slate-900 mt-1">{{ dashboardTotalRecordCount }}</h3>
              </div>

              <div class="glass-card p-6 rounded-2xl shadow-sm hover:shadow-md transition-shadow group">
                <div class="flex items-start justify-between mb-4">
                  <div class="w-12 h-12 bg-green-50 rounded-xl flex items-center justify-center text-green-600">🔄</div>
                  <span class="bg-green-100 text-green-600 text-xs px-2 py-1 rounded-md">在线</span>
                </div>
                <p class="text-slate-500 text-sm font-medium">运行中 DDNS</p>
                <h3 class="text-3xl font-bold text-slate-900 mt-1">{{ runningTaskCount }}</h3>
              </div>

              <div class="glass-card p-6 rounded-2xl shadow-sm hover:shadow-md transition-shadow group">
                <div class="flex items-start justify-between mb-4">
                  <div class="w-12 h-12 bg-purple-50 rounded-xl flex items-center justify-center text-purple-600">👥</div>
                  <span class="bg-purple-100 text-purple-600 text-xs px-2 py-1 rounded-md">在线</span>
                </div>
                <p class="text-slate-500 text-sm font-medium">核心提供商</p>
                <h3 class="text-3xl font-bold text-slate-900 mt-1">{{ providerCount }}</h3>
              </div>
            </div>

            <div class="glass-card rounded-2xl shadow-sm overflow-hidden">
              <div class="p-6 border-b border-slate-100 flex items-center justify-between">
                <h4 class="font-bold text-slate-900">域名同步状态</h4>
                <a class="text-blue-600 text-sm hover:underline cursor-pointer" @click="activeTab = 'domains'">查看全部</a>
              </div>
              <div class="overflow-x-auto">
                <table class="w-full text-left">
                  <thead class="bg-slate-50/50 text-slate-400 text-xs uppercase tracking-wider">
                    <tr>
                      <th class="px-6 py-4 font-semibold">域名</th>
                      <th class="px-6 py-4 font-semibold">提供商</th>
                      <th class="px-6 py-4 font-semibold">最后同步</th>
                      <th class="px-6 py-4 font-semibold">状态</th>
                      <th class="px-6 py-4 font-semibold">操作</th>
                    </tr>
                  </thead>
                  <tbody class="divide-y divide-slate-100 text-sm">
                    <tr v-for="d in dashboardDomains" :key="d.id">
                      <td class="px-6 py-4 font-medium text-slate-900 text-base">{{ d.domain_name }}</td>
                      <td class="px-6 py-4">{{ vendorName(d.vendor_id) }}</td>
                      <td class="px-6 py-4">{{ formatTime(d.last_synced_at) }}</td>
                      <td class="px-6 py-4">
                        <span class="px-2 py-1 rounded text-xs font-bold" :class="d.last_synced_at ? 'bg-green-50 text-green-600' : 'bg-amber-50 text-amber-600'">
                          {{ d.last_synced_at ? '已同步' : '待同步' }}
                        </span>
                      </td>
                      <td class="px-6 py-4 text-blue-600 cursor-pointer hover:font-bold">
                        <button :disabled="loading" @click="triggerDomainSync(d.id)">立即同步</button>
                      </td>
                    </tr>
                    <tr v-if="dashboardDomains.length === 0">
                      <td colspan="5" class="px-6 py-10 text-center text-slate-400">暂无域名数据</td>
                    </tr>
                  </tbody>
                </table>
              </div>
            </div>
          </div>

          <div v-else-if="activeTab === 'domains'" class="space-y-6" id="page-domains">
            <div class="flex items-center justify-between">
              <h2 class="text-2xl font-bold text-slate-900">域名资产列表</h2>
            </div>

            <div class="glass-card rounded-2xl overflow-hidden shadow-sm">
              <div class="p-6 border-b border-slate-100 flex flex-wrap gap-4 items-center justify-between bg-white/50">
                <div class="flex items-center gap-4">
                  <div class="flex border border-slate-200 rounded-lg overflow-hidden text-sm">
                    <button class="px-4 py-2 bg-blue-50 text-blue-600 font-medium">全部</button>
                    <button class="px-4 py-2 bg-white text-slate-500 hover:text-slate-900">常规</button>
                    <button class="px-4 py-2 bg-white text-slate-500 hover:text-slate-900">锁定</button>
                  </div>

                  <select v-model="domainFilterVendorId" class="border border-slate-200 rounded-lg px-3 py-2 text-sm text-slate-600 outline-none" @change="loadDomains">
                    <option :value="''">所有提供商</option>
                    <option v-for="v in vendors" :key="v.id" :value="v.id">{{ v.name }}</option>
                  </select>

                  <button class="text-slate-400 hover:text-blue-600 p-2" :disabled="loading" @click="loadDomains">筛选</button>
                </div>

                <div class="text-sm text-slate-500">共计 {{ domains.length }} 个域名</div>
              </div>

              <table class="w-full text-left">
                <thead class="bg-slate-50 text-slate-400 text-xs uppercase">
                  <tr>
                    <th class="px-6 py-4 font-semibold">域名名称</th>
                    <th class="px-6 py-4 font-semibold">所属厂商</th>
                    <th class="px-6 py-4 font-semibold">状态</th>
                    <th class="px-6 py-4 font-semibold">最后同步</th>
                    <th class="px-6 py-4 font-semibold">操作</th>
                  </tr>
                </thead>
                <tbody class="divide-y divide-slate-100 text-sm">
                  <tr v-for="d in domains" :key="d.id">
                    <td class="px-6 py-4">
                      <div class="flex items-center gap-3">
                        <div class="w-8 h-8 bg-blue-50 rounded text-blue-600 flex items-center justify-center text-xs font-bold">{{ d.domain_name.slice(0, 1).toUpperCase() }}</div>
                        <div>
                          <div class="font-semibold text-slate-900">{{ d.domain_name }}</div>
                          <div class="text-xs text-slate-400">ID: {{ d.id }}</div>
                        </div>
                      </div>
                    </td>
                    <td class="px-6 py-4">{{ vendorName(d.vendor_id) }}</td>
                    <td class="px-6 py-4">
                      <span class="inline-flex items-center px-2 py-0.5 rounded text-xs font-medium" :class="d.last_synced_at ? 'bg-green-100 text-green-800' : 'bg-amber-100 text-amber-800'">
                        {{ d.last_synced_at ? '✅ 活跃中' : '⏳ 待同步' }}
                      </span>
                    </td>
                    <td class="px-6 py-4">{{ formatTime(d.last_synced_at) }}</td>
                    <td class="px-6 py-4">
                      <div class="flex gap-3">
                        <button class="text-blue-600 hover:bg-blue-50 p-1.5 rounded" :disabled="loading" @click="openDomainDns(d.id)">解析</button>
                        <button class="text-slate-400 hover:text-blue-600 p-1.5 rounded" :disabled="loading" @click="triggerDomainSync(d.id)">同步</button>
                      </div>
                    </td>
                  </tr>
                  <tr v-if="domains.length === 0">
                    <td colspan="5" class="px-6 py-10 text-center text-slate-400">暂无域名，请先配置供应商并同步</td>
                  </tr>
                </tbody>
              </table>
            </div>
          </div>

          <div v-else-if="activeTab === 'dns'" class="space-y-6" id="page-dns">
            <div class="flex items-center justify-between">
              <h2 class="text-2xl font-bold text-slate-900">域名解析管理</h2>
              <div class="flex gap-2">
                <select v-model.number="selectedDomainId" class="border border-slate-200 rounded-lg px-3 py-2 text-sm text-slate-600 outline-none bg-white" @change="loadRecords">
                  <option :value="0">请选择域名</option>
                  <option v-for="d in domains" :key="d.id" :value="d.id">{{ d.domain_name }}</option>
                </select>
                <button class="bg-blue-600 text-white px-4 py-2 rounded-lg text-sm font-medium hover:bg-blue-700 transition-all" :disabled="!selectedDomainId" @click="openCreateRecordModal">
                  添加记录
                </button>
              </div>
            </div>

            <div class="grid grid-cols-1 md:grid-cols-4 gap-4 mb-6">
              <div class="p-4 bg-white rounded-xl border border-slate-200 flex items-center gap-4">
                <div class="w-10 h-10 bg-blue-50 rounded-lg flex items-center justify-center text-blue-600 font-bold">A</div>
                <div>
                  <div class="text-xs text-slate-500 lowercase">A 记录</div>
                  <div class="text-xl font-bold">{{ recordStats.a }} 条</div>
                </div>
              </div>
              <div class="p-4 bg-white rounded-xl border border-slate-200 flex items-center gap-4">
                <div class="w-10 h-10 bg-indigo-50 rounded-lg flex items-center justify-center text-indigo-600 font-bold">CN</div>
                <div>
                  <div class="text-xs text-slate-500 lowercase">CNAME</div>
                  <div class="text-xl font-bold">{{ recordStats.cname }} 条</div>
                </div>
              </div>
              <div class="p-4 bg-white rounded-xl border border-slate-200 flex items-center gap-4">
                <div class="w-10 h-10 bg-purple-50 rounded-lg flex items-center justify-center text-purple-600 font-bold">MX</div>
                <div>
                  <div class="text-xs text-slate-500 lowercase">MX 记录</div>
                  <div class="text-xl font-bold">{{ recordStats.mx }} 条</div>
                </div>
              </div>
              <div class="p-4 bg-white rounded-xl border border-slate-200 flex items-center gap-4">
                <div class="w-10 h-10 bg-slate-50 rounded-lg flex items-center justify-center text-slate-600 font-bold">TX</div>
                <div>
                  <div class="text-xs text-slate-500 lowercase">TXT</div>
                  <div class="text-xl font-bold">{{ recordStats.txt }} 条</div>
                </div>
              </div>
            </div>

            <div class="glass-card rounded-2xl overflow-hidden shadow-sm">
              <table class="w-full text-left">
                <thead class="bg-slate-50 text-slate-400 text-xs uppercase">
                  <tr>
                    <th class="px-6 py-4 font-semibold">记录类型</th>
                    <th class="px-6 py-4 font-semibold">主机记录</th>
                    <th class="px-6 py-4 font-semibold">记录值</th>
                    <th class="px-6 py-4 font-semibold">代理状态</th>
                    <th class="px-6 py-4 font-semibold">TTL</th>
                    <th class="px-6 py-4 font-semibold">操作</th>
                  </tr>
                </thead>
                <tbody class="divide-y divide-slate-100 text-sm">
                  <tr v-for="r in records" :key="r.id">
                    <td class="px-6 py-4 font-bold" :class="recordTypeClass(r.type)">{{ r.type }}</td>
                    <td class="px-6 py-4 font-medium">{{ r.host }}</td>
                    <td class="px-6 py-4 text-slate-600 truncate max-w-[200px]">{{ r.value }}</td>
                    <td class="px-6 py-4">
                      <div class="w-10 h-5 rounded-full relative p-1" :class="r.proxied ? 'bg-blue-500' : 'bg-slate-200'">
                        <div class="w-3 h-3 bg-white rounded-full" :class="r.proxied ? 'ml-auto' : ''"></div>
                      </div>
                    </td>
                    <td class="px-6 py-4 text-slate-500">{{ r.ttl }}</td>
                    <td class="px-6 py-4">
                      <button class="text-slate-400 hover:text-blue-600 mr-2 font-medium" :disabled="loading" @click="openEditRecordModal(r)">编辑</button>
                      <button class="text-slate-400 hover:text-red-600 font-medium" :disabled="loading" @click="removeRecord(r.id)">删除</button>
                    </td>
                  </tr>
                  <tr v-if="records.length === 0">
                    <td colspan="6" class="px-6 py-10 text-center text-slate-400">该域名暂无解析记录</td>
                  </tr>
                </tbody>
              </table>
            </div>

          </div>

          <div v-else-if="activeTab === 'ddns'" class="space-y-6" id="page-ddns">
            <div class="flex items-center justify-between">
              <h2 class="text-2xl font-bold text-slate-900">DDNS 动态域名解析任务</h2>
              <button class="bg-blue-600 text-white px-4 py-2 rounded-lg text-sm font-medium hover:bg-blue-700 transition-all" @click="openCreateTaskModal">
                创建 DDNS 任务
              </button>
            </div>

            <div class="grid grid-cols-1 md:grid-cols-2 gap-6">
              <div
                v-for="t in tasks"
                :key="t.id"
                class="glass-card p-6 rounded-2xl border-l-4 shadow-sm relative overflow-hidden"
                :class="t.status === 'running' ? 'border-l-green-500' : 'border-l-blue-500'"
              >
                <button
                  class="absolute right-2 top-2 z-20 inline-flex h-7 w-7 items-center justify-center rounded-md border border-red-200 text-red-500 hover:bg-red-50 hover:text-red-600 disabled:opacity-60"
                  title="删除任务"
                  aria-label="删除任务"
                  :disabled="loading"
                  @click="removeTask(t.id)"
                >
                  <svg viewBox="0 0 24 24" class="h-4 w-4 fill-none stroke-current" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                    <path d="M3 6h18" />
                    <path d="M8 6V4h8v2" />
                    <path d="M19 6l-1 14H6L5 6" />
                    <path d="M10 11v6" />
                    <path d="M14 11v6" />
                  </svg>
                </button>

                <div class="mb-4 pr-8">
                  <div>
                    <h4 class="font-bold text-lg text-slate-900">{{ t.host }}.{{ taskDomainName(t.domain_id) }}</h4>
                    <div class="mt-1 flex items-center gap-2">
                      <p class="text-xs text-slate-400">任务 #{{ t.id }}</p>
                      <span class="px-1.5 py-0.5 rounded text-[10px] leading-4 font-semibold" :class="t.status === 'running' ? 'bg-green-100 text-green-700' : 'bg-blue-100 text-blue-700'">
                        {{ t.status === 'running' ? '在线' : '暂停' }}
                      </span>
                    </div>
                  </div>
                </div>

                <div class="space-y-3 relative z-10">
                  <div class="flex justify-between text-sm items-center bg-slate-50 p-2 rounded">
                    <span class="text-slate-500">当前公网 IP</span>
                    <span class="font-mono font-bold text-slate-700">{{ t.last_ip || '-' }}</span>
                  </div>
                  <div class="flex justify-between text-sm items-center">
                    <span class="text-slate-500">最近更新</span>
                    <span class="text-slate-700">{{ formatTime(t.last_success_at || t.last_check_at) }}</span>
                  </div>
                  <div class="flex justify-between text-sm items-center">
                    <span class="text-slate-500">刷新频率</span>
                    <span class="text-slate-700">每 {{ t.interval_sec }} 秒</span>
                  </div>
                  <div class="pt-4 flex gap-2 flex-wrap">
                    <button
                      class="flex-1 min-w-[120px] bg-white border border-slate-200 py-2 rounded-lg text-xs font-bold hover:bg-slate-50"
                      :disabled="loading"
                      @click="logFilter.type = 'ddns_task'; logFilter.ddns_task_id = t.id; activeTab = 'logs'; loadLogs()"
                    >查看日志</button>
                    <button class="flex-1 min-w-[120px] bg-white border border-slate-200 py-2 rounded-lg text-xs font-bold hover:bg-slate-50" :disabled="loading" @click="openEditTaskModal(t)">编辑</button>
                    <button class="flex-1 min-w-[120px] bg-white border border-slate-200 py-2 rounded-lg text-xs font-bold hover:bg-slate-50" :disabled="loading" @click="runTaskOnce(t.id)">执行一次</button>
                    <button
                      class="flex-1 min-w-[120px] bg-white border border-slate-200 py-2 rounded-lg text-xs font-bold text-red-500 hover:bg-red-50"
                      :disabled="loading"
                      @click="t.status === 'running' ? pauseTask(t.id) : resumeTask(t.id)"
                    >
                      {{ t.status === 'running' ? '暂停' : '恢复' }}
                    </button>
                  </div>
                </div>
              </div>

              <div v-if="tasks.length === 0" class="glass-card p-10 rounded-2xl border border-dashed border-slate-200 text-center text-slate-400 md:col-span-2">暂无 DDNS 任务，点击右上角创建。</div>
            </div>

          </div>

          <div v-else-if="activeTab === 'providers'" id="page-providers">
            <div class="mb-6 flex items-center justify-between">
              <h2 class="text-2xl font-bold text-slate-900">供应商账号管理</h2>
              <button class="bg-blue-600 text-white px-4 py-2 rounded-lg text-sm font-medium hover:bg-blue-700 transition-all" @click="openCreateVendorModal">新增账号</button>
            </div>

            <div class="grid grid-cols-1 lg:grid-cols-3 gap-6 mb-6">
              <div v-for="item in vendors" :key="item.id" class="glass-card p-6 rounded-2xl border flex flex-col items-center">
                <div class="w-16 h-16 mb-4 rounded-2xl bg-blue-50 text-blue-600 flex items-center justify-center text-xl font-bold">{{ item.provider.slice(0, 2).toUpperCase() }}</div>
                <h5 class="font-bold">{{ item.name }}</h5>
                <p class="text-xs text-slate-400 mb-6">{{ item.provider }} 授权方式</p>
                <div class="w-full space-y-2 mb-6 text-sm">
                  <div class="flex justify-between px-3 py-2 bg-slate-50 rounded">
                    <span class="text-slate-500">账号 ID</span>
                    <span>#{{ item.id }}</span>
                  </div>
                  <div class="flex justify-between px-3 py-2 bg-slate-50 rounded">
                    <span class="text-slate-500">API Key</span>
                    <span class="truncate max-w-[160px]" :title="item.api_key">{{ item.api_key }}</span>
                  </div>
                </div>
                <div class="w-full grid grid-cols-3 gap-2 text-sm">
                  <button class="border py-2 rounded hover:bg-slate-50 font-medium" :disabled="loading" @click="openEditVendorModal(item)">编辑</button>
                  <button class="border py-2 rounded hover:bg-slate-50 font-medium" :disabled="loading" @click="verifyVendor(item.id)">校验</button>
                  <button class="border py-2 rounded hover:bg-red-50 text-red-600 font-medium" :disabled="loading" @click="removeVendor(item.id)">删除</button>
                </div>
              </div>

              <div v-if="vendors.length === 0" class="glass-card p-10 rounded-2xl border border-dashed border-slate-200 text-center text-slate-400 lg:col-span-3">暂无供应商账号，请先创建。</div>
            </div>

          </div>

          <div v-else-if="activeTab === 'logs'" id="page-logs">
            <h2 class="text-2xl font-bold text-slate-900 mb-6">系统日志</h2>

            <div class="glass-card rounded-2xl p-4 mb-4">
              <div class="grid grid-cols-1 md:grid-cols-4 gap-3">
                <select v-model="logFilter.type" class="rounded-lg border border-slate-200 px-3 py-2 text-sm outline-none focus:ring-2 focus:ring-blue-500" @change="onLogTypeChange">
                  <option value="">全部类型</option>
                  <option value="ddns_task">DDNS任务</option>
                  <option value="public_ip_check">公网IP检查</option>
                  <option value="operation">操作日志</option>
                </select>
                <select v-model="logFilter.result" class="rounded-lg border border-slate-200 px-3 py-2 text-sm outline-none focus:ring-2 focus:ring-blue-500">
                  <option value="">全部结果</option>
                  <option value="success">success</option>
                  <option value="failed">failed</option>
                </select>
                <select v-model="logFilter.ddns_task_id" class="rounded-lg border border-slate-200 px-3 py-2 text-sm outline-none focus:ring-2 focus:ring-blue-500" :disabled="logFilter.type !== 'ddns_task'">
                  <option :value="''">全部DDNS任务</option>
                  <option v-for="t in tasks" :key="t.id" :value="t.id">#{{ t.id }} {{ t.host }}.{{ taskDomainName(t.domain_id) }}</option>
                </select>
                <button class="rounded-lg bg-slate-900 px-4 py-2 text-sm font-medium text-white hover:bg-slate-700 transition-colors" :disabled="loading" @click="loadLogs">查询日志</button>
              </div>
            </div>

            <div class="glass-card rounded-2xl bg-slate-900 text-slate-300 p-6 font-mono text-sm leading-relaxed overflow-x-auto min-h-[500px]">
              <div v-for="(line, idx) in mergedLogLines" :key="idx" class="mb-2">
                <span class="text-slate-500">[{{ formatTime(line.timeRaw) }}]</span>
                <span class="mx-1" :class="line.levelClass">{{ line.level }}</span>
                {{ line.text }}
              </div>
              <div v-if="mergedLogLines.length === 0" class="text-slate-500">暂无日志数据</div>
            </div>
          </div>

          <div v-else id="page-settings">
            <h2 class="text-2xl font-bold text-slate-900 mb-6">系统全局设置</h2>
            <div class="max-w-3xl glass-card rounded-2xl p-8 space-y-8">
              <div>
                <h5 class="font-bold mb-4 text-slate-700">公网 IP 检查</h5>
                <div class="space-y-4 px-4">
                  <label class="flex items-center justify-between gap-3">
                    <span class="text-sm text-slate-600">public_ip_check（启用）</span>
                    <span class="relative inline-flex h-6 w-11 items-center">
                      <input v-model="settingsState.public_ip_check" type="checkbox" class="peer sr-only" />
                      <span class="absolute inset-0 rounded-full bg-slate-300 transition-colors peer-checked:bg-blue-600"></span>
                      <span class="absolute left-0.5 top-0.5 h-5 w-5 rounded-full bg-white shadow transition-transform peer-checked:translate-x-5"></span>
                    </span>
                  </label>

                  <label class="block">
                    <span class="text-sm text-slate-600">ip_check_interval_sec</span>
                    <input
                      v-model.number="settingsState.ip_check_interval_sec"
                      class="mt-2 w-full border border-slate-200 rounded-lg px-3 py-2 text-sm outline-none focus:ring-2 focus:ring-blue-500 disabled:bg-slate-100 disabled:text-slate-400"
                      type="number"
                      min="1"
                      :disabled="!settingsState.public_ip_check"
                    />
                  </label>

                  <label class="block">
                    <span class="text-sm text-slate-600">ip_sources（每行一个）</span>
                    <textarea
                      v-model="settingsState.ip_sources_text"
                      class="mt-2 w-full border border-slate-200 rounded-lg px-3 py-2 text-sm outline-none focus:ring-2 focus:ring-blue-500 h-28 disabled:bg-slate-100 disabled:text-slate-400"
                      :disabled="!settingsState.public_ip_check"
                    ></textarea>
                  </label>

                  <div v-if="settingsState.public_ip_check" class="rounded-lg border border-slate-200 bg-slate-50 p-3 space-y-2">
                    <div class="flex items-center justify-between text-sm">
                      <span class="text-slate-500">当前公网 IP</span>
                      <span class="font-mono font-bold text-slate-700">{{ settingsState.public_ip || '-' }}</span>
                    </div>
                    <div class="flex items-center justify-between text-sm">
                      <span class="text-slate-500">最后更新时间</span>
                      <span class="text-slate-700">{{ formatTime(settingsState.public_ip_last_checked_at) }}</span>
                    </div>
                    <div class="pt-1">
                      <button
                        class="bg-white border border-slate-200 px-3 py-1.5 rounded-lg text-xs font-bold hover:bg-slate-100 disabled:cursor-not-allowed disabled:opacity-60"
                        :disabled="loading"
                        @click="runPublicIPCheckNow"
                      >
                        立即检查
                      </button>
                    </div>
                  </div>
                </div>
              </div>

              <div>
                <h5 class="font-bold mb-4 text-slate-700">运行参数</h5>
                <div class="space-y-4 px-4">
                  <label class="block">
                    <span class="text-sm text-slate-600">sync_ttl_sec</span>
                    <input v-model.number="settingsState.sync_ttl_sec" class="mt-2 w-full border border-slate-200 rounded-lg px-3 py-2 text-sm outline-none focus:ring-2 focus:ring-blue-500" type="number" min="1" />
                  </label>

                  <label class="block">
                    <span class="text-sm text-slate-600">logs.retention_days</span>
                    <input v-model.number="settingsState.logs_retention_days" class="mt-2 w-full border border-slate-200 rounded-lg px-3 py-2 text-sm outline-none focus:ring-2 focus:ring-blue-500" type="number" min="1" />
                  </label>

                  <label class="block">
                    <span class="text-sm text-slate-600">ddns.default_interval_sec</span>
                    <input v-model.number="settingsState.ddns_default_interval_sec" class="mt-2 w-full border border-slate-200 rounded-lg px-3 py-2 text-sm outline-none focus:ring-2 focus:ring-blue-500" type="number" min="1" />
                  </label>

                  <div class="pt-2 flex gap-2">
                    <button class="bg-blue-600 text-white px-4 py-2 rounded-lg text-sm font-medium hover:bg-blue-700 transition-all" :disabled="loading" @click="saveSettings">保存设置</button>
                    <button class="bg-white border border-slate-200 px-4 py-2 rounded-lg text-sm font-medium hover:bg-slate-50" :disabled="loading" @click="loadSettings">重新加载</button>
                  </div>
                </div>
              </div>
            </div>
          </div>

          <div v-if="showRecordModal" class="fixed inset-0 z-50 bg-slate-900/40 p-4 flex items-center justify-center" @click.self="closeRecordModal">
            <div class="w-full max-w-3xl rounded-2xl border border-slate-200 bg-white shadow-xl">
              <div class="px-6 py-4 border-b border-slate-100 flex items-center justify-between">
                <h3 class="font-bold text-slate-900">{{ recordForm.id ? '编辑记录' : '新增记录' }} {{ selectedDomain ? `· ${selectedDomain.domain_name}` : '' }}</h3>
                <button class="text-slate-400 hover:text-slate-700" @click="closeRecordModal">关闭</button>
              </div>
              <div class="p-6">
                <div class="grid grid-cols-1 md:grid-cols-3 gap-4">
                  <label class="space-y-1">
                    <span class="text-sm text-slate-600">主机记录</span>
                    <input v-model="recordForm.host" type="text" class="w-full rounded-lg border border-slate-200 px-3 py-2 text-sm outline-none focus:ring-2 focus:ring-blue-500" placeholder="@ / www" />
                  </label>
                  <label class="space-y-1">
                    <span class="text-sm text-slate-600">类型</span>
                    <input v-model="recordForm.type" type="text" class="w-full rounded-lg border border-slate-200 px-3 py-2 text-sm outline-none focus:ring-2 focus:ring-blue-500" placeholder="A / AAAA / CNAME" />
                  </label>
                  <label class="space-y-1">
                    <span class="text-sm text-slate-600">TTL</span>
                    <input v-model.number="recordForm.ttl" type="number" min="1" class="w-full rounded-lg border border-slate-200 px-3 py-2 text-sm outline-none focus:ring-2 focus:ring-blue-500" />
                  </label>
                  <label class="space-y-1 md:col-span-2">
                    <span class="text-sm text-slate-600">记录值</span>
                    <input v-model="recordForm.value" type="text" class="w-full rounded-lg border border-slate-200 px-3 py-2 text-sm outline-none focus:ring-2 focus:ring-blue-500" placeholder="1.1.1.1 / example.com" />
                  </label>
                  <label class="space-y-1">
                    <span class="text-sm text-slate-600">线路（可选）</span>
                    <input v-model="recordForm.line" type="text" class="w-full rounded-lg border border-slate-200 px-3 py-2 text-sm outline-none focus:ring-2 focus:ring-blue-500" placeholder="default" />
                  </label>
                </div>
                <label class="inline-flex items-center gap-2 mt-4 text-sm text-slate-700">
                  <input v-model="recordForm.proxied" type="checkbox" /> 启用 Cloudflare 代理
                </label>
                <div class="pt-5 flex gap-2">
                  <button class="bg-blue-600 text-white px-4 py-2 rounded-lg text-sm font-medium hover:bg-blue-700 transition-all disabled:opacity-60" :disabled="loading || !selectedDomainId" @click="saveRecord">
                    {{ recordForm.id ? '更新记录' : '创建记录' }}
                  </button>
                  <button class="bg-white border border-slate-200 px-4 py-2 rounded-lg text-sm font-medium hover:bg-slate-50" :disabled="loading" @click="resetRecordForm">重置</button>
                </div>
              </div>
            </div>
          </div>

          <div v-if="showTaskModal" class="fixed inset-0 z-50 bg-slate-900/40 p-4 flex items-center justify-center" @click.self="closeTaskModal">
            <div class="w-full max-w-3xl rounded-2xl border border-slate-200 bg-white shadow-xl">
              <div class="px-6 py-4 border-b border-slate-100 flex items-center justify-between">
                <h3 class="font-bold text-slate-900">{{ taskForm.id ? '编辑 DDNS 任务' : '创建 DDNS 任务' }}</h3>
                <button class="text-slate-400 hover:text-slate-700" @click="closeTaskModal">关闭</button>
              </div>
              <div class="p-6">
                <div class="grid grid-cols-1 md:grid-cols-4 gap-4">
                  <label class="space-y-1">
                    <span class="text-sm text-slate-600">域名</span>
                    <select v-model.number="taskForm.domain_id" class="w-full rounded-lg border border-slate-200 px-3 py-2 text-sm outline-none focus:ring-2 focus:ring-blue-500">
                      <option :value="0">请选择域名</option>
                      <option v-for="d in domains" :key="d.id" :value="d.id">{{ d.domain_name }}</option>
                    </select>
                  </label>
                  <label class="space-y-1">
                    <span class="text-sm text-slate-600">Host</span>
                    <input v-model="taskForm.host" type="text" class="w-full rounded-lg border border-slate-200 px-3 py-2 text-sm outline-none focus:ring-2 focus:ring-blue-500" placeholder="@ / home" />
                  </label>
                  <label class="space-y-1">
                    <span class="text-sm text-slate-600">记录类型</span>
                    <select v-model="taskForm.record_type" class="w-full rounded-lg border border-slate-200 px-3 py-2 text-sm outline-none focus:ring-2 focus:ring-blue-500">
                      <option value="A">A</option>
                      <option value="AAAA">AAAA</option>
                    </select>
                  </label>
                  <label class="space-y-1">
                    <span class="text-sm text-slate-600">刷新间隔(秒)</span>
                    <input v-model.number="taskForm.interval_sec" type="number" min="1" class="w-full rounded-lg border border-slate-200 px-3 py-2 text-sm outline-none focus:ring-2 focus:ring-blue-500" />
                  </label>
                </div>
                <label v-if="taskForm.id" class="space-y-1 block mt-4 max-w-[220px]">
                  <span class="text-sm text-slate-600">状态</span>
                  <select v-model="taskForm.status" class="w-full rounded-lg border border-slate-200 px-3 py-2 text-sm outline-none focus:ring-2 focus:ring-blue-500">
                    <option value="running">running</option>
                    <option value="paused">paused</option>
                  </select>
                </label>
                <div class="pt-5 flex gap-2">
                  <button class="bg-blue-600 text-white px-4 py-2 rounded-lg text-sm font-medium hover:bg-blue-700 transition-all disabled:opacity-60" :disabled="loading" @click="saveTask">
                    {{ taskForm.id ? '保存任务' : '创建任务' }}
                  </button>
                  <button class="bg-white border border-slate-200 px-4 py-2 rounded-lg text-sm font-medium hover:bg-slate-50" :disabled="loading" @click="resetTaskForm">重置</button>
                </div>
              </div>
            </div>
          </div>

          <div v-if="showVendorModal" class="fixed inset-0 z-50 bg-slate-900/40 p-4 flex items-center justify-center" @click.self="closeVendorModal">
            <div class="w-full max-w-3xl rounded-2xl border border-slate-200 bg-white shadow-xl">
              <div class="px-6 py-4 border-b border-slate-100 flex items-center justify-between">
                <h3 class="font-bold text-slate-900">{{ vendorForm.id ? '编辑供应商账号' : '新增供应商账号' }}</h3>
                <button class="text-slate-400 hover:text-slate-700" @click="closeVendorModal">关闭</button>
              </div>
              <div class="p-6">
                <div class="grid grid-cols-1 md:grid-cols-2 gap-4">
                  <label class="space-y-1">
                    <span class="text-sm text-slate-600">名称</span>
                    <input v-model="vendorForm.name" type="text" class="w-full rounded-lg border border-slate-200 px-3 py-2 text-sm outline-none focus:ring-2 focus:ring-blue-500" placeholder="例如：主阿里云账号" />
                  </label>
                  <label class="space-y-1">
                    <span class="text-sm text-slate-600">Provider</span>
                    <select v-model="vendorForm.provider" class="w-full rounded-lg border border-slate-200 px-3 py-2 text-sm outline-none focus:ring-2 focus:ring-blue-500">
                      <option value="aliyun">aliyun</option>
                      <option value="cloudflare">cloudflare</option>
                    </select>
                  </label>
                  <label class="space-y-1">
                    <span class="text-sm text-slate-600">API Key</span>
                    <input v-model="vendorForm.api_key" type="text" class="w-full rounded-lg border border-slate-200 px-3 py-2 text-sm outline-none focus:ring-2 focus:ring-blue-500" />
                  </label>
                  <label class="space-y-1">
                    <span class="text-sm text-slate-600">API Secret {{ vendorForm.id ? '(留空不更新)' : '' }}</span>
                    <input v-model="vendorForm.api_secret" type="password" class="w-full rounded-lg border border-slate-200 px-3 py-2 text-sm outline-none focus:ring-2 focus:ring-blue-500" />
                  </label>
                  <label class="space-y-1 md:col-span-2">
                    <span class="text-sm text-slate-600">Extra(JSON，可选)</span>
                    <textarea v-model="vendorForm.extra" rows="4" class="w-full rounded-lg border border-slate-200 px-3 py-2 text-sm outline-none focus:ring-2 focus:ring-blue-500" placeholder='{"zone_type":"public"}'></textarea>
                  </label>
                </div>
                <div class="pt-5 flex gap-2">
                  <button class="bg-blue-600 text-white px-4 py-2 rounded-lg text-sm font-medium hover:bg-blue-700 transition-all disabled:opacity-60" :disabled="loading" @click="saveVendor">
                    {{ vendorForm.id ? '保存更新' : '创建供应商' }}
                  </button>
                  <button class="bg-white border border-slate-200 px-4 py-2 rounded-lg text-sm font-medium hover:bg-slate-50" :disabled="loading" @click="resetVendorForm">重置</button>
                </div>
              </div>
            </div>
          </div>
        </div>
      </main>
    </section>
  </main>
</template>

<style>
.app-font {
  font-family: 'Inter', 'Microsoft YaHei', 'PingFang SC', 'Noto Sans SC', sans-serif;
}

.glass-card {
  background: rgba(255, 255, 255, 0.8);
  backdrop-filter: blur(10px);
  border: 1px solid rgba(229, 231, 235, 0.5);
}

.sidebar-item {
  color: #475569;
  transition: all 0.15s ease;
}

.sidebar-item:hover {
  background-color: #f8fafc;
}

.sidebar-item.active {
  background-color: rgba(59, 130, 246, 0.1);
  border-right: 4px solid #3b82f6;
  color: #3b82f6;
}

::-webkit-scrollbar {
  width: 6px;
  height: 6px;
}

::-webkit-scrollbar-track {
  background: #f1f1f1;
}

::-webkit-scrollbar-thumb {
  background: #d1d5db;
  border-radius: 10px;
}

::-webkit-scrollbar-thumb:hover {
  background: #9ca3af;
}
</style>
