package aliyun

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"litedns/internal/provider"
)

const (
	defaultEndpoint = "https://alidns.aliyuncs.com"
	apiVersion      = "2015-01-09"
)

type Adapter struct {
	httpClient *http.Client
}

type rpcError struct {
	Code      string `json:"Code"`
	Message   string `json:"Message"`
	RequestID string `json:"RequestId"`
}

type describeDomainsResponse struct {
	TotalCount int `json:"TotalCount"`
	PageNumber int `json:"PageNumber"`
	PageSize   int `json:"PageSize"`
	Domains    struct {
		Domain []struct {
			DomainID           string `json:"DomainId"`
			DomainName         string `json:"DomainName"`
			ExpirationDate     string `json:"ExpirationDate"`
			ExpireDate         string `json:"ExpireDate"`
			ExpirationDateLong int64  `json:"ExpirationDateLong"`
			ExpireDateLong     int64  `json:"ExpireDateLong"`
		} `json:"Domain"`
	} `json:"Domains"`
}

type describeDomainRecordsResponse struct {
	TotalCount    int `json:"TotalCount"`
	PageNumber    int `json:"PageNumber"`
	PageSize      int `json:"PageSize"`
	DomainRecords struct {
		Record []struct {
			RecordID string `json:"RecordId"`
			RR       string `json:"RR"`
			Type     string `json:"Type"`
			Value    string `json:"Value"`
			TTL      int    `json:"TTL"`
			Line     string `json:"Line"`
		} `json:"Record"`
	} `json:"DomainRecords"`
}

type addOrUpdateResponse struct {
	RecordID string `json:"RecordId"`
}

