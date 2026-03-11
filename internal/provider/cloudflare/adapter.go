package cloudflare

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"litedns/internal/provider"
)

const defaultBaseURL = "https://api.cloudflare.com/client/v4"

type Adapter struct {
	httpClient *http.Client
}

type apiError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type apiResultInfo struct {
	Page       int `json:"page"`
	PerPage    int `json:"per_page"`
	Count      int `json:"count"`
	TotalCount int `json:"total_count"`
	TotalPages int `json:"total_pages"`
}

type apiEnvelope struct {
	Success    bool            `json:"success"`
	Errors     []apiError      `json:"errors"`
	Messages   []any           `json:"messages"`
	Result     json.RawMessage `json:"result"`
	ResultInfo apiResultInfo   `json:"result_info"`
}

type zone struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type dnsRecord struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Type    string `json:"type"`
	Content string `json:"content"`
	TTL     int    `json:"ttl"`
	Proxied bool   `json:"proxied"`
}

func New() *Adapter {
	return &Adapter{httpClient: &http.Client{Timeout: 10 * time.Second}}
}

func (a *Adapter) ProviderName() string {
	return "cloudflare"
}

func (a *Adapter) VerifyCredential(ctx context.Context, credential provider.Credential) error {
	token := resolveToken(credential)
	if token == "" {
		return provider.ErrCredentialInvalid
	}

	_, _, err := a.listZonesPage(ctx, credential, 1, 1, "")
	if err != nil {
		if isCredentialError(err) {
			return provider.ErrCredentialInvalid
		}
		return fmt.Errorf("verify cloudflare credential: %w", err)
	}
	return nil
}

func (a *Adapter) ListDomains(ctx context.Context, credential provider.Credential) ([]provider.DomainRemote, error) {
	if err := a.VerifyCredential(ctx, credential); err != nil {
		return nil, err
	}

	out := make([]provider.DomainRemote, 0)
	page := 1
	perPage := 50
	for {
		zones, info, err := a.listZonesPage(ctx, credential, page, perPage, "")
		if err != nil {
			return nil, fmt.Errorf("list cloudflare zones: %w", err)
		}
		for _, z := range zones {
			out = append(out, provider.DomainRemote{ID: z.ID, Name: z.Name})
		}
		if info.TotalPages > 0 && page >= info.TotalPages {
			break
		}
		if len(zones) < perPage {
			break
		}
		page++
	}
	return out, nil
}

func (a *Adapter) ListRecords(ctx context.Context, credential provider.Credential, domain string) ([]provider.RecordRemote, error) {
	domain = strings.TrimSpace(domain)
	if domain == "" {
		return nil, fmt.Errorf("domain is required")
	}
	if err := a.VerifyCredential(ctx, credential); err != nil {
		return nil, err
	}

	zone, err := a.getZoneByName(ctx, credential, domain)
	if err != nil {
		return nil, err
	}

	page := 1
	perPage := 100
	out := make([]provider.RecordRemote, 0)
	for {
		records, info, err := a.listRecordsPage(ctx, credential, zone.ID, page, perPage, "", "")
		if err != nil {
			return nil, fmt.Errorf("list cloudflare records: %w", err)
		}
		for _, r := range records {
			out = append(out, provider.RecordRemote{
				ID:      r.ID,
				Host:    relativeHost(r.Name, domain),
				Type:    r.Type,
				Value:   r.Content,
				TTL:     r.TTL,
				Proxied: r.Proxied,
			})
		}
		if info.TotalPages > 0 && page >= info.TotalPages {
			break
		}
		if len(records) < perPage {
			break
		}
		page++
	}
	return out, nil
}

