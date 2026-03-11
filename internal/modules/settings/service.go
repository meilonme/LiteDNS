package settings

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"litedns/internal/config"
	"litedns/internal/modules/logs"
)

type UpdateInput struct {
	SyncTTLSec             *int      `json:"sync_ttl_sec"`
	LogsRetentionDays      *int      `json:"logs.retention_days"`
	DDNSDefaultIntervalSec *int      `json:"ddns.default_interval_sec"`
	PublicIPCheck          *bool     `json:"public_ip_check"`
	IPCheckIntervalSec     *int      `json:"ip_check_interval_sec"`
	IPSources              *[]string `json:"ip_sources"`
}

type publicIPCheckConfig struct {
	Enabled       bool
	IntervalSec   int
	IPSources     []string
	PublicIP      *string
	LastCheckedAt *time.Time
}

var (
	ErrPublicIPCheckDisabled = errors.New("public_ip_check is disabled")
	ErrPublicIPCheckRunning  = errors.New("public ip check is already running")
)

type Service struct {
	db                   *sql.DB
	defaults             map[string]any
	defaultPublicIPCheck publicIPCheckConfig
	logs                 *logs.Service
	httpClient           *http.Client

	ipCheckMu      sync.Mutex
	ipCheckRunning bool
}

func NewService(db *sql.DB, cfg config.Config, logsSvc *logs.Service) *Service {
	defaultInterval := cfg.IP.CheckIntervalSec
	if defaultInterval <= 0 {
		defaultInterval = 300
	}
	defaultSources := normalizeSources(cfg.IP.Sources)

	return &Service{
		db: db,
		defaults: map[string]any{
			"sync_ttl_sec":              cfg.Sync.TTLSeconds,
			"logs.retention_days":       cfg.Logs.RetentionDays,
			"ddns.default_interval_sec": cfg.DDNS.DefaultIntervalSec,
		},
		defaultPublicIPCheck: publicIPCheckConfig{
			Enabled:     cfg.IP.PublicIPCheck,
			IntervalSec: defaultInterval,
			IPSources:   append([]string(nil), defaultSources...),
		},
		logs:       logsSvc,
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

func (s *Service) Get(ctx context.Context) (map[string]any, error) {
	out := map[string]any{}
	for k, v := range s.defaults {
		out[k] = v
	}

	publicIPCheckCfg, err := s.loadPublicIPCheckConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("load public ip check settings: %w", err)
	}
	out["public_ip_check"] = publicIPCheckCfg.Enabled
	out["ip_check_interval_sec"] = publicIPCheckCfg.IntervalSec
	out["ip_sources"] = append([]string(nil), publicIPCheckCfg.IPSources...)
	out["public_ip"] = nullableStringPtr(publicIPCheckCfg.PublicIP)
	out["public_ip_last_checked_at"] = publicIPCheckCfg.LastCheckedAt

	rows, err := s.db.QueryContext(ctx, `SELECT key, value FROM system_settings`)
	if err != nil {
		return nil, fmt.Errorf("list system settings: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			key string
			raw string
		)
		if err := rows.Scan(&key, &raw); err != nil {
			return nil, fmt.Errorf("scan system setting: %w", err)
		}
		if isPublicIPSettingKey(key) {
			continue
		}
		var v any
		if err := json.Unmarshal([]byte(raw), &v); err != nil {
			continue
		}
		out[key] = v
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate system settings: %w", err)
	}
	return out, nil
}

func (s *Service) Update(ctx context.Context, in UpdateInput) (map[string]any, error) {
	now := time.Now().UTC()
	if in.SyncTTLSec != nil {
		if *in.SyncTTLSec <= 0 {
			return nil, fmt.Errorf("sync_ttl_sec must be positive")
		}
		if err := s.upsert(ctx, "sync_ttl_sec", *in.SyncTTLSec, now); err != nil {
			return nil, err
		}
	}
	if in.LogsRetentionDays != nil {
		if *in.LogsRetentionDays <= 0 {
			return nil, fmt.Errorf("logs.retention_days must be positive")
		}
		if err := s.upsert(ctx, "logs.retention_days", *in.LogsRetentionDays, now); err != nil {
			return nil, err
		}
	}
	if in.DDNSDefaultIntervalSec != nil {
		if *in.DDNSDefaultIntervalSec <= 0 {
			return nil, fmt.Errorf("ddns.default_interval_sec must be positive")
		}
		if err := s.upsert(ctx, "ddns.default_interval_sec", *in.DDNSDefaultIntervalSec, now); err != nil {
			return nil, err
		}
	}
	if in.PublicIPCheck != nil || in.IPCheckIntervalSec != nil || in.IPSources != nil {
		if in.PublicIPCheck != nil && !*in.PublicIPCheck {
			hasTasks, err := s.hasDDNSTasks(ctx)
			if err != nil {
				return nil, err
			}
			if hasTasks {
				return nil, fmt.Errorf("cannot disable public_ip_check while ddns tasks exist")
			}
		}

		current, err := s.loadPublicIPCheckConfig(ctx)
		if err != nil {
			return nil, fmt.Errorf("load public ip check settings: %w", err)
		}
		if in.PublicIPCheck != nil {
			current.Enabled = *in.PublicIPCheck
		}
		if in.IPCheckIntervalSec != nil {
			if *in.IPCheckIntervalSec <= 0 {
				return nil, fmt.Errorf("ip_check_interval_sec must be positive")
			}
			current.IntervalSec = *in.IPCheckIntervalSec
		}
		if in.IPSources != nil {
			current.IPSources = normalizeSources(*in.IPSources)
		}
		if current.Enabled && len(current.IPSources) == 0 {
			return nil, fmt.Errorf("ip_sources cannot be empty when public_ip_check is enabled")
		}
		if err := s.upsertPublicIPCheckConfig(ctx, current, now); err != nil {
			return nil, err
		}
		if err := s.deleteLegacyIPSourceSetting(ctx); err != nil {
			return nil, err
		}
	}
	return s.Get(ctx)
}

func (s *Service) EffectiveIPSources(ctx context.Context) []string {
	cfg, err := s.loadPublicIPCheckConfig(ctx)
	if err != nil {
		return append([]string(nil), s.defaultPublicIPCheck.IPSources...)
	}
	return append([]string(nil), cfg.IPSources...)
}

func (s *Service) EffectivePublicIPCheck(ctx context.Context) bool {
	cfg, err := s.loadPublicIPCheckConfig(ctx)
	if err != nil {
		return s.defaultPublicIPCheck.Enabled
	}
	return cfg.Enabled
}

func (s *Service) EffectiveIPCheckIntervalSec(ctx context.Context) int {
	cfg, err := s.loadPublicIPCheckConfig(ctx)
	if err != nil {
		return s.defaultPublicIPCheck.IntervalSec
	}
	return cfg.IntervalSec
}

func (s *Service) EffectiveDDNSInterval(ctx context.Context) int {
	vals, err := s.Get(ctx)
	if err != nil {
		if v, ok := s.defaults["ddns.default_interval_sec"].(int); ok {
			return v
		}
		return 300
	}
	raw, ok := vals["ddns.default_interval_sec"]
	if !ok {
		return 300
	}
	switch v := raw.(type) {
	case float64:
		return int(v)
	case int:
		return v
	default:
		return 300
	}
}

func (s *Service) PublicIPForRecordType(ctx context.Context, recordType string) (string, error) {
	cfg, err := s.loadPublicIPCheckConfig(ctx)
	if err != nil {
		return "", err
	}
	if !cfg.Enabled {
		return "", fmt.Errorf("public_ip_check is disabled")
	}
	if cfg.PublicIP == nil || strings.TrimSpace(*cfg.PublicIP) == "" {
		return "", fmt.Errorf("public ip is not ready yet")
	}
	ip := strings.TrimSpace(*cfg.PublicIP)
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return "", fmt.Errorf("invalid public ip in settings")
	}
	switch strings.ToUpper(strings.TrimSpace(recordType)) {
	case "A":
		if parsed.To4() == nil {
			return "", fmt.Errorf("public ip is not ipv4")
		}
	case "AAAA":
		if parsed.To4() != nil {
			return "", fmt.Errorf("public ip is not ipv6")
		}
	}
	return ip, nil
}

