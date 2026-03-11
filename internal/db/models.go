package db

import "time"

type Provider string

const (
	ProviderAliyun     Provider = "aliyun"
	ProviderCloudflare Provider = "cloudflare"
)

type DDNSTaskStatus string

const (
	DDNSTaskStatusRunning DDNSTaskStatus = "running"
	DDNSTaskStatusPaused  DDNSTaskStatus = "paused"
)

type DDNSLogAction string

const (
	DDNSLogActionSkip   DDNSLogAction = "skip"
	DDNSLogActionUpdate DDNSLogAction = "update"
	DDNSLogActionCreate DDNSLogAction = "create"
)

type DDNSLogResult string

const (
	DDNSLogResultSuccess DDNSLogResult = "success"
	DDNSLogResultFailed  DDNSLogResult = "failed"
)

type Admin struct {
	ID                 int64      `db:"id"`
	Username           string     `db:"username"`
	PasswordHash       string     `db:"password_hash"`
	MustChangePassword bool       `db:"must_change_password"`
	LastLoginAt        *time.Time `db:"last_login_at"`
	CreatedAt          time.Time  `db:"created_at"`
	UpdatedAt          time.Time  `db:"updated_at"`
}

type Session struct {
	ID        int64      `db:"id"`
	AdminID   int64      `db:"admin_id"`
	TokenHash string     `db:"token_hash"`
	ExpiresAt time.Time  `db:"expires_at"`
	RevokedAt *time.Time `db:"revoked_at"`
	CreatedAt time.Time  `db:"created_at"`
}

type Vendor struct {
	ID              int64     `db:"id"`
	Name            string    `db:"name"`
	Provider        Provider  `db:"provider"`
	APIKey          string    `db:"api_key"`
	APISecretCipher string    `db:"api_secret_cipher"`
	ExtraJSON       *string   `db:"extra_json"`
	CreatedAt       time.Time `db:"created_at"`
	UpdatedAt       time.Time `db:"updated_at"`
}

type Domain struct {
	ID             int64      `db:"id"`
	VendorID       int64      `db:"vendor_id"`
	RemoteDomainID *string    `db:"remote_domain_id"`
	DomainName     string     `db:"domain_name"`
	LastSyncedAt   *time.Time `db:"last_synced_at"`
	CreatedAt      time.Time  `db:"created_at"`
	UpdatedAt      time.Time  `db:"updated_at"`
}

type Record struct {
	ID             int64     `db:"id"`
	DomainID       int64     `db:"domain_id"`
	RemoteRecordID string    `db:"remote_record_id"`
	Host           string    `db:"host"`
	Type           string    `db:"type"`
	Value          string    `db:"value"`
	TTL            int       `db:"ttl"`
	Proxied        bool      `db:"proxied"`
	Line           *string   `db:"line"`
	UpdatedAt      time.Time `db:"updated_at"`
}

type DDNSTask struct {
	ID                  int64          `db:"id"`
	DomainID            int64          `db:"domain_id"`
	Host                string         `db:"host"`
	RecordType          string         `db:"record_type"`
	IntervalSec         int            `db:"interval_sec"`
	Status              DDNSTaskStatus `db:"status"`
	LastIP              *string        `db:"last_ip"`
	LastCheckAt         *time.Time     `db:"last_check_at"`
	LastSuccessAt       *time.Time     `db:"last_success_at"`
	ConsecutiveFailures int            `db:"consecutive_failures"`
	NextRunAt           time.Time      `db:"next_run_at"`
	LastError           *string        `db:"last_error"`
	Version             int            `db:"version"`
	CreatedAt           time.Time      `db:"created_at"`
	UpdatedAt           time.Time      `db:"updated_at"`
}

type DDNSLog struct {
	ID        int64         `db:"id"`
	TaskID    int64         `db:"task_id"`
	OldIP     *string       `db:"old_ip"`
	NewIP     *string       `db:"new_ip"`
	Action    DDNSLogAction `db:"action"`
	Result    DDNSLogResult `db:"result"`
	ErrorMsg  *string       `db:"error_msg"`
	LatencyMS int           `db:"latency_ms"`
	CreatedAt time.Time     `db:"created_at"`
}

type AuditLog struct {
	ID         int64     `db:"id"`
	Actor      string    `db:"actor"`
	Action     string    `db:"action"`
	TargetType string    `db:"target_type"`
	TargetID   string    `db:"target_id"`
	DetailJSON *string   `db:"detail_json"`
	CreatedAt  time.Time `db:"created_at"`
}

type SystemSetting struct {
	Key       string    `db:"key"`
	Value     string    `db:"value"`
	UpdatedAt time.Time `db:"updated_at"`
}

type PublicIPCheckSetting struct {
	ID            int64      `db:"id"`
	Enabled       bool       `db:"enabled"`
	IntervalSec   int        `db:"interval_sec"`
	IPSourcesJSON string     `db:"ip_sources_json"`
	PublicIP      *string    `db:"public_ip"`
	LastCheckedAt *time.Time `db:"last_checked_at"`
	UpdatedAt     time.Time  `db:"updated_at"`
}

type SystemLog struct {
	ID         int64     `db:"id"`
	Type       string    `db:"type"`
	Result     string    `db:"result"`
	DDNSTaskID *int64    `db:"ddns_task_id"`
	Actor      *string   `db:"actor"`
	Action     *string   `db:"action"`
	TargetType *string   `db:"target_type"`
	TargetID   *string   `db:"target_id"`
	OldIP      *string   `db:"old_ip"`
	NewIP      *string   `db:"new_ip"`
	ErrorMsg   *string   `db:"error_msg"`
	LatencyMS  *int      `db:"latency_ms"`
	DetailJSON *string   `db:"detail_json"`
	CreatedAt  time.Time `db:"created_at"`
}
