package vendor

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"litedns/internal/provider"
	"litedns/internal/security"
)

var (
	ErrNotFound = errors.New("vendor not found")
	ErrConflict = errors.New("vendor is referenced by domains or tasks")
)

type Vendor struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Provider  string    `json:"provider"`
	APIKey    string    `json:"api_key"`
	Extra     any       `json:"extra,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type CreateInput struct {
	Name      string
	Provider  string
	APIKey    string
	APISecret string
	ExtraJSON string
}

type UpdateInput struct {
	Name      *string
	Provider  *string
	APIKey    *string
	APISecret *string
	ExtraJSON *string
}

type ResolvedCredential struct {
	VendorID   int64
	Provider   string
	Credential provider.Credential
}

type Service struct {
	db        *sql.DB
	masterKey []byte
	providers *provider.Manager
}

func NewService(db *sql.DB, masterKey []byte, providers *provider.Manager) *Service {
	return &Service{db: db, masterKey: masterKey, providers: providers}
}

func (s *Service) List(ctx context.Context) ([]Vendor, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, provider, api_key, extra_json, created_at, updated_at
		FROM vendors
		ORDER BY id ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("list vendors: %w", err)
	}
	defer rows.Close()

	items := make([]Vendor, 0)
	for rows.Next() {
		item, err := scanVendor(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate vendors: %w", err)
	}
	return items, nil
}

func (s *Service) Create(ctx context.Context, in CreateInput) (Vendor, error) {
	if err := validateInput(in.Provider, in.APIKey, in.APISecret); err != nil {
		return Vendor{}, err
	}

	if err := s.verifyCredential(ctx, in.Provider, in.APIKey, in.APISecret); err != nil {
		return Vendor{}, err
	}
	cipher, err := security.EncryptSecret(s.masterKey, in.APISecret)
	if err != nil {
		return Vendor{}, fmt.Errorf("encrypt api secret: %w", err)
	}

	now := time.Now().UTC()
	result, err := s.db.ExecContext(ctx, `
		INSERT INTO vendors(name, provider, api_key, api_secret_cipher, extra_json, created_at, updated_at)
		VALUES(?, ?, ?, ?, ?, ?, ?)
	`, in.Name, in.Provider, in.APIKey, cipher, emptyToNil(in.ExtraJSON), now, now)
	if err != nil {
		return Vendor{}, fmt.Errorf("insert vendor: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return Vendor{}, fmt.Errorf("read vendor id: %w", err)
	}
	return s.Get(ctx, id)
}

func (s *Service) Update(ctx context.Context, id int64, in UpdateInput) (Vendor, error) {
	current, secretCipher, err := s.getRaw(ctx, id)
	if err != nil {
		return Vendor{}, err
	}

	providerName := current.Provider
	if in.Provider != nil {
		providerName = strings.TrimSpace(*in.Provider)
	}
	apiKey := current.APIKey
	if in.APIKey != nil {
		apiKey = strings.TrimSpace(*in.APIKey)
	}
	apiSecret := ""
	if in.APISecret != nil {
		apiSecret = strings.TrimSpace(*in.APISecret)
	} else {
		apiSecret, err = security.DecryptSecret(s.masterKey, secretCipher)
		if err != nil {
			return Vendor{}, fmt.Errorf("decrypt existing secret: %w", err)
		}
	}
	if err := validateInput(providerName, apiKey, apiSecret); err != nil {
		return Vendor{}, err
	}
	if err := s.verifyCredential(ctx, providerName, apiKey, apiSecret); err != nil {
		return Vendor{}, err
	}

	name := current.Name
	if in.Name != nil {
		name = strings.TrimSpace(*in.Name)
	}
	extra := current.Extra
	if in.ExtraJSON != nil {
		extra = parseExtra(*in.ExtraJSON)
	}

	cipher, err := security.EncryptSecret(s.masterKey, apiSecret)
	if err != nil {
		return Vendor{}, fmt.Errorf("encrypt api secret: %w", err)
	}

	now := time.Now().UTC()
	extraJSON, _ := json.Marshal(extra)
	if _, err := s.db.ExecContext(ctx, `
		UPDATE vendors
		SET name = ?, provider = ?, api_key = ?, api_secret_cipher = ?, extra_json = ?, updated_at = ?
		WHERE id = ?
	`, name, providerName, apiKey, cipher, string(extraJSON), now, id); err != nil {
		return Vendor{}, fmt.Errorf("update vendor: %w", err)
	}
	return s.Get(ctx, id)
}

func (s *Service) Delete(ctx context.Context, id int64) error {
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM domains WHERE vendor_id = ?`, id).Scan(&count); err != nil {
		return fmt.Errorf("check vendor domains: %w", err)
	}
	if count > 0 {
		return ErrConflict
	}

	res, err := s.db.ExecContext(ctx, `DELETE FROM vendors WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete vendor: %w", err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Service) Verify(ctx context.Context, id int64) error {
	resolved, err := s.ResolveCredential(ctx, id)
	if err != nil {
		return err
	}
	adapter, err := s.providers.Get(resolved.Provider)
	if err != nil {
		return err
	}
	return adapter.VerifyCredential(ctx, resolved.Credential)
}

func (s *Service) Get(ctx context.Context, id int64) (Vendor, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, name, provider, api_key, extra_json, created_at, updated_at
		FROM vendors
		WHERE id = ?
	`, id)
	item, err := scanVendor(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Vendor{}, ErrNotFound
		}
		return Vendor{}, err
	}
	return item, nil
}