func New() *Adapter {
	return &Adapter{
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

func (a *Adapter) ProviderName() string {
	return "aliyun"
}

func (a *Adapter) VerifyCredential(ctx context.Context, credential provider.Credential) error {
	if strings.TrimSpace(credential.Key) == "" || strings.TrimSpace(credential.Secret) == "" {
		return provider.ErrCredentialInvalid
	}

	_, err := a.listDomainsPage(ctx, credential, 1, 20)
	if err != nil {
		if isCredentialError(err) {
			return provider.ErrCredentialInvalid
		}
		return fmt.Errorf("verify aliyun credential: %w", err)
	}
	return nil
}

func (a *Adapter) ListDomains(ctx context.Context, credential provider.Credential) ([]provider.DomainRemote, error) {
	if err := a.VerifyCredential(ctx, credential); err != nil {
		return nil, err
	}

	page := 1
	pageSize := 100
	out := make([]provider.DomainRemote, 0)
	for {
		resp, err := a.listDomainsPage(ctx, credential, page, pageSize)
		if err != nil {
			return nil, fmt.Errorf("list aliyun domains: %w", err)
		}
		for _, d := range resp.Domains.Domain {
			out = append(out, provider.DomainRemote{
				ID:        d.DomainID,
				Name:      d.DomainName,
				ExpiresAt: parseAliyunExpiration(d.ExpirationDate, d.ExpireDate, d.ExpirationDateLong, d.ExpireDateLong),
				RenewURL:  buildRenewURL(d.DomainName),
			})
		}
		if len(resp.Domains.Domain) == 0 {
			break
		}
		if resp.TotalCount > 0 && len(out) >= resp.TotalCount {
			break
		}
		if len(resp.Domains.Domain) < pageSize {
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

	page := 1
	pageSize := 500
	out := make([]provider.RecordRemote, 0)
	for {
		resp, err := a.listRecordsPage(ctx, credential, domain, page, pageSize, "", "")
		if err != nil {
			return nil, fmt.Errorf("list aliyun records: %w", err)
		}
		for _, r := range resp.DomainRecords.Record {
			out = append(out, provider.RecordRemote{
				ID:      r.RecordID,
				Host:    normalizeRR(r.RR),
				Type:    r.Type,
				Value:   r.Value,
				TTL:     r.TTL,
				Proxied: false,
				Line:    r.Line,
			})
		}
		if len(resp.DomainRecords.Record) == 0 {
			break
		}
		if resp.TotalCount > 0 && len(out) >= resp.TotalCount {
			break
		}
		if len(resp.DomainRecords.Record) < pageSize {
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

	rr := toAliyunRR(host, domain)
	line := "default"
	if v, ok := extra["line"].(string); ok && strings.TrimSpace(v) != "" {
		line = strings.TrimSpace(v)
	}

	existing, err := a.findRecord(ctx, credential, domain, rr, recordType)
	if err != nil {
		return "", err
	}
	if existing != nil {
		if line == "default" && strings.TrimSpace(existing.Line) != "" {
			line = existing.Line
		}
		resp := addOrUpdateResponse{}
		err := a.callRPC(ctx, credential, "UpdateDomainRecord", map[string]string{
			"RecordId": existing.RecordID,
			"RR":       rr,
			"Type":     recordType,
			"Value":    value,
			"TTL":      strconv.Itoa(ttl),
			"Line":     line,
		}, &resp)
		if err != nil {
			return "", fmt.Errorf("update aliyun record: %w", err)
		}
		return resp.RecordID, nil
	}

	resp := addOrUpdateResponse{}
	err = a.callRPC(ctx, credential, "AddDomainRecord", map[string]string{
		"DomainName": domain,
		"RR":         rr,
		"Type":       recordType,
		"Value":      value,
		"TTL":        strconv.Itoa(ttl),
		"Line":       line,
	}, &resp)
	if err != nil {
		return "", fmt.Errorf("add aliyun record: %w", err)
	}
	return resp.RecordID, nil
}

func (a *Adapter) DeleteRecord(ctx context.Context, credential provider.Credential, domain, recordID string) error {
	recordID = strings.TrimSpace(recordID)
	if recordID == "" {
		return fmt.Errorf("recordID is required")
	}
	if err := a.VerifyCredential(ctx, credential); err != nil {
		return err
	}

	err := a.callRPC(ctx, credential, "DeleteDomainRecord", map[string]string{
		"RecordId": recordID,
	}, nil)
	if err != nil {
		return fmt.Errorf("delete aliyun record: %w", err)
	}
	return nil
}

func (a *Adapter) listDomainsPage(ctx context.Context, credential provider.Credential, page, pageSize int) (describeDomainsResponse, error) {
	resp := describeDomainsResponse{}
	err := a.callRPC(ctx, credential, "DescribeDomains", map[string]string{
		"PageNumber": strconv.Itoa(page),
		"PageSize":   strconv.Itoa(pageSize),
	}, &resp)
	if err != nil {
		return describeDomainsResponse{}, err
	}
	return resp, nil
}

func (a *Adapter) listRecordsPage(ctx context.Context, credential provider.Credential, domain string, page, pageSize int, rr, recordType string) (describeDomainRecordsResponse, error) {
	params := map[string]string{
		"DomainName": domain,
		"PageNumber": strconv.Itoa(page),
		"PageSize":   strconv.Itoa(pageSize),
	}
	if rr != "" {
		params["RRKeyWord"] = rr
	}
	if recordType != "" {
		params["TypeKeyWord"] = recordType
	}
	if rr != "" || recordType != "" {
		params["SearchMode"] = "COMBINATION"
	}

	resp := describeDomainRecordsResponse{}
	err := a.callRPC(ctx, credential, "DescribeDomainRecords", params, &resp)
	if err != nil {
		return describeDomainRecordsResponse{}, err
	}
	return resp, nil
}

func (a *Adapter) findRecord(ctx context.Context, credential provider.Credential, domain, rr, recordType string) (*struct {
	RecordID string
	Line     string
}, error) {
	page := 1
	pageSize := 200
	for {
		resp, err := a.listRecordsPage(ctx, credential, domain, page, pageSize, rr, recordType)
		if err != nil {
			return nil, fmt.Errorf("find aliyun record: %w", err)
		}
		for _, r := range resp.DomainRecords.Record {
			if strings.EqualFold(normalizeRR(r.RR), normalizeRR(rr)) && strings.EqualFold(r.Type, recordType) {
				return &struct {
					RecordID string
					Line     string
				}{RecordID: r.RecordID, Line: r.Line}, nil
			}
		}
		if len(resp.DomainRecords.Record) == 0 || len(resp.DomainRecords.Record) < pageSize {
			break
		}
		page++
	}
	return nil, nil
}

func (a *Adapter) callRPC(ctx context.Context, credential provider.Credential, action string, params map[string]string, out any) error {
	merged := map[string]string{
		"Action":           action,
		"AccessKeyId":      strings.TrimSpace(credential.Key),
		"Format":           "JSON",
		"Version":          apiVersion,
		"SignatureMethod":  "HMAC-SHA1",
		"SignatureVersion": "1.0",
		"SignatureNonce":   randomNonce(),
		"Timestamp":        time.Now().UTC().Format("2006-01-02T15:04:05Z"),
	}
	for k, v := range params {
		if strings.TrimSpace(v) == "" {
			continue
		}
		merged[k] = v
	}

	signature := signQuery("GET", merged, credential.Secret)
	merged["Signature"] = signature

	endpoint := defaultEndpoint
	if v, ok := credential.Extra["endpoint"].(string); ok && strings.TrimSpace(v) != "" {
		endpoint = strings.TrimSpace(v)
	}

	query := buildCanonicalQuery(merged)
	requestURL := strings.TrimRight(endpoint, "/") + "/?" + query

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return err
	}
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return err
	}

	if resp.StatusCode >= http.StatusBadRequest {
		apiErr := parseRPCError(body)
		if apiErr != nil {
			return fmt.Errorf("aliyun api error [%s]: %s", apiErr.Code, apiErr.Message)
		}
		return fmt.Errorf("aliyun api http status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	if maybeErr := parseRPCError(body); maybeErr != nil && maybeErr.Code != "" {
		return fmt.Errorf("aliyun api error [%s]: %s", maybeErr.Code, maybeErr.Message)
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("decode aliyun api response: %w", err)
	}
	return nil
}

func parseRPCError(body []byte) *rpcError {
	if len(body) == 0 {
		return nil
	}
	var e rpcError
	if err := json.Unmarshal(body, &e); err != nil {
		return nil
	}
	if strings.TrimSpace(e.Code) == "" && strings.TrimSpace(e.Message) == "" {
		return nil
	}
	return &e
}

func isCredentialError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	indicators := []string{
		"InvalidAccessKeyId",
		"SignatureDoesNotMatch",
		"IncompleteSignature",
		"Forbidden.RAM",
		"Authentication",
		"InvalidSecurityToken",
	}
	for _, s := range indicators {
		if strings.Contains(msg, s) {
			return true
		}
	}
	return false
}

func signQuery(method string, params map[string]string, accessSecret string) string {
	canonical := buildCanonicalQuery(params)
	stringToSign := strings.ToUpper(method) + "&" + percentEncode("/") + "&" + percentEncode(canonical)

	h := hmac.New(sha1.New, []byte(accessSecret+"&"))
	_, _ = h.Write([]byte(stringToSign))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

func buildCanonicalQuery(params map[string]string) string {
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, percentEncode(k)+"="+percentEncode(params[k]))
	}
	return strings.Join(parts, "&")
}

func percentEncode(v string) string {
	encoded := url.QueryEscape(v)
	encoded = strings.ReplaceAll(encoded, "+", "%20")
	encoded = strings.ReplaceAll(encoded, "*", "%2A")
	encoded = strings.ReplaceAll(encoded, "%7E", "~")
	return encoded
}

func randomNonce() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 10)
	}
	return hex.EncodeToString(buf)
}

