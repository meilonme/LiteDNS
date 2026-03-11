package record

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"litedns/internal/modules/domain"
	"litedns/internal/provider"
)

var ErrRecordNotFound = errors.New("record not found")

type CreateInput struct {
	Host    string
	Type    string
	Value   string
	TTL     int
	Proxied bool
	Line    string
}

type UpdateInput struct {
	Value   *string
	TTL     *int
	Proxied *bool
	Line    *string
}

type Service struct {
	db        *sql.DB
	domains   *domain.Service
	providers *provider.Manager
}

func NewService(db *sql.DB, domains *domain.Service, providers *provider.Manager) *Service {
	return &Service{db: db, domains: domains, providers: providers}
}

func (s *Service) Create(ctx context.Context, domainID int64, in CreateInput) (domain.Record, error) {
	if strings.TrimSpace(in.Host) == "" || strings.TrimSpace(in.Type) == "" || strings.TrimSpace(in.Value) == "" {
		return domain.Record{}, fmt.Errorf("host, type and value are required")
	}
	if in.TTL <= 0 {
		in.TTL = 600
	}

	d, cred, err := s.domains.ResolveDomainCredential(ctx, domainID)
	if err != nil {
		return domain.Record{}, err
	}
	adapter, err := s.providers.Get(cred.Provider)
	if err != nil {
		return domain.Record{}, err
	}
	recordID, err := adapter.UpsertRecord(ctx, cred.Credential, d.DomainName, in.Host, in.Type, in.Value, in.TTL, map[string]any{
		"proxied": in.Proxied,
		"line":    in.Line,
	})
	if err != nil {
		return domain.Record{}, err
	}

	now := time.Now().UTC()
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO records(domain_id, remote_record_id, host, type, value, ttl, proxied, line, updated_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(domain_id, host, type)
		DO UPDATE SET remote_record_id = excluded.remote_record_id,
			value = excluded.value,
			ttl = excluded.ttl,
			proxied = excluded.proxied,
			line = excluded.line,
			updated_at = excluded.updated_at
	`, domainID, recordID, in.Host, in.Type, in.Value, in.TTL, boolToInt(in.Proxied), emptyToNil(in.Line), now)
	if err != nil {
		return domain.Record{}, fmt.Errorf("upsert local record: %w", err)
	}

	return s.findByDomainHostType(ctx, domainID, in.Host, in.Type)
}

func (s *Service) Update(ctx context.Context, recordID int64, in UpdateInput) (domain.Record, error) {
	rec, d, providerName, credential, err := s.getRecordContext(ctx, recordID)
	if err != nil {
		return domain.Record{}, err
	}

	value := rec.Value
	if in.Value != nil {
		value = strings.TrimSpace(*in.Value)
	}
	if value == "" {
		return domain.Record{}, fmt.Errorf("value cannot be empty")
	}
	TTL := rec.TTL
	if in.TTL != nil {
		TTL = *in.TTL
	}
	if TTL <= 0 {
		TTL = 600
	}
	proxied := rec.Proxied
	if in.Proxied != nil {
		proxied = *in.Proxied
	}
	line := ""
	if rec.Line != nil {
		line = *rec.Line
	}
	if in.Line != nil {
		line = strings.TrimSpace(*in.Line)
	}

	adapter, err := s.providers.Get(providerName)
	if err != nil {
		return domain.Record{}, err
	}
	remoteID, err := adapter.UpsertRecord(ctx, credential, d.DomainName, rec.Host, rec.Type, value, TTL, map[string]any{
		"proxied": proxied,
		"line":    line,
	})
	if err != nil {
		return domain.Record{}, err
	}

	now := time.Now().UTC()
	if _, err := s.db.ExecContext(ctx, `
		UPDATE records
		SET remote_record_id = ?, value = ?, ttl = ?, proxied = ?, line = ?, updated_at = ?
		WHERE id = ?
	`, remoteID, value, TTL, boolToInt(proxied), emptyToNil(line), now, recordID); err != nil {
		return domain.Record{}, fmt.Errorf("update local record: %w", err)
	}

	return s.findByID(ctx, recordID)
}

func (s *Service) Delete(ctx context.Context, recordID int64) error {
	rec, d, providerName, credential, err := s.getRecordContext(ctx, recordID)
	if err != nil {
		return err
	}

	adapter, err := s.providers.Get(providerName)
	if err != nil {
		return err
	}
	if err := adapter.DeleteRecord(ctx, credential, d.DomainName, rec.RemoteRecordID); err != nil {
		return err
	}

	res, err := s.db.ExecContext(ctx, `DELETE FROM records WHERE id = ?`, recordID)
	if err != nil {
		return fmt.Errorf("delete local record: %w", err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return ErrRecordNotFound
	}
	return nil
}

func (s *Service) getRecordContext(ctx context.Context, recordID int64) (domain.Record, domain.Domain, string, provider.Credential, error) {
	rec, err := s.findByID(ctx, recordID)
	if err != nil {
		return domain.Record{}, domain.Domain{}, "", provider.Credential{}, err
	}
	d, cred, err := s.domains.ResolveDomainCredential(ctx, rec.DomainID)
	if err != nil {
		return domain.Record{}, domain.Domain{}, "", provider.Credential{}, err
	}
	return rec, d, cred.Provider, cred.Credential, nil
}

func (s *Service) findByDomainHostType(ctx context.Context, domainID int64, host, recordType string) (domain.Record, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, domain_id, remote_record_id, host, type, value, ttl, proxied, line, updated_at
		FROM records WHERE domain_id = ? AND host = ? AND type = ?
	`, domainID, host, recordType)
	return scanRecord(row)
}

func (s *Service) findByID(ctx context.Context, recordID int64) (domain.Record, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, domain_id, remote_record_id, host, type, value, ttl, proxied, line, updated_at
		FROM records WHERE id = ?
	`, recordID)
	rec, err := scanRecord(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Record{}, ErrRecordNotFound
		}
		return domain.Record{}, err
	}
	return rec, nil
}

func scanRecord(scanner interface{ Scan(dest ...any) error }) (domain.Record, error) {
	var (
		rec     domain.Record
		proxied int
		line    sql.NullString
	)
	if err := scanner.Scan(&rec.ID, &rec.DomainID, &rec.RemoteRecordID, &rec.Host, &rec.Type, &rec.Value, &rec.TTL, &proxied, &line, &rec.UpdatedAt); err != nil {
		return domain.Record{}, err
	}
	rec.Proxied = proxied == 1
	if line.Valid {
		rec.Line = &line.String
	}
	return rec, nil
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func emptyToNil(v string) any {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	return v
}
