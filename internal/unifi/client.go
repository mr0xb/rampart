// Package unifi is a minimal client for the local UniFi Network API.
//
// It talks to either a UniFi OS gateway (UDM/UDR/UCG..., API proxied under
// /proxy/network) or a legacy software controller (typically port 8443,
// API at the root). The flavor is auto-detected unless forced via Config.Legacy.
package unifi

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"
)

type Config struct {
	Host     string // address of the gateway/controller, e.g. "192.168.1.1" or "https://unifi.local:8443"
	Site     string // site name, almost always "default"
	User     string
	Pass     string
	APIKey   string // local API key (Settings > Control Plane > Integrations); preferred over user/pass
	Insecure bool   // skip TLS verification (self-signed certs)
	Legacy   bool   // force legacy controller mode (no /proxy/network prefix)
	Timeout  time.Duration
}

type Client struct {
	cfg      Config
	base     string
	http     *http.Client
	csrf     string
	unifiOS  bool
	loggedIn bool
}

func New(cfg Config) (*Client, error) {
	if cfg.Host == "" {
		return nil, fmt.Errorf("no host configured (use --host, RAMPART_HOST, or a config file)")
	}
	if cfg.Site == "" {
		cfg.Site = "default"
	}
	host := cfg.Host
	if !strings.Contains(host, "://") {
		host = "https://" + host
	}
	u, err := url.Parse(host)
	if err != nil {
		return nil, fmt.Errorf("invalid host %q: %w", cfg.Host, err)
	}
	jar, _ := cookiejar.New(nil)
	return &Client{
		cfg:  cfg,
		base: strings.TrimRight(u.String(), "/"),
		http: &http.Client{
			Jar: jar,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: cfg.Insecure},
			},
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}, nil
}

// Login detects the controller flavor and authenticates. With an API key no
// session is needed; the key is sent on every request instead.
func (c *Client) Login(ctx context.Context) error {
	if c.loggedIn {
		return nil
	}
	if err := c.detect(ctx); err != nil {
		return err
	}
	if c.cfg.APIKey != "" {
		c.loggedIn = true
		return nil
	}
	if c.cfg.User == "" || c.cfg.Pass == "" {
		return fmt.Errorf("no credentials: set an API key (--api-key / RAMPART_API_KEY) or username and password (--user/--pass, RAMPART_USER/RAMPART_PASS)")
	}
	path := "/api/auth/login" // UniFi OS login lives at the root, not under /proxy/network
	if !c.unifiOS {
		path = "/api/login"
	}
	body, _ := json.Marshal(map[string]string{
		"username": c.cfg.User,
		"password": c.cfg.Pass,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("login request failed: %w", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("login failed: %s", apiErrorMessage(resp.StatusCode, data))
	}
	if t := resp.Header.Get("X-Csrf-Token"); t != "" {
		c.csrf = t
	}
	c.loggedIn = true
	return nil
}

func (c *Client) detect(ctx context.Context) error {
	if c.cfg.Legacy {
		c.unifiOS = false
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/", nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("cannot reach %s: %w", c.base, err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	// UniFi OS answers 200 at the root; a legacy controller redirects to /manage.
	c.unifiOS = resp.StatusCode == http.StatusOK
	if t := resp.Header.Get("X-Csrf-Token"); t != "" {
		c.csrf = t
	}
	return nil
}

func (c *Client) apiURL(path string) string {
	if c.unifiOS {
		return c.base + "/proxy/network" + path
	}
	return c.base + path
}

func (c *Client) sitePath(path string) string {
	return fmt.Sprintf("/api/s/%s%s", c.cfg.Site, path)
}

func (c *Client) v2Path(path string) string {
	return fmt.Sprintf("/v2/api/site/%s%s", c.cfg.Site, path)
}

func (c *Client) do(ctx context.Context, method, path string, body any) ([]byte, error) {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.apiURL(path), rdr)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.cfg.APIKey != "" {
		req.Header.Set("X-API-KEY", c.cfg.APIKey)
	}
	if c.csrf != "" {
		req.Header.Set("X-Csrf-Token", c.csrf)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if t := resp.Header.Get("X-Csrf-Token"); t != "" {
		c.csrf = t
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("%s %s: %s", method, path, apiErrorMessage(resp.StatusCode, data))
	}
	return data, nil
}

type envelope struct {
	Meta struct {
		RC  string `json:"rc"`
		Msg string `json:"msg"`
	} `json:"meta"`
	Data json.RawMessage `json:"data"`
}

// v1 calls a classic /api/s/<site>/... endpoint and unwraps the meta/data envelope.
func (c *Client) v1(ctx context.Context, method, path string, body any) (json.RawMessage, error) {
	raw, err := c.do(ctx, method, path, body)
	if err != nil {
		return nil, err
	}
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("unexpected response from %s: %w", path, err)
	}
	if env.Meta.RC != "" && env.Meta.RC != "ok" {
		return nil, fmt.Errorf("api error: %s", env.Meta.Msg)
	}
	return env.Data, nil
}

func apiErrorMessage(status int, body []byte) string {
	var env envelope
	if json.Unmarshal(body, &env) == nil && env.Meta.Msg != "" {
		return fmt.Sprintf("%s (HTTP %d)", env.Meta.Msg, status)
	}
	var v2 struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &v2) == nil && v2.Message != "" {
		return fmt.Sprintf("%s (HTTP %d)", v2.Message, status)
	}
	msg := strings.TrimSpace(string(body))
	if len(msg) > 200 {
		msg = msg[:200] + "..."
	}
	if msg == "" {
		return fmt.Sprintf("HTTP %d", status)
	}
	return fmt.Sprintf("HTTP %d: %s", status, msg)
}

// --- classic firewall rules (/rest/firewallrule) ---

func (c *Client) ListFirewallRules(ctx context.Context) (json.RawMessage, error) {
	return c.v1(ctx, http.MethodGet, c.sitePath("/rest/firewallrule"), nil)
}

// GetFirewallRule returns the rule as a raw map so that updates can round-trip
// fields this client doesn't know about.
func (c *Client) GetFirewallRule(ctx context.Context, id string) (map[string]any, error) {
	data, err := c.v1(ctx, http.MethodGet, c.sitePath("/rest/firewallrule/"+id), nil)
	if err != nil {
		return nil, err
	}
	var items []map[string]any
	if err := json.Unmarshal(data, &items); err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("firewall rule %s not found", id)
	}
	return items[0], nil
}

