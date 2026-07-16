// Package cloudflare implements the small slice of the Cloudflare v4 API
// this controller needs: remotely-managed tunnel configuration and DNS
// record management.
package cloudflare

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const baseURL = "https://api.cloudflare.com/client/v4"

type Client struct {
	http      *http.Client
	token     string
	accountID string
	tunnelID  string

	zonesMu sync.Mutex
	zones   []Zone
}

func NewClient(token, accountID, tunnelID string) *Client {
	return &Client{
		http:      &http.Client{Timeout: 30 * time.Second},
		token:     token,
		accountID: accountID,
		tunnelID:  tunnelID,
	}
}

// TunnelDomain is the CNAME target for hostnames routed through the tunnel.
func (c *Client) TunnelDomain() string {
	return c.tunnelID + ".cfargotunnel.com"
}

type apiEnvelope struct {
	Success bool            `json:"success"`
	Errors  []apiError      `json:"errors"`
	Result  json.RawMessage `json:"result"`
}

type apiError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var reqBody io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reqBody = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, baseURL+path, reqBody)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	var env apiEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		return fmt.Errorf("%s %s: HTTP %d: unparseable response: %w", method, path, resp.StatusCode, err)
	}
	if !env.Success {
		msgs := make([]string, 0, len(env.Errors))
		for _, e := range env.Errors {
			msgs = append(msgs, fmt.Sprintf("%d: %s", e.Code, e.Message))
		}
		return fmt.Errorf("%s %s: HTTP %d: %s", method, path, resp.StatusCode, strings.Join(msgs, "; "))
	}
	if out != nil {
		if err := json.Unmarshal(env.Result, out); err != nil {
			return fmt.Errorf("%s %s: decoding result: %w", method, path, err)
		}
	}
	return nil
}

// ---- Tunnel configuration ----

type OriginRequest struct {
	NoTLSVerify bool `json:"noTLSVerify,omitempty"`
}

type IngressRule struct {
	Hostname      string         `json:"hostname,omitempty"`
	Path          string         `json:"path,omitempty"`
	Service       string         `json:"service"`
	OriginRequest *OriginRequest `json:"originRequest,omitempty"`
}

type TunnelConfig struct {
	Ingress []IngressRule `json:"ingress"`
}

func (c *Client) tunnelConfigPath() string {
	return fmt.Sprintf("/accounts/%s/cfd_tunnel/%s/configurations", c.accountID, c.tunnelID)
}

func (c *Client) GetTunnelConfig(ctx context.Context) (*TunnelConfig, error) {
	var result struct {
		Config *TunnelConfig `json:"config"`
	}
	if err := c.do(ctx, http.MethodGet, c.tunnelConfigPath(), nil, &result); err != nil {
		return nil, err
	}
	if result.Config == nil {
		return &TunnelConfig{}, nil
	}
	return result.Config, nil
}

func (c *Client) PutTunnelConfig(ctx context.Context, cfg *TunnelConfig) error {
	body := map[string]any{"config": cfg}
	return c.do(ctx, http.MethodPut, c.tunnelConfigPath(), body, nil)
}

// ---- Zones ----

type Zone struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func (c *Client) listZones(ctx context.Context) ([]Zone, error) {
	var all []Zone
	for page := 1; ; page++ {
		var zones []Zone
		path := fmt.Sprintf("/zones?per_page=50&page=%d", page)
		if err := c.do(ctx, http.MethodGet, path, nil, &zones); err != nil {
			return nil, err
		}
		all = append(all, zones...)
		if len(zones) < 50 {
			return all, nil
		}
	}
}

// Zones returns all zones visible to the token, cached for the lifetime of
// the process (zone changes are rare in a homelab; restart to pick them up).
func (c *Client) Zones(ctx context.Context) ([]Zone, error) {
	c.zonesMu.Lock()
	defer c.zonesMu.Unlock()
	if c.zones == nil {
		zones, err := c.listZones(ctx)
		if err != nil {
			return nil, err
		}
		c.zones = zones
	}
	return c.zones, nil
}

// ZoneForHostname finds the zone whose name is the longest suffix of hostname.
func (c *Client) ZoneForHostname(ctx context.Context, hostname string) (*Zone, error) {
	zones, err := c.Zones(ctx)
	if err != nil {
		return nil, err
	}

	host := strings.TrimPrefix(hostname, "*.")
	var best *Zone
	for i := range zones {
		z := &zones[i]
		if host == z.Name || strings.HasSuffix(host, "."+z.Name) {
			if best == nil || len(z.Name) > len(best.Name) {
				best = z
			}
		}
	}
	if best == nil {
		return nil, fmt.Errorf("no Cloudflare zone found for hostname %q (token may lack zone access)", hostname)
	}
	return best, nil
}

// ---- DNS records ----

type DNSRecord struct {
	ID      string `json:"id,omitempty"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
	Proxied bool   `json:"proxied"`
	Comment string `json:"comment,omitempty"`
	TTL     int    `json:"ttl,omitempty"`
}

func (c *Client) ListDNSRecords(ctx context.Context, zoneID string, params map[string]string) ([]DNSRecord, error) {
	q := url.Values{"per_page": {"100"}}
	for k, v := range params {
		q.Set(k, v)
	}
	var records []DNSRecord
	path := fmt.Sprintf("/zones/%s/dns_records?%s", zoneID, q.Encode())
	if err := c.do(ctx, http.MethodGet, path, nil, &records); err != nil {
		return nil, err
	}
	return records, nil
}

func (c *Client) CreateDNSRecord(ctx context.Context, zoneID string, rec DNSRecord) error {
	path := fmt.Sprintf("/zones/%s/dns_records", zoneID)
	return c.do(ctx, http.MethodPost, path, rec, nil)
}

func (c *Client) UpdateDNSRecord(ctx context.Context, zoneID string, rec DNSRecord) error {
	path := fmt.Sprintf("/zones/%s/dns_records/%s", zoneID, rec.ID)
	return c.do(ctx, http.MethodPut, path, rec, nil)
}

func (c *Client) DeleteDNSRecord(ctx context.Context, zoneID, recordID string) error {
	path := fmt.Sprintf("/zones/%s/dns_records/%s", zoneID, recordID)
	return c.do(ctx, http.MethodDelete, path, nil, nil)
}
