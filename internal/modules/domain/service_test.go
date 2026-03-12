package domain

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"litedns/internal/db"
	"litedns/internal/modules/vendor"
	"litedns/internal/provider"
)

type fakeAdapter struct {
	domains          []provider.DomainRemote
	recordsByDomain  map[string][]provider.RecordRemote
	listRecordsCalls int
}

func (a *fakeAdapter) ProviderName() string {
	return "aliyun"
}

func (a *fakeAdapter) VerifyCredential(context.Context, provider.Credential) error {
	return nil
}

func (a *fakeAdapter) ListDomains(context.Context, provider.Credential) ([]provider.DomainRemote, error) {
	return a.domains, nil
}

func (a *fakeAdapter) ListRecords(_ context.Context, _ provider.Credential, domain string) ([]provider.RecordRemote, error) {
	a.listRecordsCalls++
	return a.recordsByDomain[domain], nil
}

func (a *fakeAdapter) UpsertRecord(context.Context, provider.Credential, string, string, string, string, int, map[string]any) (string, error) {
	return "stub", nil
}

func (a *fakeAdapter) DeleteRecord(context.Context, provider.Credential, string, string) error {
	return nil
}

func setupDomainService(t *testing.T, adapter *fakeAdapter) (*Service, *sql.DB, int64) {
	t.Helper()

	conn, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() {
		_ = conn.Close()
	})
	if err := db.Migrate(context.Background(), conn); err != nil {
		t.Fatalf("migrate db: %v", err)
	}

	providers := provider.NewManager(adapter)
	vendorSvc := vendor.NewService(conn, []byte("0123456789abcdef0123456789abcdef"), providers)
	created, err := vendorSvc.Create(context.Background(), vendor.CreateInput{
		Name:      "test-vendor",
		Provider:  "aliyun",
		APIKey:    "key",
		APISecret: "secret",
	})
	if err != nil {
		t.Fatalf("create vendor: %v", err)
	}

	svc := NewService(conn, providers, vendorSvc, 600)
	return svc, conn, created.ID
}

func mustGetDomainID(t *testing.T, conn *sql.DB, vendorID int64, name string) int64 {
	t.Helper()

	var id int64
	if err := conn.QueryRow(`SELECT id FROM domains WHERE vendor_id = ? AND domain_name = ?`, vendorID, name).Scan(&id); err != nil {
		t.Fatalf("query domain id: %v", err)
	}
	return id
}

func TestSyncVendorDomains_DoesNotOverwriteRecordSyncTime(t *testing.T) {
	adapter := &fakeAdapter{
		domains: []provider.DomainRemote{
			{Name: "example.com"},
		},
		recordsByDomain: map[string][]provider.RecordRemote{},
	}
	svc, conn, vendorID := setupDomainService(t, adapter)

	if err := svc.SyncVendorDomains(context.Background(), vendorID); err != nil {
		t.Fatalf("sync vendor domains: %v", err)
	}

	var lastSynced sql.NullTime
	domainID := mustGetDomainID(t, conn, vendorID, "example.com")
	if err := conn.QueryRow(`SELECT last_synced_at FROM domains WHERE id = ?`, domainID).Scan(&lastSynced); err != nil {
		t.Fatalf("query last_synced_at: %v", err)
	}
	if lastSynced.Valid {
		t.Fatalf("expected last_synced_at to be nil after vendor domain sync, got: %#v", lastSynced.Time)
	}
}

func TestListRecords_TriggersSyncWhenLocalCacheEmpty(t *testing.T) {
	adapter := &fakeAdapter{
		domains: []provider.DomainRemote{
			{Name: "example.com"},
		},
		recordsByDomain: map[string][]provider.RecordRemote{
			"example.com": {
				{ID: "r1", Host: "@", Type: "A", Value: "1.2.3.4", TTL: 600},
			},
		},
	}
	svc, conn, vendorID := setupDomainService(t, adapter)

	if err := svc.SyncVendorDomains(context.Background(), vendorID); err != nil {
		t.Fatalf("sync vendor domains: %v", err)
	}
	domainID := mustGetDomainID(t, conn, vendorID, "example.com")

	if _, err := conn.Exec(`UPDATE domains SET last_synced_at = ?, updated_at = ? WHERE id = ?`, time.Now().UTC(), time.Now().UTC(), domainID); err != nil {
		t.Fatalf("seed stale domain sync timestamp: %v", err)
	}

	items, err := svc.ListRecords(context.Background(), domainID)
	if err != nil {
		t.Fatalf("list records: %v", err)
	}
	if adapter.listRecordsCalls != 1 {
		t.Fatalf("expected listRecordsCalls=1, got %d", adapter.listRecordsCalls)
	}
	if len(items) != 1 || items[0].Value != "1.2.3.4" {
		t.Fatalf("unexpected records: %#v", items)
	}
}
