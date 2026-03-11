# LiteDNS 详细设计 - 20 API 与数据模型

## 1. API 设计约定
### 1.1 基础约定
- Base Path: `/api/v1`
- 数据格式：`application/json; charset=utf-8`
- 时间格式：RFC3339（UTC 存储，前端按本地时区展示）
- 认证方式：`Authorization: Bearer <token>`

### 1.2 通用响应结构
```json
{
  "code": "OK",
  "message": "success",
  "data": {}
}
```

### 1.3 通用错误结构
```json
{
  "code": "VALIDATION_ERROR",
  "message": "provider is required",
  "request_id": "9f9a4f5f9b3b4ff8"
}
```

### 1.4 错误码
| 错误码 | HTTP 状态码 | 说明 |
| --- | --- | --- |
| `OK` | 200 | 成功 |
| `UNAUTHORIZED` | 401 | 未登录或 token 失效 |
| `FORBIDDEN` | 403 | 权限不足或会话已撤销 |
| `VALIDATION_ERROR` | 400 | 参数校验失败 |
| `NOT_FOUND` | 404 | 资源不存在 |
| `CONFLICT` | 409 | 资源冲突（唯一键） |
| `UPSTREAM_ERROR` | 502 | 上游 Provider 调用失败 |
| `UPSTREAM_TIMEOUT` | 504 | 上游调用超时 |
| `INTERNAL_ERROR` | 500 | 服务内部错误 |

## 2. API 契约
## 2.1 Auth
### `POST /auth/login`
- 请求字段：`username`, `password`
- 响应字段：`token`, `expires_at`, `must_change_password`

### `POST /auth/logout`
- 行为：撤销当前会话 token。

### `POST /auth/change-password`
- 请求字段：`old_password`, `new_password`
- 规则：首次登录必须先调用本接口。

### `GET /auth/me`
- 响应字段：`id`, `username`, `must_change_password`, `last_login_at`

## 2.2 Vendor
### `GET /vendors`
- 查询当前供应商账号列表。

### `POST /vendors`
- 请求字段：
  - `name`: 供应商别名
  - `provider`: `aliyun` 或 `cloudflare`
  - `api_key`: AccessKey ID 或 API Token ID（根据 provider）
  - `api_secret`: AccessKey Secret 或 Token Secret（密文入库）
  - `extra`: 扩展字段（JSON，可选）

### `PUT /vendors/:id`
- 更新供应商配置，支持局部更新。

### `DELETE /vendors/:id`
- 删除供应商；若关联域名/任务存在则返回 `CONFLICT`。

### `POST /vendors/:id/verify`
- 触发一次上游鉴权校验并返回结果。

## 2.3 Domain & Record
### `GET /domains?vendor_id=<id>`
- 行为：若缓存过期（`last_synced_at + sync_ttl_sec < now`）则阻塞同步后返回。

### `POST /domains/:id/sync`
- 行为：手动触发域名记录同步，返回同步摘要（新增/更新/删除计数）。

### `GET /domains/:id/records`
- 行为：同样遵循缓存过期阻塞同步策略。

### `POST /domains/:id/records`
- 请求字段：`host`, `type`, `value`, `ttl`, `proxied`, `line`
- 行为：先写远端，成功后回写本地缓存。

### `PUT /records/:id`
- 请求字段：`value`, `ttl`, `proxied`, `line`
- 行为：远端更新成功后回写本地。

### `DELETE /records/:id`
- 行为：先删远端，再删本地缓存。

## 2.4 DDNS
### `GET /ddns/tasks`
- 支持按 `status`, `domain_id` 过滤。

### `POST /ddns/tasks`
- 请求字段：`domain_id`, `host`, `record_type`, `interval_sec`
- 默认值：`interval_sec` 缺省时为 `300`。
- 约束：仅当 `public_ip_check=true` 时允许创建。

### `PUT /ddns/tasks/:id`
- 可更新字段：`interval_sec`, `status`

### `POST /ddns/tasks/:id/pause`
- 将任务状态改为 `paused`。

### `POST /ddns/tasks/:id/resume`
- 将任务状态改为 `running` 并重置 `next_run_at`。

