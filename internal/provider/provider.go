package provider

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

var (
	ErrUnsupportedProvider = errors.New("unsupported provider")
	ErrCredentialInvalid   = errors.New("credential invalid")
)

type Credential struct {
	Key    string
	Secret string
	Extra  map[string]any
}

type DomainRemote struct {
	ID        string
	Name      string
	ExpiresAt *time.Time
	RenewURL  string
}

type RecordRemote struct {
	ID      string
	Host    string
	Type    string
	Value   string
	TTL     int
	Proxied bool
	Line    string
}

type Adapter interface {
	ProviderName() string
	VerifyCredential(ctx context.Context, credential Credential) error
	ListDomains(ctx context.Context, credential Credential) ([]DomainRemote, error)
	ListRecords(ctx context.Context, credential Credential, domain string) ([]RecordRemote, error)
	UpsertRecord(ctx context.Context, credential Credential, domain, host, recordType, value string, ttl int, extra map[string]any) (string, error)
	DeleteRecord(ctx context.Context, credential Credential, domain, recordID string) error
}

type Manager struct {
	mu       sync.RWMutex
	adapters map[string]Adapter
}

func NewManager(adapters ...Adapter) *Manager {
	m := &Manager{adapters: make(map[string]Adapter)}
	for _, a := range adapters {
		m.Register(a)
	}
	return m
}

func (m *Manager) Register(adapter Adapter) {
	if adapter == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.adapters[adapter.ProviderName()] = adapter
}

func (m *Manager) Get(provider string) (Adapter, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	adapter, ok := m.adapters[provider]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnsupportedProvider, provider)
	}
	return adapter, nil
}

func (m *Manager) Providers() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]string, 0, len(m.adapters))
	for name := range m.adapters {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
