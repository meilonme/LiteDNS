package domain

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"litedns/internal/modules/vendor"
	"litedns/internal/provider"
)

var ErrDomainNotFound = errors.New("domain not found")

type Domain struct {
	ID             int64      `json:"id"`
	VendorID       int64      `json:"vendor_id"`
	RemoteDomainID *string    `json:"remote_domain_id,omitempty"`
	DomainName     string     `json:"domain_name"`
	LastSyncedAt   *time.Time `json:"last_synced_at,omitempty"`
	ExpiresAt      *time.Time `json:"expires_at,omitempty"`
	RenewURL       *string    `json:"renew_url,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

type Record struct {
	ID             int64     `json:"id"`
	DomainID       int64     `json:"domain_id"`
	RemoteRecordID string    `json:"remote_record_id"`
	Host           string    `json:"host"`
	Type           string    `json:"type"`
	Value          string    `json:"value"`
	TTL            int       `json:"ttl"`
	Proxied        bool      `json:"proxied"`
	Line           *string   `json:"line,omitempty"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type SyncSummary struct {
	Added   int `json:"added"`
	Updated int `json:"updated"`
	Deleted int `json:"deleted"`
}

type Service struct {
	db        *sql.DB
	providers *provider.Manager
	vendors   *vendor.Service
	syncTTL   time.Duration
}

func NewService(db *sql.DB, providers *provider.Manager, vendors *vendor.Service, syncTTLSeconds int) *Service {
	if syncTTLSeconds <= 0 {
		syncTTLSeconds = 600
	}
	return &Service{
		db:        db,
		providers: providers,
		vendors:   vendors,
		syncTTL:   time.Duration(syncTTLSeconds) * time.Second,
	}
}

func (s *Service) ListDomains(ctx context.Context, vendorID *int64) ([]Domain, error) {
	if vendorID != nil {
		if err := s.SyncVendorDomains(ctx, *vendorID); err != nil {
			return nil, err
		}
	}

	query := `
		SELECT id, vendor_id, remote_domain_id, domain_name, last_synced_at, expires_at, renew_url, created_at, updated_at
		FROM domains
	`
	args := make([]any, 0)
	if vendorID != nil {
		query += ` WHERE vendor_id = ?`
		args = append(args, *vendorID)
	}
	query += ` ORDER BY id ASC`

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list domains: %w", err)
	}
	defer rows.Close()

	items := make([]Domain, 0)
	for rows.Next() {
		item, err := scanDomain(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate domains: %w", err)
	}
	return items, nil
}

func (s *Service) ListRecords(ctx context.Context, domainID int64) ([]Record, error) {
	if err := s.SyncRecordsIfExpired(ctx, domainID); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, domain_id, remote_record_id, host, type, value, ttl, proxied, line, updated_at
		FROM records
		WHERE domain_id = ?
		ORDER BY host ASC, type ASC
	`, domainID)
	if err != nil {
		return nil, fmt.Errorf("list records: %w", err)
	}
	defer rows.Close()

	items := make([]Record, 0)
	for rows.Next() {
		item, err := scanRecord(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate records: %w", err)
	}
	return items, nil
}

func (s *Service) SyncRecordsIfExpired(ctx context.Context, domainID int64) error {
	d, err := s.GetDomain(ctx, domainID)
	if err != nil {
		return err
	}
	var localRecordCount int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM records WHERE domain_id = ?`, domainID).Scan(&localRecordCount); err != nil {
		return fmt.Errorf("count local records: %w", err)
	}

	if localRecordCount == 0 || d.LastSyncedAt == nil || d.LastSyncedAt.Add(s.syncTTL).Before(time.Now().UTC()) {
		_, err := s.SyncDomainRecords(ctx, domainID)
		return err
	}
	return nil
}

