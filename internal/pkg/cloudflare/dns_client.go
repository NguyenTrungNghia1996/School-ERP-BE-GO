package cloudflare

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var (
	ErrNotConfigured  = errors.New("cloudflare credentials (CLOUDFLARE_API_TOKEN / CLOUDFLARE_ZONE_ID) are not configured")
	ErrRecordNotFound = errors.New("cloudflare dns record not found")
)

type DNSRecord struct {
	ID      string `json:"id,omitempty"`
	Type    string `json:"type"`    // "CNAME"
	Name    string `json:"name"`    // VD: "thpt-chu-van-an.school-erp.vn"
	Content string `json:"content"` // VD: "tenant-ingress.school-erp.vn"
	TTL     int    `json:"ttl"`     // 1 = Auto
	Proxied bool   `json:"proxied"` // true
	Comment string `json:"comment,omitempty"`
}

type cfAPIResponse[T any] struct {
	Success bool `json:"success"`
	Errors  []struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"errors"`
	Result T `json:"result"`
}

type Client struct {
	apiToken   string
	zoneID     string
	rootDomain string
	targetHost string
	proxied    bool
	ttl        int
	baseURL    string
	httpClient *http.Client
}

func NewClient(apiToken, zoneID, rootDomain, targetHost string, proxied bool, ttl int) *Client {
	if rootDomain == "" {
		rootDomain = "school-erp.vn"
	}
	if targetHost == "" {
		targetHost = "tenant-ingress.school-erp.vn"
	}
	if proxied || ttl <= 0 {
		ttl = 1
	}
	return &Client{
		apiToken:   strings.TrimSpace(apiToken),
		zoneID:     strings.TrimSpace(zoneID),
		rootDomain: strings.TrimSpace(rootDomain),
		targetHost: strings.TrimSpace(targetHost),
		proxied:    proxied,
		ttl:        ttl,
		baseURL:    "https://api.cloudflare.com/client/v4",
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

// SetBaseURL overrides the Cloudflare API v4 base URL (useful for testing).
func (c *Client) SetBaseURL(baseURL string) {
	c.baseURL = strings.TrimRight(baseURL, "/")
}

func (c *Client) IsConfigured() bool {
	return c != nil && c.apiToken != "" && c.zoneID != ""
}

func (c *Client) RootDomain() string {
	if c == nil || c.rootDomain == "" {
		return "school-erp.vn"
	}
	return c.rootDomain
}

func (c *Client) BuildFQDN(subdomain string) string {
	return fmt.Sprintf("%s.%s", strings.ToLower(strings.TrimSpace(subdomain)), c.RootDomain())
}

// CreateSubdomainCNAME tạo mới bản ghi CNAME trên Cloudflare (Idempotent: nếu đã tồn tại thì lấy ID)
func (c *Client) CreateSubdomainCNAME(ctx context.Context, subdomain, orgID string) (string, error) {
	if !c.IsConfigured() {
		return "", ErrNotConfigured
	}

	fqdn := c.BuildFQDN(subdomain)

	// 1. Kiểm tra xem bản ghi đã tồn tại trên Cloudflare chưa (tránh lỗi trùng khi retry)
	existing, err := c.FindRecordByFQDN(ctx, fqdn)
	if err == nil && existing != nil && existing.ID != "" {
		return existing.ID, nil
	}

	payload := DNSRecord{
		Type:    "CNAME",
		Name:    fqdn,
		Content: c.targetHost,
		TTL:     c.ttl,
		Proxied: c.proxied,
		Comment: fmt.Sprintf("SchoolERP Tenant OrgID: %s", orgID),
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	endpoint := fmt.Sprintf("%s/zones/%s/dns_records", c.baseURL, c.zoneID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	c.setHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var cfResp cfAPIResponse[DNSRecord]
	if err := json.NewDecoder(resp.Body).Decode(&cfResp); err != nil {
		return "", err
	}
	if !cfResp.Success {
		return "", fmt.Errorf("cloudflare create DNS failed: %+v", cfResp.Errors)
	}
	return cfResp.Result.ID, nil
}

// DeleteRecord xóa bản ghi DNS khỏi Cloudflare theo Record ID
func (c *Client) DeleteRecord(ctx context.Context, recordID string) error {
	if strings.TrimSpace(recordID) == "" {
		return nil
	}
	if !c.IsConfigured() {
		return ErrNotConfigured
	}

	endpoint := fmt.Sprintf("%s/zones/%s/dns_records/%s", c.baseURL, c.zoneID, recordID)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, endpoint, nil)
	if err != nil {
		return err
	}
	c.setHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// Nếu 404 (đã bị xóa trước đó trên Cloudflare) thì coi như thành công
	if resp.StatusCode == http.StatusNotFound {
		return nil
	}

	var cfResp cfAPIResponse[map[string]any]
	if err := json.NewDecoder(resp.Body).Decode(&cfResp); err != nil {
		return err
	}
	if !cfResp.Success {
		return fmt.Errorf("cloudflare delete DNS failed: %+v", cfResp.Errors)
	}
	return nil
}

func (c *Client) FindRecordByFQDN(ctx context.Context, fqdn string) (*DNSRecord, error) {
	if !c.IsConfigured() {
		return nil, ErrNotConfigured
	}

	q := url.Values{}
	q.Set("name", strings.ToLower(strings.TrimSpace(fqdn)))

	endpoint := fmt.Sprintf("%s/zones/%s/dns_records?%s", c.baseURL, c.zoneID, q.Encode())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	c.setHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var cfResp cfAPIResponse[[]DNSRecord]
	if err := json.NewDecoder(resp.Body).Decode(&cfResp); err != nil {
		return nil, err
	}
	if !cfResp.Success {
		return nil, fmt.Errorf("cloudflare query DNS failed: %+v", cfResp.Errors)
	}
	if len(cfResp.Result) > 0 {
		return &cfResp.Result[0], nil
	}
	return nil, ErrRecordNotFound
}

type cfListResultInfo struct {
	Page       int `json:"page"`
	PerPage    int `json:"per_page"`
	TotalPages int `json:"total_pages"`
}

type cfListAPIResponse struct {
	Success bool `json:"success"`
	Errors  []struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"errors"`
	Result     []DNSRecord      `json:"result"`
	ResultInfo cfListResultInfo `json:"result_info"`
}

// ListRecords fetches all DNS records in the configured Cloudflare zone.
func (c *Client) ListRecords(ctx context.Context) ([]DNSRecord, error) {
	if !c.IsConfigured() {
		return nil, ErrNotConfigured
	}

	var all []DNSRecord
	for page := 1; ; page++ {
		endpoint := fmt.Sprintf("%s/zones/%s/dns_records?per_page=100&page=%d", c.baseURL, c.zoneID, page)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return nil, err
		}
		c.setHeaders(req)

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return nil, err
		}

		var cfResp cfListAPIResponse
		decodeErr := json.NewDecoder(resp.Body).Decode(&cfResp)
		resp.Body.Close()
		if decodeErr != nil {
			return nil, decodeErr
		}
		if !cfResp.Success {
			return nil, fmt.Errorf("cloudflare list DNS failed: %+v", cfResp.Errors)
		}

		all = append(all, cfResp.Result...)
		if cfResp.ResultInfo.TotalPages <= page || len(cfResp.Result) == 0 {
			break
		}
	}
	return all, nil
}

func (c *Client) setHeaders(req *http.Request) {
	req.Header.Set("Authorization", "Bearer "+c.apiToken)
	req.Header.Set("Content-Type", "application/json")
}
