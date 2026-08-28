// Package cmd implements the rampart CLI.
package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/mr0xb/rampart/internal/output"
	"github.com/mr0xb/rampart/internal/unifi"
)

var version = "0.1.0"

var flags struct {
	config   string
	host     string
	site     string
	user     string
	pass     string
	apiKey   string
	insecure bool
	legacy   bool
	timeout  time.Duration
	jsonOut  bool
}

var rootCmd = &cobra.Command{
	Use:   "rampart",
	Short: "rampart — a wall around your network. Manage a local UniFi gateway from the CLI.",
	Long: `rampart manages a local UniFi gateway or controller: firewall rules,
zone-based firewall policies, devices, clients and firewall groups.

Configuration (precedence: flags > environment > config file):

  flags:   --host, --site, --user, --pass, --api-key, --insecure, --legacy
  env:     RAMPART_HOST, RAMPART_SITE, RAMPART_USER, RAMPART_PASS,
           RAMPART_API_KEY, RAMPART_INSECURE, RAMPART_LEGACY
  file:    ~/.config/rampart/config.json
           {"host": "192.168.1.1", "api_key": "...", "site": "default"}

An API key (UniFi UI: Settings > Control Plane > Integrations) is the
recommended way to authenticate. Every list command supports --json for
script-friendly output and -i for an interactive TUI.`,
	Example: `  rampart fw list
  rampart fw list --ruleset WAN_IN --json | jq '.[].name'
  rampart fw add --name "Block telnet" --ruleset LAN_IN --action drop --protocol tcp --dst-port 23
  rampart fw disable 6633aabbcc00112233445566
  rampart devices --json
  rampart clients -i`,
	SilenceUsage:  true,
	SilenceErrors: true,
	Version:       version,
}

// Execute runs the root command.
func Execute() error {
	return rootCmd.Execute()
}

func init() {
	pf := rootCmd.PersistentFlags()
	pf.StringVar(&flags.config, "config", "", "path to config file (default ~/.config/rampart/config.json)")
	pf.StringVarP(&flags.host, "host", "H", "", "gateway/controller address, e.g. 192.168.1.1 or https://unifi:8443")
	pf.StringVarP(&flags.site, "site", "s", "default", "UniFi site name")
	pf.StringVarP(&flags.user, "user", "u", "", "username (local admin)")
	pf.StringVarP(&flags.pass, "pass", "p", "", "password")
	pf.StringVarP(&flags.apiKey, "api-key", "k", "", "local API key (preferred over user/pass)")
	pf.BoolVar(&flags.insecure, "insecure", true, "skip TLS certificate verification (UniFi gear ships self-signed certs)")
	pf.BoolVar(&flags.legacy, "legacy", false, "force legacy controller mode (software controller on :8443)")
	pf.DurationVar(&flags.timeout, "timeout", 15*time.Second, "API request timeout")
	pf.BoolVarP(&flags.jsonOut, "json", "j", false, "output raw JSON (for scripting)")
}

type fileConfig struct {
	Host     string `json:"host"`
	Site     string `json:"site"`
	User     string `json:"user"`
	Pass     string `json:"pass"`
	APIKey   string `json:"api_key"`
	Insecure *bool  `json:"insecure"`
	Legacy   *bool  `json:"legacy"`
}

func resolveConfig() (unifi.Config, error) {
	cfg := unifi.Config{Site: "default", Insecure: true, Timeout: flags.timeout}

	// config file (lowest precedence)
	path := flags.config
	explicit := path != ""
	if !explicit {
		if dir, err := os.UserConfigDir(); err == nil {
			path = filepath.Join(dir, "rampart", "config.json")
		}
	}
	if path != "" {
		b, err := os.ReadFile(path)
		switch {
		case err == nil:
			var fc fileConfig
			if err := json.Unmarshal(b, &fc); err != nil {
				return cfg, fmt.Errorf("config file %s: %w", path, err)
			}
			setIf(&cfg.Host, fc.Host)
			setIf(&cfg.Site, fc.Site)
			setIf(&cfg.User, fc.User)
			setIf(&cfg.Pass, fc.Pass)
			setIf(&cfg.APIKey, fc.APIKey)
			if fc.Insecure != nil {
				cfg.Insecure = *fc.Insecure
			}
			if fc.Legacy != nil {
				cfg.Legacy = *fc.Legacy
			}
		case explicit:
			return cfg, fmt.Errorf("config file: %w", err)
		}
	}

	// environment
	setIf(&cfg.Host, os.Getenv("RAMPART_HOST"))
	setIf(&cfg.Site, os.Getenv("RAMPART_SITE"))
	setIf(&cfg.User, os.Getenv("RAMPART_USER"))
	setIf(&cfg.Pass, os.Getenv("RAMPART_PASS"))
	setIf(&cfg.APIKey, os.Getenv("RAMPART_API_KEY"))
	if v := os.Getenv("RAMPART_INSECURE"); v != "" {
		cfg.Insecure = parseBool(v)
	}
	if v := os.Getenv("RAMPART_LEGACY"); v != "" {
		cfg.Legacy = parseBool(v)
	}

	// flags (highest precedence)
	pf := rootCmd.PersistentFlags()
	if pf.Changed("host") {
		cfg.Host = flags.host
	}
	if pf.Changed("site") {
		cfg.Site = flags.site
	}
	if pf.Changed("user") {
		cfg.User = flags.user
	}
	if pf.Changed("pass") {
		cfg.Pass = flags.pass
	}
	if pf.Changed("api-key") {
		cfg.APIKey = flags.apiKey
	}
	if pf.Changed("insecure") {
		cfg.Insecure = flags.insecure
	}
	if pf.Changed("legacy") {
		cfg.Legacy = flags.legacy
	}
	return cfg, nil
}

func titleCase(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func setIf(dst *string, v string) {
	if v != "" {
		*dst = v
	}
}

func parseBool(v string) bool {
	b, err := strconv.ParseBool(v)
	return err == nil && b
}

// withClient resolves configuration, logs in, and runs fn with a
// timeout-bounded context.
func withClient(cmd *cobra.Command, fn func(ctx context.Context, c *unifi.Client) error) error {
	cfg, err := resolveConfig()
	if err != nil {
		return err
	}
	c, err := unifi.New(cfg)
	if err != nil {
		return err
	}
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()
	if err := c.Login(ctx); err != nil {
		return err
	}
	return fn(ctx, c)
}

// backgroundCtx returns a fresh timeout context, for API calls made after the
// original command context may have expired (e.g. from inside the TUI).
func backgroundCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), flags.timeout)
}

func printJSON(v any) error {
	return output.JSON(os.Stdout, v)
}

// splitRaw splits a raw JSON array into raw items so list commands can filter
// without losing fields the typed structs don't know about.
func splitRaw(raw json.RawMessage) ([]json.RawMessage, error) {
	var items []json.RawMessage
	if len(raw) == 0 {
		return nil, nil
	}
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, err
	}
	return items, nil
}