func (s *Service) ResolveCredential(ctx context.Context, vendorID int64) (ResolvedCredential, error) {
	var (
		providerName string
		apiKey       string
		cipher       string
		extraJSON    sql.NullString
	)
	if err := s.db.QueryRowContext(ctx, `
		SELECT provider, api_key, api_secret_cipher, extra_json
		FROM vendors WHERE id = ?
	`, vendorID).Scan(&providerName, &apiKey, &cipher, &extraJSON); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ResolvedCredential{}, ErrNotFound
		}
		return ResolvedCredential{}, fmt.Errorf("query vendor credential: %w", err)
	}
	secret, err := security.DecryptSecret(s.masterKey, cipher)
	if err != nil {
		return ResolvedCredential{}, fmt.Errorf("decrypt vendor secret: %w", err)
	}

	cred := provider.Credential{Key: apiKey, Secret: secret}
	if extraJSON.Valid && strings.TrimSpace(extraJSON.String) != "" {
		m := map[string]any{}
		if err := json.Unmarshal([]byte(extraJSON.String), &m); err == nil {
			cred.Extra = m
		}
	}

	return ResolvedCredential{VendorID: vendorID, Provider: providerName, Credential: cred}, nil
}

func (s *Service) getRaw(ctx context.Context, id int64) (Vendor, string, error) {
	var (
		item         Vendor
		extraJSON    sql.NullString
		secretCipher string
	)
	if err := s.db.QueryRowContext(ctx, `
		SELECT id, name, provider, api_key, api_secret_cipher, extra_json, created_at, updated_at
		FROM vendors
		WHERE id = ?
	`, id).Scan(&item.ID, &item.Name, &item.Provider, &item.APIKey, &secretCipher, &extraJSON, &item.CreatedAt, &item.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Vendor{}, "", ErrNotFound
		}
		return Vendor{}, "", fmt.Errorf("query vendor: %w", err)
	}
	if extraJSON.Valid && strings.TrimSpace(extraJSON.String) != "" {
		item.Extra = parseExtra(extraJSON.String)
	}
	return item, secretCipher, nil
}

func (s *Service) verifyCredential(ctx context.Context, providerName, apiKey, apiSecret string) error {
	adapter, err := s.providers.Get(providerName)
	if err != nil {
		return err
	}
	return adapter.VerifyCredential(ctx, provider.Credential{Key: apiKey, Secret: apiSecret})
}

func scanVendor(scanner interface{ Scan(dest ...any) error }) (Vendor, error) {
	var (
		item      Vendor
		extraJSON sql.NullString
	)
	if err := scanner.Scan(&item.ID, &item.Name, &item.Provider, &item.APIKey, &extraJSON, &item.CreatedAt, &item.UpdatedAt); err != nil {
		return Vendor{}, err
	}
	if extraJSON.Valid && strings.TrimSpace(extraJSON.String) != "" {
		item.Extra = parseExtra(extraJSON.String)
	}
	return item, nil
}

func parseExtra(raw string) any {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(trimmed), &m); err != nil {
		return raw
	}
	return m
}

func emptyToNil(v string) any {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	return v
}

func validateInput(providerName, apiKey, apiSecret string) error {
	providerName = strings.TrimSpace(providerName)
	if providerName != "aliyun" && providerName != "cloudflare" {
		return fmt.Errorf("provider must be aliyun or cloudflare")
	}
	if strings.TrimSpace(apiKey) == "" {
		return fmt.Errorf("api_key is required")
	}
	if strings.TrimSpace(apiSecret) == "" {
		return fmt.Errorf("api_secret is required")
	}
	return nil
}