func (s *Service) StartPublicIPChecker(ctx context.Context, scanInterval time.Duration, logger *log.Logger) {
	if scanInterval <= 0 {
		scanInterval = time.Second
	}
	if logger == nil {
		logger = log.Default()
	}

	s.RunPublicIPCheckIfDue(ctx, logger)

	ticker := time.NewTicker(scanInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.RunPublicIPCheckIfDue(ctx, logger)
		}
	}
}

func (s *Service) RunPublicIPCheckIfDue(ctx context.Context, logger *log.Logger) {
	if logger == nil {
		logger = log.Default()
	}

	cfg, err := s.loadPublicIPCheckConfig(ctx)
	if err != nil {
		logger.Printf("load public ip check settings failed: %v", err)
		return
	}
	if !cfg.Enabled {
		return
	}

	interval := cfg.IntervalSec
	if interval <= 0 {
		interval = 300
	}
	now := time.Now().UTC()
	if cfg.LastCheckedAt != nil && now.Before(cfg.LastCheckedAt.Add(time.Duration(interval)*time.Second)) {
		return
	}

	if !s.beginIPCheckRun() {
		return
	}
	defer s.endIPCheckRun()

	_ = s.runPublicIPCheck(ctx, cfg, logger)
}

func (s *Service) RunPublicIPCheckNow(ctx context.Context, logger *log.Logger) error {
	if logger == nil {
		logger = log.Default()
	}

	cfg, err := s.loadPublicIPCheckConfig(ctx)
	if err != nil {
		return fmt.Errorf("load public ip check settings: %w", err)
	}
	if !cfg.Enabled {
		return ErrPublicIPCheckDisabled
	}
	if !s.beginIPCheckRun() {
		return ErrPublicIPCheckRunning
	}
	defer s.endIPCheckRun()

	return s.runPublicIPCheck(ctx, cfg, logger)
}