func parseAliyunExpiration(expirationDate, expireDate string, expirationDateLong, expireDateLong int64) *time.Time {
	if t, ok := parseAliyunTimestamp(expirationDateLong); ok {
		return &t
	}
	if t, ok := parseAliyunTimestamp(expireDateLong); ok {
		return &t
	}
	for _, raw := range []string{expirationDate, expireDate} {
		if t, ok := parseAliyunDate(raw); ok {
			return &t
		}
	}
	return nil
}

func parseAliyunTimestamp(raw int64) (time.Time, bool) {
	if raw <= 0 {
		return time.Time{}, false
	}
	if raw > 9999999999 {
		return time.UnixMilli(raw).UTC(), true
	}
	return time.Unix(raw, 0).UTC(), true
}

func parseAliyunDate(raw string) (time.Time, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return time.Time{}, false
	}
	formats := []string{
		time.RFC3339,
		"2006-01-02T15:04:05Z",
		"2006-01-02 15:04:05",
		"2006-01-02",
	}
	for _, layout := range formats {
		if t, err := time.Parse(layout, trimmed); err == nil {
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}

func buildRenewURL(domain string) string {
	const base = "https://dc.console.aliyun.com/next/index#/domain-list/all"
	trimmed := strings.TrimSpace(domain)
	if trimmed == "" {
		return base
	}
	return base + "?keyword=" + url.QueryEscape(trimmed)
}

func normalizeRR(v string) string {
	if strings.TrimSpace(v) == "" {
		return "@"
	}
	return strings.TrimSpace(v)
}

func toAliyunRR(host, domain string) string {
	h := strings.TrimSpace(host)
	d := strings.TrimSpace(domain)
	if h == "" || h == "@" || strings.EqualFold(h, d) {
		return "@"
	}
	suffix := "." + d
	if strings.HasSuffix(strings.ToLower(h), strings.ToLower(suffix)) {
		h = h[:len(h)-len(suffix)]
	}
	h = strings.TrimSuffix(h, ".")
	if h == "" {
		return "@"
	}
	return h
}
