package ddns

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"litedns/internal/modules/domain"
	"litedns/internal/modules/logs"
	"litedns/internal/modules/settings"
	"litedns/internal/provider"
)

var (
	ErrTaskNotFound = errors.New("ddns task not found")
)

type Task struct {
	ID                  int64      `json:"id"`
	DomainID            int64      `json:"domain_id"`
	Host                string     `json:"host"`
	RecordType          string     `json:"record_type"`
	IntervalSec         int        `json:"interval_sec"`
	Status              string     `json:"status"`
	LastIP              *string    `json:"last_ip,omitempty"`
	LastCheckAt         *time.Time `json:"last_check_at,omitempty"`
	LastSuccessAt       *time.Time `json:"last_success_at,omitempty"`
	ConsecutiveFailures int        `json:"consecutive_failures"`
	NextRunAt           time.Time  `json:"next_run_at"`
	LastError           *string    `json:"last_error,omitempty"`
	Version             int        `json:"version"`
	CreatedAt           time.Time  `json:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at"`
}

type ListFilter struct {
	Status   string
	DomainID *int64
}

type CreateInput struct {
	DomainID    int64
	Host        string
	RecordType  string
	IntervalSec int
}

type UpdateInput struct {
	IntervalSec *int
	Status      *string
}

type Service struct {
	db        *sql.DB
	providers *provider.Manager
	domains   *domain.Service
	settings  *settings.Service
	logs      *logs.Service

	running sync.Map
}

func NewService(db *sql.DB, providers *provider.Manager, domains *domain.Service, settings *settings.Service, logs *logs.Service) *Service {
	return &Service{
		db:        db,
		providers: providers,
		domains:   domains,
		settings:  settings,
		logs:      logs,
	}
}

func (s *Service) List(ctx context.Context, f ListFilter) ([]Task, error) {
	where := []string{"1=1"}
	args := make([]any, 0)
	if strings.TrimSpace(f.Status) != "" {
		where = append(where, "status = ?")
		args = append(args, f.Status)
	}
	if f.DomainID != nil {
		where = append(where, "domain_id = ?")
		args = append(args, *f.DomainID)
	}
	query := `
		SELECT id, domain_id, host, record_type, interval_sec, status, last_ip, last_check_at,
			last_success_at, consecutive_failures, next_run_at, last_error, version, created_at, updated_at
		FROM ddns_tasks
		WHERE ` + strings.Join(where, " AND ") + `
		ORDER BY id ASC`

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list ddns tasks: %w", err)
	}
	defer rows.Close()

	out := make([]Task, 0)
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate ddns tasks: %w", err)
	}
	return out, nil
}

func (s *Service) Create(ctx context.Context, in CreateInput) (Task, error) {
	if !s.settings.EffectivePublicIPCheck(ctx) {
		return Task{}, fmt.Errorf("public_ip_check must be enabled before creating ddns tasks")
	}
	if strings.TrimSpace(in.Host) == "" {
		return Task{}, fmt.Errorf("host is required")
	}
	if in.RecordType != "A" && in.RecordType != "AAAA" {
		return Task{}, fmt.Errorf("record_type must be A or AAAA")
	}
	if in.IntervalSec <= 0 {
		in.IntervalSec = s.settings.EffectiveDDNSInterval(ctx)
	}

	if _, err := s.domains.GetDomain(ctx, in.DomainID); err != nil {
		return Task{}, err
	}

	now := time.Now().UTC()
	result, err := s.db.ExecContext(ctx, `
		INSERT INTO ddns_tasks(domain_id, host, record_type, interval_sec, status, next_run_at, created_at, updated_at)
		VALUES(?, ?, ?, ?, 'running', ?, ?, ?)
	`, in.DomainID, in.Host, in.RecordType, in.IntervalSec, now, now, now)
	if err != nil {
		return Task{}, fmt.Errorf("insert ddns task: %w", err)
	}
	id, _ := result.LastInsertId()
	return s.Get(ctx, id)
}

func (s *Service) Update(ctx context.Context, taskID int64, in UpdateInput) (Task, error) {
	task, err := s.Get(ctx, taskID)
	if err != nil {
		return Task{}, err
	}
	interval := task.IntervalSec
	if in.IntervalSec != nil {
		if *in.IntervalSec <= 0 {
			return Task{}, fmt.Errorf("interval_sec must be positive")
		}
		interval = *in.IntervalSec
	}
	status := task.Status
	if in.Status != nil {
		if *in.Status != "running" && *in.Status != "paused" {
			return Task{}, fmt.Errorf("status must be running or paused")
		}
		status = *in.Status
	}

	now := time.Now().UTC()
	if _, err := s.db.ExecContext(ctx, `
		UPDATE ddns_tasks
		SET interval_sec = ?, status = ?, updated_at = ?
		WHERE id = ?
	`, interval, status, now, taskID); err != nil {
		return Task{}, fmt.Errorf("update ddns task: %w", err)
	}
	return s.Get(ctx, taskID)
}