func (s *Service) runPublicIPCheck(ctx context.Context, cfg publicIPCheckConfig, logger *log.Logger) error {
	start := time.Now()
	ip, source, runErr := s.resolvePublicIP(ctx, cfg.IPSources)
	now := time.Now().UTC()

	cfg.LastCheckedAt = &now
	if runErr == nil {
		cfg.PublicIP = stringPtr(ip)
	}

	if err := s.upsertPublicIPCheckConfig(ctx, cfg, now); err != nil {
		if runErr == nil {
			runErr = err
		}
		logger.Printf("update public ip check state failed: %v", err)
	}

	s.writePublicIPCheckAudit(ctx, runErr == nil, nullableStringPtrValue(cfg.PublicIP), source, runErr, time.Since(start))
	if runErr != nil {
		logger.Printf("public ip check failed: %v", runErr)
	}
	return runErr
}

func (s *Service) resolvePublicIP(ctx context.Context, sources []string) (string, string, error) {
	if len(sources) == 0 {
		return "", "", fmt.Errorf("no ip source configured")
	}

	var lastErr error
	for _, source := range sources {
		ip, err := s.fetchIP(ctx, source)
		if err != nil {
			lastErr = err
			continue
		}
		parsed := net.ParseIP(ip)
		if parsed == nil {
			lastErr = fmt.Errorf("invalid ip from source %s", source)
			continue
		}
		return ip, source, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("unable to fetch public ip")
	}
	return "", "", lastErr
}

func (s *Service) fetchIP(ctx context.Context, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("source returned status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 256))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(body)), nil
}

func (s *Service) writePublicIPCheckAudit(ctx context.Context, success bool, publicIP, source string, runErr error, duration time.Duration) {
	if s.logs == nil {
		return
	}
	detail := map[string]any{
		"result":     "success",
		"public_ip":  publicIP,
		"source":     source,
		"latency_ms": duration.Milliseconds(),
	}
	if !success {
		detail["result"] = "failed"
		if runErr != nil {
			detail["error"] = runErr.Error()
		}
	}
	payload, _ := json.Marshal(detail)
	_ = s.logs.CreatePublicIPCheckLog(ctx, logs.PublicIPCheckLogInput{
		Result:     detail["result"].(string),
		ErrorMsg:   nullableError(runErr),
		LatencyMS:  int(duration.Milliseconds()),
		DetailJSON: string(payload),
	})
}

func (s *Service) beginIPCheckRun() bool {
	s.ipCheckMu.Lock()
	defer s.ipCheckMu.Unlock()
	if s.ipCheckRunning {
		return false
	}
	s.ipCheckRunning = true
	return true
}

func (s *Service) endIPCheckRun() {
	s.ipCheckMu.Lock()
	defer s.ipCheckMu.Unlock()
	s.ipCheckRunning = false
}

func (s *Service) upsert(ctx context.Context, key string, value any, now time.Time) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("marshal setting %s: %w", key, err)
	}
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO system_settings(key, value, updated_at)
		VALUES(?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at
	`, key, string(payload), now); err != nil {
		return fmt.Errorf("upsert setting %s: %w", key, err)
	}
	return nil
}

func (s *Service) loadPublicIPCheckConfig(ctx context.Context) (publicIPCheckConfig, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT enabled, interval_sec, ip_sources_json, public_ip, last_checked_at
		FROM public_ip_check_settings
		WHERE id = 1
	`)

	var (
		enabledRaw    int
		intervalSec   int
		sourcesRaw    string
		publicIPRaw   sql.NullString
		lastCheckTime sql.NullTime
	)
	if err := row.Scan(&enabledRaw, &intervalSec, &sourcesRaw, &publicIPRaw, &lastCheckTime); err != nil {
		if err == sql.ErrNoRows {
			return s.defaultPublicIPCheckCopy(), nil
		}
		return publicIPCheckConfig{}, fmt.Errorf("query public ip check settings: %w", err)
	}

	cfg := publicIPCheckConfig{
		Enabled:     enabledRaw != 0,
		IntervalSec: intervalSec,
	}

	var sources []string
	if err := json.Unmarshal([]byte(sourcesRaw), &sources); err != nil {
		cfg.IPSources = append([]string(nil), s.defaultPublicIPCheck.IPSources...)
	} else {
		cfg.IPSources = normalizeSources(sources)
	}
	if publicIPRaw.Valid {
		cfg.PublicIP = stringPtr(publicIPRaw.String)
	}
	if lastCheckTime.Valid {
		cfg.LastCheckedAt = timePtr(lastCheckTime.Time)
	}
	if cfg.IntervalSec <= 0 {
		cfg.IntervalSec = s.defaultPublicIPCheck.IntervalSec
	}
	if len(cfg.IPSources) == 0 {
		cfg.IPSources = append([]string(nil), s.defaultPublicIPCheck.IPSources...)
	}
	return cfg, nil
}

