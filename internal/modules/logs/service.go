package logs

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

const (
	TypeDDNSTask      = "ddns_task"
	TypePublicIPCheck = "public_ip_check"
	TypeOperation     = "operation"

	ResultSuccess = "success"
	ResultFailed  = "failed"
)

type Item struct {
	ID         int64     `json:"id"`
	Type       string    `json:"type"`
	Result     string    `json:"result"`
	DDNSTaskID *int64    `json:"ddns_task_id,omitempty"`
	Actor      *string   `json:"actor,omitempty"`
	Action     *string   `json:"action,omitempty"`
	TargetType *string   `json:"target_type,omitempty"`
	TargetID   *string   `json:"target_id,omitempty"`
	OldIP      *string   `json:"old_ip,omitempty"`
	NewIP      *string   `json:"new_ip,omitempty"`
	ErrorMsg   *string   `json:"error_msg,omitempty"`
	LatencyMS  *int      `json:"latency_ms,omitempty"`
	DetailJSON *string   `json:"detail_json,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

type Filter struct {
	Type       string
	Result     string
	DDNSTaskID *int64
	Start      *time.Time
	End        *time.Time
}

type DDNSTaskLogInput struct {
	TaskID    int64
	OldIP     *string
	NewIP     *string
	Action    string
	Result    string
	ErrorMsg  string
	LatencyMS int
}

type PublicIPCheckLogInput struct {
	Result     string
	ErrorMsg   string
	LatencyMS  int
	DetailJSON string
}

type OperationLogInput struct {
	Actor      string
	Action     string
	TargetType string
	TargetID   string
	DetailJSON string
}

type Service struct {
	db *sql.DB
}

func NewService(db *sql.DB) *Service {
	return &Service{db: db}
}

func (s *Service) List(ctx context.Context, f Filter) ([]Item, error) {
	where := []string{"1=1"}
	args := make([]any, 0)

	typ := strings.TrimSpace(f.Type)
	if typ != "" {
		if !isValidType(typ) {
			return nil, fmt.Errorf("invalid log type")
		}
		where = append(where, "type = ?")
		args = append(args, typ)
	}
	if strings.TrimSpace(f.Result) != "" {
		where = append(where, "result = ?")
		args = append(args, strings.TrimSpace(f.Result))
	}
	if f.DDNSTaskID != nil {
		if typ != TypeDDNSTask {
			return nil, fmt.Errorf("ddns_task_id filter requires type=ddns_task")
		}
		where = append(where, "ddns_task_id = ?")
		args = append(args, *f.DDNSTaskID)
	}
	if f.Start != nil {
		where = append(where, "created_at >= ?")
		args = append(args, *f.Start)
	}
	if f.End != nil {
		where = append(where, "created_at <= ?")
		args = append(args, *f.End)
	}

	query := `
		SELECT id, type, result, ddns_task_id, actor, action, target_type, target_id,
			old_ip, new_ip, error_msg, latency_ms, detail_json, created_at
		FROM system_logs
		WHERE ` + strings.Join(where, " AND ") + `
		ORDER BY created_at DESC
		LIMIT 1000`

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list system logs: %w", err)
	}
	defer rows.Close()

	items := make([]Item, 0)
	for rows.Next() {
		item, err := scanItem(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate system logs: %w", err)
	}
	return items, nil
}

func (s *Service) CreateDDNSTaskLog(ctx context.Context, in DDNSTaskLogInput) error {
	if in.TaskID <= 0 {
		return fmt.Errorf("task id must be positive")
	}
	if strings.TrimSpace(in.Result) == "" {
		in.Result = ResultSuccess
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO system_logs(
			type, result, ddns_task_id, action, old_ip, new_ip, error_msg, latency_ms, created_at
		)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, TypeDDNSTask, in.Result, in.TaskID, nullableString(in.Action), nullableIP(in.OldIP), nullableIP(in.NewIP), nullableString(in.ErrorMsg), nullableInt(in.LatencyMS), time.Now().UTC())
	if err != nil {
		return fmt.Errorf("insert ddns task log: %w", err)
	}
	return nil
}

func (s *Service) CreatePublicIPCheckLog(ctx context.Context, in PublicIPCheckLogInput) error {
	if strings.TrimSpace(in.Result) == "" {
		in.Result = ResultSuccess
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO system_logs(type, result, error_msg, latency_ms, detail_json, created_at)
		VALUES(?, ?, ?, ?, ?, ?)
	`, TypePublicIPCheck, in.Result, nullableString(in.ErrorMsg), nullableInt(in.LatencyMS), nullableString(in.DetailJSON), time.Now().UTC())
	if err != nil {
		return fmt.Errorf("insert public ip check log: %w", err)
	}
	return nil
}