func (s *Service) Pause(ctx context.Context, taskID int64) (Task, error) {
	status := "paused"
	return s.Update(ctx, taskID, UpdateInput{Status: &status})
}

func (s *Service) Resume(ctx context.Context, taskID int64) (Task, error) {
	now := time.Now().UTC()
	if _, err := s.db.ExecContext(ctx, `
		UPDATE ddns_tasks
		SET status = 'running', next_run_at = ?, updated_at = ?
		WHERE id = ?
	`, now, now, taskID); err != nil {
		return Task{}, fmt.Errorf("resume ddns task: %w", err)
	}
	return s.Get(ctx, taskID)
}

func (s *Service) RunOnce(ctx context.Context, taskID int64) error {
	return s.execute(ctx, taskID, false)
}

func (s *Service) Delete(ctx context.Context, taskID int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM ddns_tasks WHERE id = ?`, taskID)
	if err != nil {
		return fmt.Errorf("delete ddns task: %w", err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return ErrTaskNotFound
	}
	s.running.Delete(taskID)
	return nil
}

func (s *Service) DueTaskIDs(ctx context.Context, now time.Time, limit int) ([]int64, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id
		FROM ddns_tasks
		WHERE status = 'running' AND next_run_at <= ?
		ORDER BY next_run_at ASC
		LIMIT ?
	`, now.UTC(), limit)
	if err != nil {
		return nil, fmt.Errorf("query due tasks: %w", err)
	}
	defer rows.Close()

	ids := make([]int64, 0)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan due task id: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate due task ids: %w", err)
	}
	return ids, nil
}

func (s *Service) ExecuteScheduled(ctx context.Context, taskID int64) {
	_ = s.execute(ctx, taskID, true)
}

func (s *Service) execute(ctx context.Context, taskID int64, updateSchedule bool) error {
	if _, loaded := s.running.LoadOrStore(taskID, struct{}{}); loaded {
		return nil
	}
	defer s.running.Delete(taskID)

	task, err := s.Get(ctx, taskID)
	if err != nil {
		return err
	}
	if task.Status == "paused" && updateSchedule {
		return nil
	}

	d, cred, err := s.domains.ResolveDomainCredential(ctx, task.DomainID)
	if err != nil {
		return err
	}
	adapter, err := s.providers.Get(cred.Provider)
	if err != nil {
		return err
	}

	start := time.Now()
	action := "update"
	oldIP := task.LastIP
	var newIP *string

	publicIP, execErr := s.settings.PublicIPForRecordType(ctx, task.RecordType)
	if execErr == nil {
		var remoteRecord *provider.RecordRemote
		records, listErr := adapter.ListRecords(ctx, cred.Credential, d.DomainName)
		if listErr != nil {
			execErr = fmt.Errorf("list provider records: %w", listErr)
		} else {
			remoteRecord = findRemoteRecord(records, task.Host, task.RecordType)
			if remoteRecord == nil {
				action = "create"
			} else {
				remoteValue := strings.TrimSpace(remoteRecord.Value)
				if remoteValue != "" {
					oldIP = stringPtr(remoteValue)
				}
				if remoteValue == publicIP {
					action = "skip"
				}
			}
		}

		now := time.Now().UTC()
		if execErr == nil && action != "skip" {
			recordID, upsertErr := adapter.UpsertRecord(ctx, cred.Credential, d.DomainName, task.Host, task.RecordType, publicIP, 600, map[string]any{"proxied": false})
			if upsertErr != nil {
				execErr = upsertErr
			} else {
				if strings.TrimSpace(recordID) == "" {
					if remoteRecord != nil && strings.TrimSpace(remoteRecord.ID) != "" {
						recordID = remoteRecord.ID
					} else {
						recordID = fmt.Sprintf("ddns-%d", task.ID)
					}
				}
				proxied := false
				line := ""
				if remoteRecord != nil {
					proxied = remoteRecord.Proxied
					line = remoteRecord.Line
				}
				_ = s.upsertLocalRecord(ctx, task, recordID, publicIP, proxied, line, now)
			}
		}
		if execErr == nil && action == "skip" && remoteRecord != nil {
			recordID := strings.TrimSpace(remoteRecord.ID)
			if recordID == "" {
				recordID = fmt.Sprintf("ddns-%d", task.ID)
			}
			_ = s.upsertLocalRecord(ctx, task, recordID, publicIP, remoteRecord.Proxied, remoteRecord.Line, now)
		}
		if execErr == nil {
			newIP = stringPtr(publicIP)
		}
	}

	if oldIP == nil && task.LastIP != nil {
		oldIP = stringPtr(*task.LastIP)
	}

	now := time.Now().UTC()
	nextRun := task.NextRunAt
	if updateSchedule || nextRun.IsZero() {
		nextRun = now.Add(time.Duration(task.IntervalSec) * time.Second)
	}

	if execErr != nil {
		msg := execErr.Error()
		if _, err := s.db.ExecContext(ctx, `
			UPDATE ddns_tasks
			SET last_check_at = ?, consecutive_failures = consecutive_failures + 1,
				last_error = ?, next_run_at = ?, version = version + 1, updated_at = ?
			WHERE id = ?
		`, now, msg, nextRun, now, taskID); err != nil {
			return fmt.Errorf("update failed task: %w", err)
		}
		if err := s.insertLog(ctx, taskID, oldIP, nil, action, "failed", msg, time.Since(start)); err != nil {
			return err
		}
		return execErr
	}

	finalIP := ""
	if newIP != nil {
		finalIP = *newIP
	}
	if _, err := s.db.ExecContext(ctx, `
		UPDATE ddns_tasks
		SET last_ip = ?, last_check_at = ?, last_success_at = ?, consecutive_failures = 0,
			last_error = NULL, next_run_at = ?, version = version + 1, updated_at = ?
		WHERE id = ?
	`, nullableString(finalIP), now, now, nextRun, now, taskID); err != nil {
		return fmt.Errorf("update success task: %w", err)
	}

	if err := s.insertLog(ctx, taskID, oldIP, newIP, action, "success", "", time.Since(start)); err != nil {
		return err
	}
	return nil
}