func (s *Service) upsertPublicIPCheckConfig(ctx context.Context, cfg publicIPCheckConfig, now time.Time) error {
	payload, err := json.Marshal(cfg.IPSources)
	if err != nil {
		return fmt.Errorf("marshal public ip sources: %w", err)
	}
	enabled := 0
	if cfg.Enabled {
		enabled = 1
	}
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO public_ip_check_settings(
			id, enabled, interval_sec, ip_sources_json, public_ip, last_checked_at, updated_at
		)
		VALUES(1, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			enabled = excluded.enabled,
			interval_sec = excluded.interval_sec,
			ip_sources_json = excluded.ip_sources_json,
			public_ip = excluded.public_ip,
			last_checked_at = excluded.last_checked_at,
			updated_at = excluded.updated_at
	`, enabled, cfg.IntervalSec, string(payload), nullableStringPtr(cfg.PublicIP), cfg.LastCheckedAt, now); err != nil {
		return fmt.Errorf("upsert public ip check settings: %w", err)
	}
	return nil
}

func (s *Service) deleteLegacyIPSourceSetting(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM system_settings WHERE key = 'ip_sources'`); err != nil {
		return fmt.Errorf("delete legacy ip_sources setting: %w", err)
	}
	return nil
}

func (s *Service) hasDDNSTasks(ctx context.Context) (bool, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM ddns_tasks`).Scan(&count); err != nil {
		return false, fmt.Errorf("count ddns tasks: %w", err)
	}
	return count > 0, nil
}

func (s *Service) defaultPublicIPCheckCopy() publicIPCheckConfig {
	return publicIPCheckConfig{
		Enabled:       s.defaultPublicIPCheck.Enabled,
		IntervalSec:   s.defaultPublicIPCheck.IntervalSec,
		IPSources:     append([]string(nil), s.defaultPublicIPCheck.IPSources...),
		PublicIP:      stringPtr(nullableStringPtrValue(s.defaultPublicIPCheck.PublicIP)),
		LastCheckedAt: timePtrValue(s.defaultPublicIPCheck.LastCheckedAt),
	}
}

func isPublicIPSettingKey(key string) bool {
	switch key {
	case "public_ip_check", "ip_check_interval_sec", "ip_sources", "public_ip", "public_ip_last_checked_at":
		return true
	default:
		return false
	}
}

func normalizeSources(sources []string) []string {
	out := make([]string, 0, len(sources))
	seen := make(map[string]struct{}, len(sources))
	for _, source := range sources {
		trimmed := strings.TrimSpace(source)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	return out
}

func nullableStringPtr(v *string) any {
	if v == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*v)
	if trimmed == "" {
		return nil
	}
	return trimmed
}

func nullableStringPtrValue(v *string) string {
	if v == nil {
		return ""
	}
	return strings.TrimSpace(*v)
}

func stringPtr(v string) *string {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil
	}
	return &v
}

func timePtr(v time.Time) *time.Time {
	out := v
	return &out
}

func timePtrValue(v *time.Time) *time.Time {
	if v == nil {
		return nil
	}
	out := *v
	return &out
}

func nullableError(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