### `POST /ddns/tasks/:id/run-once`
- 立即触发一次执行，不改变任务周期配置。

## 2.5 Logs & Settings
### `GET /logs`
- 统一查询系统日志（按时间倒序）。
- 查询参数：
  - `type`：`ddns_task` / `public_ip_check` / `operation`
  - `result`：结果（如 `success` / `failed`）
  - `ddns_task_id`：仅当 `type=ddns_task` 时可用（否则返回参数校验错误）
  - `start` / `end`：时间范围（可选）
- 未传筛选参数时返回全部日志（时间倒序）。

### `GET /settings`
- 返回系统可见配置与默认值。
- 包含公网 IP 检查运行态：`public_ip`、`public_ip_last_checked_at`。

### `PUT /settings`
- 仅允许更新可热更新项：`sync_ttl_sec`、`logs.retention_days`、`ddns.default_interval_sec`、`public_ip_check`、`ip_check_interval_sec`、`ip_sources`。
- 其中公网 IP 检查相关配置（`public_ip_check`、`ip_check_interval_sec`、`ip_sources`）写入独立表 `public_ip_check_settings`。
- 当 `public_ip_check=false` 时，若存在任意 DDNS 任务，更新请求会被拒绝。

## 3. Provider 抽象与映射
## 3.1 统一接口
```text
VerifyCredential(ctx, credential) error
ListDomains(ctx, account) ([]DomainRemote, error)
ListRecords(ctx, account, domain) ([]RecordRemote, error)
UpsertRecord(ctx, account, domain, host, type, value, ttl, extra) (recordID, error)
DeleteRecord(ctx, account, domain, recordID) error
```

## 3.2 映射规则
- 阿里云：
  - `host` 对应 `RR`
  - `type` 对应 `Type`
  - `value` 对应 `Value`
  - `record_id` 对应 `RecordId`
- Cloudflare：
  - `host + domain` 映射 `name`
  - `type` 对应 `type`
  - `value` 对应 `content`
  - `proxied` 对应 `proxied`
  - `record_id` 对应 `id`

## 3.3 凭证约束
- Cloudflare 仅支持 API Token 模式。
- 凭证验证失败时禁止保存或更新生效。

## 4. SQLite 数据模型
## 4.1 表结构
### `admins`
| 字段 | 类型 | 约束 | 说明 |
| --- | --- | --- | --- |
| `id` | INTEGER | PK AUTOINCREMENT | 管理员 ID |
| `username` | TEXT | UNIQUE NOT NULL | 登录名 |
| `password_hash` | TEXT | NOT NULL | 密码哈希 |
| `must_change_password` | INTEGER | NOT NULL DEFAULT 1 | 是否强制改密 |
| `last_login_at` | DATETIME | NULL | 最近登录时间 |
| `created_at` | DATETIME | NOT NULL | 创建时间 |
| `updated_at` | DATETIME | NOT NULL | 更新时间 |

### `sessions`
| 字段 | 类型 | 约束 | 说明 |
| --- | --- | --- | --- |
| `id` | INTEGER | PK AUTOINCREMENT | 会话 ID |
| `admin_id` | INTEGER | NOT NULL | 管理员 ID |
| `token_hash` | TEXT | UNIQUE NOT NULL | token 哈希 |
| `expires_at` | DATETIME | NOT NULL | 过期时间 |
| `revoked_at` | DATETIME | NULL | 撤销时间 |
| `created_at` | DATETIME | NOT NULL | 创建时间 |

### `vendors`
| 字段 | 类型 | 约束 | 说明 |
| --- | --- | --- | --- |
| `id` | INTEGER | PK AUTOINCREMENT | 供应商账号 ID |
| `name` | TEXT | NOT NULL | 用户自定义别名 |
| `provider` | TEXT | NOT NULL | `aliyun`/`cloudflare` |
| `api_key` | TEXT | NOT NULL | 凭证主键 |
| `api_secret_cipher` | TEXT | NOT NULL | 加密后的密钥 |
| `extra_json` | TEXT | NULL | 扩展配置 |
| `created_at` | DATETIME | NOT NULL | 创建时间 |
| `updated_at` | DATETIME | NOT NULL | 更新时间 |