func (c *Client) CreateFirewallRule(ctx context.Context, rule map[string]any) (json.RawMessage, error) {
	return c.v1(ctx, http.MethodPost, c.sitePath("/rest/firewallrule"), rule)
}

func (c *Client) UpdateFirewallRule(ctx context.Context, id string, rule map[string]any) (json.RawMessage, error) {
	return c.v1(ctx, http.MethodPut, c.sitePath("/rest/firewallrule/"+id), rule)
}

func (c *Client) DeleteFirewallRule(ctx context.Context, id string) error {
	_, err := c.v1(ctx, http.MethodDelete, c.sitePath("/rest/firewallrule/"+id), nil)
	return err
}

// SetFirewallRuleEnabled fetches the full rule and flips only its enabled flag.
func (c *Client) SetFirewallRuleEnabled(ctx context.Context, id string, enabled bool) error {
	rule, err := c.GetFirewallRule(ctx, id)
	if err != nil {
		return err
	}
	rule["enabled"] = enabled
	_, err = c.UpdateFirewallRule(ctx, id, rule)
	return err
}

// --- port forwarding (/rest/portforward) ---

func (c *Client) ListPortForwards(ctx context.Context) (json.RawMessage, error) {
	return c.v1(ctx, http.MethodGet, c.sitePath("/rest/portforward"), nil)
}

// GetPortForward returns the rule as a raw map so updates can round-trip fields
// this client doesn't know about.
func (c *Client) GetPortForward(ctx context.Context, id string) (map[string]any, error) {
	data, err := c.v1(ctx, http.MethodGet, c.sitePath("/rest/portforward/"+id), nil)
	if err != nil {
		return nil, err
	}
	var items []map[string]any
	if err := json.Unmarshal(data, &items); err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("port forward %s not found", id)
	}
	return items[0], nil
}

func (c *Client) CreatePortForward(ctx context.Context, rule map[string]any) (json.RawMessage, error) {
	return c.v1(ctx, http.MethodPost, c.sitePath("/rest/portforward"), rule)
}