func (a *Adapter) UpsertRecord(ctx context.Context, credential provider.Credential, domain, host, recordType, value string, ttl int, extra map[string]any) (string, error) {
	domain = strings.TrimSpace(domain)
	host = strings.TrimSpace(host)
	recordType = strings.TrimSpace(recordType)
	value = strings.TrimSpace(value)
	if domain == "" || host == "" || recordType == "" || value == "" {
		return "", fmt.Errorf("domain, host, type and value are required")
	}
	if ttl <= 0 {
		ttl = 600
	}
	if err := a.VerifyCredential(ctx, credential); err != nil {
		return "", err
	}

	zone, err := a.getZoneByName(ctx, credential, domain)
	if err != nil {
		return "", err
	}

	fqdn := fullName(host, domain)
	proxied := false
	if v, ok := extra["proxied"].(bool); ok {
		proxied = v
	}

	records, _, err := a.listRecordsPage(ctx, credential, zone.ID, 1, 100, fqdn, recordType)
	if err != nil {
		return "", err
	}
	var existing *dnsRecord
	for i := range records {
		r := records[i]
		if strings.EqualFold(r.Name, fqdn) && strings.EqualFold(r.Type, recordType) {
			existing = &r
			break
		}
	}

	payload := map[string]any{
		"type":    recordType,
		"name":    fqdn,
		"content": value,
		"ttl":     ttl,
		"proxied": proxied,
	}

	if existing != nil {
		updated := dnsRecord{}
		_, err := a.call(ctx, credential, http.MethodPatch, "/zones/"+zone.ID+"/dns_records/"+existing.ID, nil, payload, &updated)
		if err != nil {
			return "", fmt.Errorf("update cloudflare record: %w", err)
		}
		return updated.ID, nil
	}

	created := dnsRecord{}
	_, err = a.call(ctx, credential, http.MethodPost, "/zones/"+zone.ID+"/dns_records", nil, payload, &created)
	if err != nil {
		return "", fmt.Errorf("create cloudflare record: %w", err)
	}
	return created.ID, nil
}

func (a *Adapter) DeleteRecord(ctx context.Context, credential provider.Credential, domain, recordID string) error {
	domain = strings.TrimSpace(domain)
	recordID = strings.TrimSpace(recordID)
	if domain == "" || recordID == "" {
		return fmt.Errorf("domain and recordID are required")
	}
	if err := a.VerifyCredential(ctx, credential); err != nil {
		return err
	}

	zone, err := a.getZoneByName(ctx, credential, domain)
	if err != nil {
		return err
	}

	if _, err := a.call(ctx, credential, http.MethodDelete, "/zones/"+zone.ID+"/dns_records/"+recordID, nil, nil, nil); err != nil {
		return fmt.Errorf("delete cloudflare record: %w", err)
	}
	return nil
}

func (a *Adapter) getZoneByName(ctx context.Context, credential provider.Credential, domain string) (zone, error) {
	zones, _, err := a.listZonesPage(ctx, credential, 1, 50, domain)
	if err != nil {
		return zone{}, err
	}
	for _, z := range zones {
		if strings.EqualFold(z.Name, domain) {
			return z, nil
		}
	}
	return zone{}, fmt.Errorf("cloudflare zone not found for domain %s", domain)
}

func (a *Adapter) listZonesPage(ctx context.Context, credential provider.Credential, page, perPage int, name string) ([]zone, apiResultInfo, error) {
	q := url.Values{}
	q.Set("page", strconv.Itoa(page))
	q.Set("per_page", strconv.Itoa(perPage))
	if strings.TrimSpace(name) != "" {
		q.Set("name", strings.TrimSpace(name))
	}

	var zones []zone
	info, err := a.call(ctx, credential, http.MethodGet, "/zones", q, nil, &zones)
	if err != nil {
		return nil, apiResultInfo{}, err
	}
	return zones, info, nil
}

func (a *Adapter) listRecordsPage(ctx context.Context, credential provider.Credential, zoneID string, page, perPage int, fqdn, recordType string) ([]dnsRecord, apiResultInfo, error) {
	q := url.Values{}
	q.Set("page", strconv.Itoa(page))
	q.Set("per_page", strconv.Itoa(perPage))
	if strings.TrimSpace(fqdn) != "" {
		q.Set("name", strings.TrimSpace(fqdn))
	}
	if strings.TrimSpace(recordType) != "" {
		q.Set("type", strings.TrimSpace(recordType))
	}

	var records []dnsRecord
	info, err := a.call(ctx, credential, http.MethodGet, "/zones/"+zoneID+"/dns_records", q, nil, &records)
	if err != nil {
		return nil, apiResultInfo{}, err
	}
	return records, info, nil
}