### `domains`
| 字段 | 类型 | 约束 | 说明 |
| --- | --- | --- | --- |
| `id` | INTEGER | PK AUTOINCREMENT | 域名 ID |
| `vendor_id` | INTEGER | NOT NULL | 所属供应商 |
| `remote_domain_id` | TEXT | NULL | 供应商域名 ID |
| `domain_name` | TEXT | NOT NULL | 主域名 |
| `last_synced_at` | DATETIME | NULL | 最近同步时间 |
| `created_at` | DATETIME | NOT NULL | 创建时间 |
| `updated_at` | DATETIME | NOT NULL | 更新时间 |

唯一约束：`UNIQUE(vendor_id, domain_name)`

### `records`
| 字段 | 类型 | 约束 | 说明 |
| --- | --- | --- | --- |
| `id` | INTEGER | PK AUTOINCREMENT | 本地记录 ID |
| `domain_id` | INTEGER | NOT NULL | 所属域名 |
| `remote_record_id` | TEXT | NOT NULL | 供应商记录 ID |
| `host` | TEXT | NOT NULL | 主机记录 |
| `type` | TEXT | NOT NULL | 记录类型 |
| `value` | TEXT | NOT NULL | 记录值 |
| `ttl` | INTEGER | NOT NULL | TTL |
| `proxied` | INTEGER | NOT NULL DEFAULT 0 | 代理开关 |
| `line` | TEXT | NULL | 线路/策略 |
| `updated_at` | DATETIME | NOT NULL | 更新时间 |

唯一约束：`UNIQUE(domain_id, host, type)`

### `ddns_tasks`
| 字段 | 类型 | 约束 | 说明 |
| --- | --- | --- | --- |
| `id` | INTEGER | PK AUTOINCREMENT | 任务 ID |
| `domain_id` | INTEGER | NOT NULL | 域名 ID |
| `host` | TEXT | NOT NULL | 子域名主机部分 |
| `record_type` | TEXT | NOT NULL | `A`/`AAAA` |
| `interval_sec` | INTEGER | NOT NULL DEFAULT 300 | 轮询周期 |
| `status` | TEXT | NOT NULL DEFAULT 'running' | `running`/`paused` |
| `last_ip` | TEXT | NULL | 上次成功 IP |
| `last_check_at` | DATETIME | NULL | 上次检查时间 |
| `last_success_at` | DATETIME | NULL | 上次成功时间 |
| `consecutive_failures` | INTEGER | NOT NULL DEFAULT 0 | 连续失败次数 |
| `next_run_at` | DATETIME | NOT NULL | 下次执行时间 |
| `last_error` | TEXT | NULL | 最近错误信息 |
| `version` | INTEGER | NOT NULL DEFAULT 1 | 乐观并发版本号 |
| `created_at` | DATETIME | NOT NULL | 创建时间 |
| `updated_at` | DATETIME | NOT NULL | 更新时间 |

唯一约束：`UNIQUE(domain_id, host, record_type)`

### `system_logs`
| 字段 | 类型 | 约束 | 说明 |
| --- | --- | --- | --- |
| `id` | INTEGER | PK AUTOINCREMENT | 日志 ID |
| `type` | TEXT | NOT NULL | 日志类型：`ddns_task`/`public_ip_check`/`operation` |
| `result` | TEXT | NOT NULL | 执行结果 |
| `ddns_task_id` | INTEGER | NULL | DDNS 任务 ID（仅 DDNS 类型） |
| `actor` | TEXT | NULL | 操作人（操作日志） |
| `action` | TEXT | NULL | 事件动作（如 `ddns_task.update`） |
| `target_type` | TEXT | NULL | 目标类型（操作日志） |
| `target_id` | TEXT | NULL | 目标 ID（操作日志） |
| `old_ip` | TEXT | NULL | 旧 IP（DDNS 类型） |
| `new_ip` | TEXT | NULL | 新 IP（DDNS 类型） |
| `error_msg` | TEXT | NULL | 错误信息 |
| `latency_ms` | INTEGER | NULL | 执行耗时 |
| `detail_json` | TEXT | NULL | 扩展详情 |
| `created_at` | DATETIME | NOT NULL | 记录时间 |