func (c *Client) UpdatePortForward(ctx context.Context, id string, rule map[string]any) (json.RawMessage, error) {
	return c.v1(ctx, http.MethodPut, c.sitePath("/rest/portforward/"+id), rule)
}

func (c *Client) DeletePortForward(ctx context.Context, id string) error {
	_, err := c.v1(ctx, http.MethodDelete, c.sitePath("/rest/portforward/"+id), nil)
	return err
}

// SetPortForwardEnabled fetches the full rule and flips only its enabled flag.
func (c *Client) SetPortForwardEnabled(ctx context.Context, id string, enabled bool) error {
	rule, err := c.GetPortForward(ctx, id)
	if err != nil {
		return err
	}
	rule["enabled"] = enabled
	_, err = c.UpdatePortForward(ctx, id, rule)
	return err
}

// --- zone-based firewall policies (UniFi Network 9+, v2 API) ---

func (c *Client) ListFirewallPolicies(ctx context.Context) (json.RawMessage, error) {
	return c.do(ctx, http.MethodGet, c.v2Path("/firewall-policies"), nil)
}

func (c *Client) GetFirewallPolicy(ctx context.Context, id string) (map[string]any, error) {
	raw, err := c.do(ctx, http.MethodGet, c.v2Path("/firewall-policies/"+id), nil)
	if err == nil {
		var m map[string]any
		if json.Unmarshal(raw, &m) == nil && m["_id"] != nil {
			return m, nil
		}
	}
	// Some firmware versions lack the single-policy GET; scan the list instead.
	raw, err = c.ListFirewallPolicies(ctx)
	if err != nil {
		return nil, err
	}
	var items []map[string]any
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, err
	}
	for _, m := range items {
		if m["_id"] == id {
			return m, nil
		}
	}
	return nil, fmt.Errorf("firewall policy %s not found", id)
}

func (c *Client) CreateFirewallPolicy(ctx context.Context, policy map[string]any) (json.RawMessage, error) {
	return c.do(ctx, http.MethodPost, c.v2Path("/firewall-policies"), policy)
}

func (c *Client) UpdateFirewallPolicy(ctx context.Context, id string, policy map[string]any) (json.RawMessage, error) {
	return c.do(ctx, http.MethodPut, c.v2Path("/firewall-policies/"+id), policy)
}

func (c *Client) DeleteFirewallPolicy(ctx context.Context, id string) error {
	_, err := c.do(ctx, http.MethodDelete, c.v2Path("/firewall-policies/"+id), nil)
	if err == nil {
		return nil
	}
	// Older builds only expose batch delete.
	_, batchErr := c.do(ctx, http.MethodPost, c.v2Path("/firewall-policies/batch-delete"), []string{id})
	if batchErr != nil {
		return err
	}
	return nil
}

func (c *Client) SetFirewallPolicyEnabled(ctx context.Context, id string, enabled bool) error {
	policy, err := c.GetFirewallPolicy(ctx, id)
	if err != nil {
		return err
	}
	policy["enabled"] = enabled
	_, err = c.UpdateFirewallPolicy(ctx, id, policy)
	return err
}

// ListFirewallZones returns zone-based firewall zones, trying the endpoint
// variants seen across firmware versions. Returns nil (no error) if none work,
// callers then fall back to showing raw zone IDs.
func (c *Client) ListFirewallZones(ctx context.Context) []FirewallZone {
	for _, p := range []string{"/firewall/zones", "/firewall-zones", "/firewall/zone"} {
		raw, err := c.do(ctx, http.MethodGet, c.v2Path(p), nil)
		if err != nil {
			continue
		}
		var zones []FirewallZone
		if json.Unmarshal(raw, &zones) == nil && len(zones) > 0 {
			return zones
		}
	}
	return nil
}

// --- firewall groups, devices, clients ---

func (c *Client) ListFirewallGroups(ctx context.Context) (json.RawMessage, error) {
	return c.v1(ctx, http.MethodGet, c.sitePath("/rest/firewallgroup"), nil)
}

func (c *Client) ListDevices(ctx context.Context) (json.RawMessage, error) {
	return c.v1(ctx, http.MethodGet, c.sitePath("/stat/device"), nil)
}

func (c *Client) ListClients(ctx context.Context) (json.RawMessage, error) {
	return c.v1(ctx, http.MethodGet, c.sitePath("/stat/sta"), nil)
}