func (s *Service) SyncVendorDomains(ctx context.Context, vendorID int64) error {
	resolved, err := s.vendors.ResolveCredential(ctx, vendorID)
	if err != nil {
		return err
	}
	adapter, err := s.providers.Get(resolved.Provider)
	if err != nil {
		return err
	}
	remoteDomains, err := adapter.ListDomains(ctx, resolved.Credential)
	if err != nil {
		return err
	}

	now := time.Now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin domain sync tx: %w", err)
	}

	for _, d := range remoteDomains {
		if strings.TrimSpace(d.Name) == "" {
			continue
		}
		renewURL := strings.TrimSpace(d.RenewURL)
		if renewURL == "" {
			renewURL = defaultRenewURL(resolved.Provider, d.Name)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO domains(vendor_id, remote_domain_id, domain_name, last_synced_at, expires_at, renew_url, created_at, updated_at)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(vendor_id, domain_name)
			DO UPDATE SET remote_domain_id = excluded.remote_domain_id,
				expires_at = excluded.expires_at,
				renew_url = excluded.renew_url,
				updated_at = excluded.updated_at
		`, vendorID, nullableString(d.ID), d.Name, nil, nullableTime(d.ExpiresAt), nullableString(renewURL), now, now); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("upsert domain: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit domain sync tx: %w", err)
	}
	return nil
}

func (s *Service) SyncDomainRecords(ctx context.Context, domainID int64) (SyncSummary, error) {
	d, err := s.GetDomain(ctx, domainID)
	if err != nil {
		return SyncSummary{}, err
	}
	resolved, err := s.vendors.ResolveCredential(ctx, d.VendorID)
	if err != nil {
		return SyncSummary{}, err
	}
	adapter, err := s.providers.Get(resolved.Provider)
	if err != nil {
		return SyncSummary{}, err
	}
	remoteRecords, err := adapter.ListRecords(ctx, resolved.Credential, d.DomainName)
	if err != nil {
		return SyncSummary{}, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SyncSummary{}, fmt.Errorf("begin record sync tx: %w", err)
	}

	existingByKey := map[string]int64{}
	existingByID := map[string]bool{}
	rows, err := tx.QueryContext(ctx, `SELECT id, host, type FROM records WHERE domain_id = ?`, domainID)
	if err != nil {
		_ = tx.Rollback()
		return SyncSummary{}, fmt.Errorf("query existing records: %w", err)
	}
	for rows.Next() {
		var (
			id   int64
			host string
			rt   string
		)
		if err := rows.Scan(&id, &host, &rt); err != nil {
			rows.Close()
			_ = tx.Rollback()
			return SyncSummary{}, fmt.Errorf("scan existing record: %w", err)
		}
		existingByKey[recordKey(host, rt)] = id
	}
	rows.Close()

	now := time.Now().UTC()
	summary := SyncSummary{}
	for _, r := range remoteRecords {
		key := recordKey(r.Host, r.Type)
		_, exists := existingByKey[key]
		if exists {
			summary.Updated++
		} else {
			summary.Added++
		}
		existingByID[key] = true

		if _, err := tx.ExecContext(ctx, `
			INSERT INTO records(domain_id, remote_record_id, host, type, value, ttl, proxied, line, updated_at)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(domain_id, host, type)
			DO UPDATE SET remote_record_id = excluded.remote_record_id,
				value = excluded.value,
				ttl = excluded.ttl,
				proxied = excluded.proxied,
				line = excluded.line,
				updated_at = excluded.updated_at
		`, domainID, r.ID, r.Host, r.Type, r.Value, r.TTL, boolToInt(r.Proxied), nullableString(r.Line), now); err != nil {
			_ = tx.Rollback()
			return SyncSummary{}, fmt.Errorf("upsert synced record: %w", err)
		}
	}

	for key := range existingByKey {
		if existingByID[key] {
			continue
		}
		split := strings.SplitN(key, "|", 2)
		if len(split) != 2 {
			continue
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM records WHERE domain_id = ? AND host = ? AND type = ?`, domainID, split[0], split[1]); err != nil {
			_ = tx.Rollback()
			return SyncSummary{}, fmt.Errorf("delete stale record: %w", err)
		}
		summary.Deleted++
	}

	if _, err := tx.ExecContext(ctx, `UPDATE domains SET last_synced_at = ?, updated_at = ? WHERE id = ?`, now, now, domainID); err != nil {
		_ = tx.Rollback()
		return SyncSummary{}, fmt.Errorf("update domain synced time: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return SyncSummary{}, fmt.Errorf("commit record sync tx: %w", err)
	}
	return summary, nil
}

func (s *Service) GetDomain(ctx context.Context, domainID int64) (Domain, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, vendor_id, remote_domain_id, domain_name, last_synced_at, expires_at, renew_url, created_at, updated_at
		FROM domains WHERE id = ?
	`, domainID)
	item, err := scanDomain(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Domain{}, ErrDomainNotFound
		}
		return Domain{}, err
	}
	return item, nil
}

func (s *Service) ResolveDomainCredential(ctx context.Context, domainID int64) (Domain, vendor.ResolvedCredential, error) {
	d, err := s.GetDomain(ctx, domainID)
	if err != nil {
		return Domain{}, vendor.ResolvedCredential{}, err
	}
	cred, err := s.vendors.ResolveCredential(ctx, d.VendorID)
	if err != nil {
		return Domain{}, vendor.ResolvedCredential{}, err
	}
	return d, cred, nil
}

func recordKey(host, recordType string) string {
	return host + "|" + recordType
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func nullableString(v string) any {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	return v
}

func nullableTime(v *time.Time) any {
	if v == nil {
		return nil
	}
	return v.UTC()
}

func defaultRenewURL(providerName, domainName string) string {
	trimmedDomain := strings.TrimSpace(domainName)
	switch strings.ToLower(strings.TrimSpace(providerName)) {
	case "aliyun":
		const base = "https://dc.console.aliyun.com/next/index#/domain-list/all"
		if trimmedDomain == "" {
			return base
		}
		return base + "?keyword=" + url.QueryEscape(trimmedDomain)
	case "cloudflare":
		return "https://dash.cloudflare.com/"
	default:
		return ""
	}
}

func scanDomain(scanner interface{ Scan(dest ...any) error }) (Domain, error) {
	var (
		domain       Domain
		remoteDomain sql.NullString
		lastSynced   sql.NullTime
		expiresAt    sql.NullTime
		renewURL     sql.NullString
	)
	if err := scanner.Scan(&domain.ID, &domain.VendorID, &remoteDomain, &domain.DomainName, &lastSynced, &expiresAt, &renewURL, &domain.CreatedAt, &domain.UpdatedAt); err != nil {
		return Domain{}, err
	}
	if remoteDomain.Valid {
		domain.RemoteDomainID = &remoteDomain.String
	}
	if lastSynced.Valid {
		t := lastSynced.Time
		domain.LastSyncedAt = &t
	}
	if expiresAt.Valid {
		t := expiresAt.Time
		domain.ExpiresAt = &t
	}
	if renewURL.Valid && strings.TrimSpace(renewURL.String) != "" {
		domain.RenewURL = &renewURL.String
	}
	return domain, nil
}

func scanRecord(scanner interface{ Scan(dest ...any) error }) (Record, error) {
	var (
		record  Record
		proxied int
		line    sql.NullString
	)
	if err := scanner.Scan(&record.ID, &record.DomainID, &record.RemoteRecordID, &record.Host, &record.Type, &record.Value, &record.TTL, &proxied, &line, &record.UpdatedAt); err != nil {
		return Record{}, err
	}
	record.Proxied = proxied == 1
	if line.Valid {
		record.Line = &line.String
	}
	return record, nil
}