### `system_settings`
| 字段 | 类型 | 约束 | 说明 |
| --- | --- | --- | --- |
| `key` | TEXT | PK | 配置键 |
| `value` | TEXT | NOT NULL | 配置值（JSON 字符串） |
| `updated_at` | DATETIME | NOT NULL | 更新时间 |

说明：该表用于存储通用键值配置（如 `sync_ttl_sec`、`logs.retention_days`、`ddns.default_interval_sec`），不再承载公网 IP 检查配置。

### `public_ip_check_settings`
| 字段 | 类型 | 约束 | 说明 |
| --- | --- | --- | --- |
| `id` | INTEGER | PK, CHECK(`id` = 1) | 单例配置行 ID |
| `enabled` | INTEGER | NOT NULL, CHECK IN (0,1) | 是否启用公网 IP 检查 |
| `interval_sec` | INTEGER | NOT NULL, CHECK > 0 | 检查间隔（秒） |
| `ip_sources_json` | TEXT | NOT NULL | IP 源列表（JSON 数组字符串） |
| `public_ip` | TEXT | NULL | 最近一次成功获取的公网 IP |
| `last_checked_at` | DATETIME | NULL | 最近一次执行公网 IP 检查时间 |
| `updated_at` | DATETIME | NOT NULL | 更新时间 |

## 4.2 索引设计
- `idx_domains_vendor_id` on `domains(vendor_id)`
- `idx_records_domain_id` on `records(domain_id)`
- `idx_ddns_tasks_status_next_run_at` on `ddns_tasks(status, next_run_at)`
- `idx_system_logs_created_at` on `system_logs(created_at)`
- `idx_system_logs_type_result_created_at` on `system_logs(type, result, created_at)`
- `idx_system_logs_ddns_task_created_at` on `system_logs(ddns_task_id, created_at)`

## 4.3 枚举定义
- `provider`: `aliyun` | `cloudflare`
- `ddns_tasks.status`: `running` | `paused`
- `system_logs.type`: `ddns_task` | `public_ip_check` | `operation`

## 5. 数据一致性与事务边界
## 5.1 记录写入原则
- 远端优先：先调用 Provider 成功，再更新本地缓存。
- 本地更新失败时记录错误并在下一次同步纠偏。

## 5.2 同步事务边界
- 单域名同步作为一个本地事务单元：
  - 写 `domains.last_synced_at`
  - 批量 upsert `records`
  - 删除远端不存在的本地缓存记录（可选策略）

## 5.3 幂等与并发
- `ddns_tasks` 以 `(domain_id, host, record_type)` 保证唯一。
- 执行器更新任务时使用 `version` 字段实现乐观并发控制，避免重复执行。
- `run-once` 与调度触发并发冲突时，以首次抢占成功者执行。

## 5.4 同步超时与分页
- 上游请求超时：默认 10 秒，可配置。
- 分页拉取策略：
  - 阿里云：按官方分页参数循环拉取至完成。
  - Cloudflare：按 `page/per_page` 拉取至最后一页。
- 任一页失败则本次同步失败，不更新 `last_synced_at`。

## 6. 安全与密钥轮换策略
## 6.1 存储策略
- `api_secret` 不以明文落库，仅存储 `api_secret_cipher`。
- 密文建议格式：`base64(nonce|ciphertext|tag)`。

## 6.2 轮换策略
- 支持双阶段轮换：
  1. 启动时加载新旧主密钥（旧可选）。
  2. 读取旧密文后使用新密钥重加密并覆盖。
- 轮换完成后移除旧密钥配置。

## 7. 可测试性与验收对齐
### 7.1 Mock Provider
- 所有 Provider 调用通过接口注入，支持 Mock。
- 测试覆盖成功、超时、限流、鉴权失败、记录不存在自动创建。

### 7.2 核心验收点
- 接口入参与错误码符合本文档定义。
- 表结构与唯一约束可支持全部 V1 场景。
- 同步、DDNS、日志清理流程在状态字段与日志上可追溯。

### 7.3 兼容性约束
- 非破坏性新增字段可直接向后兼容。
- 破坏性变更（字段重命名、语义改变、状态枚举变化）必须升级文档版本并给出迁移说明。