func (s *Service) CreateOperationLog(ctx context.Context, in OperationLogInput) error {
	if strings.TrimSpace(in.Actor) == "" {
		in.Actor = "system"
	}
	if strings.TrimSpace(in.Action) == "" {
		return fmt.Errorf("action is required")
	}
	if strings.TrimSpace(in.TargetType) == "" {
		return fmt.Errorf("target_type is required")
	}
	if strings.TrimSpace(in.TargetID) == "" {
		in.TargetID = "-"
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO system_logs(type, result, actor, action, target_type, target_id, detail_json, created_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?)
	`, TypeOperation, ResultSuccess, in.Actor, in.Action, in.TargetType, in.TargetID, nullableString(in.DetailJSON), time.Now().UTC())
	if err != nil {
		return fmt.Errorf("insert operation log: %w", err)
	}
	return nil
}

func (s *Service) Cleanup(ctx context.Context, retentionDays int) error {
	if retentionDays <= 0 {
		retentionDays = 90
	}
	threshold := time.Now().UTC().Add(-time.Duration(retentionDays) * 24 * time.Hour)
	if _, err := s.db.ExecContext(ctx, `DELETE FROM system_logs WHERE created_at < ?`, threshold); err != nil {
		return fmt.Errorf("cleanup system logs: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM ddns_logs WHERE created_at < ?`, threshold); err != nil {
		return fmt.Errorf("cleanup ddns logs: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM audit_logs WHERE created_at < ?`, threshold); err != nil {
		return fmt.Errorf("cleanup audit logs: %w", err)
	}
	return nil
}

func scanItem(scanner interface{ Scan(dest ...any) error }) (Item, error) {
	var (
		item       Item
		taskID     sql.NullInt64
		actor      sql.NullString
		action     sql.NullString
		targetType sql.NullString
		targetID   sql.NullString
		oldIP      sql.NullString
		newIP      sql.NullString
		errorMsg   sql.NullString
		latencyMS  sql.NullInt64
		detailJSON sql.NullString
	)
	if err := scanner.Scan(
		&item.ID,
		&item.Type,
		&item.Result,
		&taskID,
		&actor,
		&action,
		&targetType,
		&targetID,
		&oldIP,
		&newIP,
		&errorMsg,
		&latencyMS,
		&detailJSON,
		&item.CreatedAt,
	); err != nil {
		return Item{}, fmt.Errorf("scan system log: %w", err)
	}
	if taskID.Valid {
		v := taskID.Int64
		item.DDNSTaskID = &v
	}
	if actor.Valid {
		v := actor.String
		item.Actor = &v
	}
	if action.Valid {
		v := action.String
		item.Action = &v
	}
	if targetType.Valid {
		v := targetType.String
		item.TargetType = &v
	}
	if targetID.Valid {
		v := targetID.String
		item.TargetID = &v
	}
	if oldIP.Valid {
		v := oldIP.String
		item.OldIP = &v
	}
	if newIP.Valid {
		v := newIP.String
		item.NewIP = &v
	}
	if errorMsg.Valid {
		v := errorMsg.String
		item.ErrorMsg = &v
	}
	if latencyMS.Valid {
		v := int(latencyMS.Int64)
		item.LatencyMS = &v
	}
	if detailJSON.Valid {
		v := detailJSON.String
		item.DetailJSON = &v
	}
	return item, nil
}

func isValidType(v string) bool {
	switch strings.TrimSpace(v) {
	case TypeDDNSTask, TypePublicIPCheck, TypeOperation:
		return true
	default:
		return false
	}
}

func nullableString(v string) any {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	return strings.TrimSpace(v)
}

func nullableInt(v int) any {
	if v < 0 {
		return nil
	}
	return v
}

func nullableIP(v *string) any {
	if v == nil {
		return nil
	}
	if strings.TrimSpace(*v) == "" {
		return nil
	}
	return strings.TrimSpace(*v)
}