func (s *Service) upsertLocalRecord(ctx context.Context, task Task, remoteRecordID, value string, proxied bool, line string, now time.Time) error {
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO records(domain_id, remote_record_id, host, type, value, ttl, proxied, line, updated_at)
		VALUES(?, ?, ?, ?, ?, 600, ?, ?, ?)
		ON CONFLICT(domain_id, host, type)
		DO UPDATE SET
			remote_record_id = excluded.remote_record_id,
			value = excluded.value,
			ttl = excluded.ttl,
			proxied = excluded.proxied,
			line = excluded.line,
			updated_at = excluded.updated_at
	`, task.DomainID, remoteRecordID, task.Host, task.RecordType, value, boolToInt(proxied), nullableString(line), now); err != nil {
		return fmt.Errorf("upsert local record cache: %w", err)
	}
	return nil
}

func (s *Service) insertLog(ctx context.Context, taskID int64, oldIP, newIP *string, action, result, errMsg string, duration time.Duration) error {
	if s.logs == nil {
		return nil
	}
	return s.logs.CreateDDNSTaskLog(ctx, logs.DDNSTaskLogInput{
		TaskID:    taskID,
		OldIP:     oldIP,
		NewIP:     newIP,
		Action:    action,
		Result:    result,
		ErrorMsg:  errMsg,
		LatencyMS: int(duration.Milliseconds()),
	})
}

func (s *Service) Get(ctx context.Context, taskID int64) (Task, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, domain_id, host, record_type, interval_sec, status, last_ip, last_check_at,
			last_success_at, consecutive_failures, next_run_at, last_error, version, created_at, updated_at
		FROM ddns_tasks
		WHERE id = ?
	`, taskID)
	t, err := scanTask(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Task{}, ErrTaskNotFound
		}
		return Task{}, err
	}
	return t, nil
}

func scanTask(scanner interface{ Scan(dest ...any) error }) (Task, error) {
	var (
		task        Task
		lastIP      sql.NullString
		lastCheck   sql.NullTime
		lastSuccess sql.NullTime
		lastError   sql.NullString
	)
	if err := scanner.Scan(
		&task.ID,
		&task.DomainID,
		&task.Host,
		&task.RecordType,
		&task.IntervalSec,
		&task.Status,
		&lastIP,
		&lastCheck,
		&lastSuccess,
		&task.ConsecutiveFailures,
		&task.NextRunAt,
		&lastError,
		&task.Version,
		&task.CreatedAt,
		&task.UpdatedAt,
	); err != nil {
		return Task{}, err
	}
	if lastIP.Valid {
		task.LastIP = &lastIP.String
	}
	if lastCheck.Valid {
		t := lastCheck.Time
		task.LastCheckAt = &t
	}
	if lastSuccess.Valid {
		t := lastSuccess.Time
		task.LastSuccessAt = &t
	}
	if lastError.Valid {
		task.LastError = &lastError.String
	}
	return task, nil
}

func findRemoteRecord(records []provider.RecordRemote, host, recordType string) *provider.RecordRemote {
	targetHost := normalizeHost(host)
	targetType := strings.ToUpper(strings.TrimSpace(recordType))
	for i := range records {
		item := &records[i]
		if normalizeHost(item.Host) == targetHost && strings.ToUpper(strings.TrimSpace(item.Type)) == targetType {
			return item
		}
	}
	return nil
}

func normalizeHost(v string) string {
	trimmed := strings.TrimSpace(v)
	if trimmed == "" {
		return "@"
	}
	return strings.ToLower(trimmed)
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func stringPtr(v string) *string {
	trimmed := strings.TrimSpace(v)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func nullableIP(v *string) any {
	if v == nil || strings.TrimSpace(*v) == "" {
		return nil
	}
	return *v
}

func nullableString(v string) any {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	return v
}