func (a *Adapter) call(ctx context.Context, credential provider.Credential, method, path string, query url.Values, payload any, out any) (apiResultInfo, error) {
	baseURL := defaultBaseURL
	if v, ok := credential.Extra["base_url"].(string); ok && strings.TrimSpace(v) != "" {
		baseURL = strings.TrimRight(strings.TrimSpace(v), "/")
	}

	fullURL := strings.TrimRight(baseURL, "/") + path
	if len(query) > 0 {
		fullURL += "?" + query.Encode()
	}

	var body io.Reader
	if payload != nil {
		buf, err := json.Marshal(payload)
		if err != nil {
			return apiResultInfo{}, fmt.Errorf("marshal cloudflare payload: %w", err)
		}
		body = bytes.NewReader(buf)
	}

	req, err := http.NewRequestWithContext(ctx, method, fullURL, body)
	if err != nil {
		return apiResultInfo{}, err
	}
	req.Header.Set("Authorization", "Bearer "+resolveToken(credential))
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return apiResultInfo{}, err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return apiResultInfo{}, err
	}

	env := apiEnvelope{}
	if err := json.Unmarshal(raw, &env); err != nil {
		if resp.StatusCode >= http.StatusBadRequest {
			return apiResultInfo{}, fmt.Errorf("cloudflare api http status %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
		}
		return apiResultInfo{}, fmt.Errorf("decode cloudflare response: %w", err)
	}

	if resp.StatusCode >= http.StatusBadRequest || !env.Success {
		return env.ResultInfo, buildAPIError(resp.StatusCode, env.Errors)
	}

	if out != nil && len(env.Result) > 0 {
		if err := json.Unmarshal(env.Result, out); err != nil {
			return env.ResultInfo, fmt.Errorf("decode cloudflare result: %w", err)
		}
	}

	return env.ResultInfo, nil
}

func buildAPIError(statusCode int, errs []apiError) error {
	if len(errs) == 0 {
		return fmt.Errorf("cloudflare api status %d", statusCode)
	}
	parts := make([]string, 0, len(errs))
	for _, e := range errs {
		parts = append(parts, fmt.Sprintf("%d:%s", e.Code, e.Message))
	}
	return fmt.Errorf("cloudflare api status %d: %s", statusCode, strings.Join(parts, "; "))
}

func resolveToken(credential provider.Credential) string {
	if s := strings.TrimSpace(credential.Secret); s != "" {
		return s
	}
	return strings.TrimSpace(credential.Key)
}

func isCredentialError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	if strings.Contains(msg, "status 401") || strings.Contains(msg, "status 403") {
		return true
	}
	indicators := []string{
		"10000",
		"9109",
		"invalid api token",
		"authentication",
		"unauthorized",
	}
	lower := strings.ToLower(msg)
	for _, s := range indicators {
		if strings.Contains(lower, strings.ToLower(s)) {
			return true
		}
	}
	return false
}

func fullName(host, domain string) string {
	h := strings.TrimSpace(host)
	d := strings.TrimSpace(domain)
	if h == "" || h == "@" || strings.EqualFold(h, d) {
		return d
	}
	suffix := "." + d
	if strings.HasSuffix(strings.ToLower(h), strings.ToLower(suffix)) {
		return strings.TrimSuffix(h, ".")
	}
	return h + "." + d
}

func relativeHost(name, domain string) string {
	n := strings.TrimSuffix(strings.TrimSpace(name), ".")
	d := strings.TrimSuffix(strings.TrimSpace(domain), ".")
	if strings.EqualFold(n, d) {
		return "@"
	}
	suffix := "." + d
	if strings.HasSuffix(strings.ToLower(n), strings.ToLower(suffix)) {
		h := n[:len(n)-len(suffix)]
		if h == "" {
			return "@"
		}
		return h
	}
	return n
}
