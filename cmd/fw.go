package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mr0xb/rampart/internal/output"
	"github.com/mr0xb/rampart/internal/unifi"
)

var knownRulesets = []string{
	"WAN_IN", "WAN_OUT", "WAN_LOCAL",
	"LAN_IN", "LAN_OUT", "LAN_LOCAL",
	"GUEST_IN", "GUEST_OUT", "GUEST_LOCAL",
	"WANv6_IN", "WANv6_OUT", "WANv6_LOCAL",
	"LANv6_IN", "LANv6_OUT", "LANv6_LOCAL",
	"GUESTv6_IN", "GUESTv6_OUT", "GUESTv6_LOCAL",
}

var fwCmd = &cobra.Command{
	Use:     "fw",
	Aliases: []string{"firewall"},
	Short:   "Manage classic firewall rules",
	Long: `Manage classic (ruleset-based) firewall rules.

If your gateway runs UniFi Network 9+ with the zone-based firewall, classic
rules may be empty — use "rampart policy" instead.`,
}

// --- fw list ---

var fwListFlags struct {
	ruleset     string
	interactive bool
}

var fwListCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "List firewall rules",
	RunE: func(cmd *cobra.Command, args []string) error {
		return withClient(cmd, func(ctx context.Context, c *unifi.Client) error {
			raw, err := c.ListFirewallRules(ctx)
			if err != nil {
				return err
			}
			items, err := splitRaw(raw)
			if err != nil {
				return err
			}
			rules := make([]unifi.FirewallRule, len(items))
			for i, it := range items {
				if err := json.Unmarshal(it, &rules[i]); err != nil {
					return err
				}
			}

			// filter by ruleset, keeping typed and raw views in sync
			if rs := strings.ToUpper(fwListFlags.ruleset); rs != "" {
				var fr []unifi.FirewallRule
				var fi []json.RawMessage
				for i, r := range rules {
					if r.Ruleset == rs {
						fr = append(fr, r)
						fi = append(fi, items[i])
					}
				}
				rules, items = fr, fi
			}

			order := make([]int, len(rules))
			for i := range order {
				order[i] = i
			}
			sort.SliceStable(order, func(a, b int) bool {
				ra, rb := rules[order[a]], rules[order[b]]
				if ra.Ruleset != rb.Ruleset {
					return ra.Ruleset < rb.Ruleset
				}
				return ra.RuleIndex.Int() < rb.RuleIndex.Int()
			})

			if flags.jsonOut {
				sorted := make([]json.RawMessage, len(order))
				for i, idx := range order {
					sorted[i] = items[idx]
				}
				return printJSON(sorted)
			}

			rows := make([][]string, len(order))
			for i, idx := range order {
				r := rules[idx]
				rows[i] = []string{
					r.ID,
					fmt.Sprint(r.RuleIndex.Int()),
					r.Ruleset,
					output.YesNo(r.Enabled),
					r.Action,
					orDash(r.Protocol),
					orDash(r.SrcAddress),
					orDash(r.SrcPort),
					orDash(r.DstAddress),
					orDash(r.DstPort),
					r.Name,
				}
			}
			headers := []string{"ID", "INDEX", "RULESET", "ENABLED", "ACTION", "PROTO", "SRC", "SPORT", "DST", "DPORT", "NAME"}

			if fwListFlags.interactive {
				return output.RunInteractiveTable(output.TableOpts{
					Title:      "rampart · firewall rules",
					Columns:    headers,
					Rows:       rows,
					IDColumn:   0,
					EnabledCol: 3,
					OnToggle: func(id string, enable bool) error {
						ctx, cancel := backgroundCtx()
						defer cancel()
						return c.SetFirewallRuleEnabled(ctx, id, enable)
					},
				})
			}

			if len(rows) == 0 {
				fmt.Fprintln(os.Stderr, "No classic firewall rules found. If your gateway uses the zone-based firewall (UniFi Network 9+), try: rampart policy list")
				return nil
			}
			output.Table(os.Stdout, headers, rows)
			return nil
		})
	},
}

// --- fw get ---

var fwGetCmd = &cobra.Command{
	Use:               "get <rule-id>",
	Short:             "Show the full JSON of one firewall rule",
	Args:              cobra.ExactArgs(1),
	ValidArgsFunction: completeRuleIDs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return withClient(cmd, func(ctx context.Context, c *unifi.Client) error {
			rule, err := c.GetFirewallRule(ctx, args[0])
			if err != nil {
				return err
			}
			return printJSON(rule)
		})
	},
}

// --- fw add / set ---

type fwRuleFlags struct {
	name     string
	ruleset  string
	action   string
	protocol string
	src      string
	srcPort  string
	dst      string
	dstPort  string
	index    int
	enabled  bool
	log      bool
}

func addRuleFlags(cmd *cobra.Command, f *fwRuleFlags) {
	cmd.Flags().StringVar(&f.name, "name", "", "rule name")
	cmd.Flags().StringVar(&f.ruleset, "ruleset", "WAN_IN", "ruleset ("+strings.Join(knownRulesets[:6], ", ")+", ...)")
	cmd.Flags().StringVar(&f.action, "action", "drop", "action: accept, drop or reject")
	cmd.Flags().StringVar(&f.protocol, "protocol", "all", "protocol: all, tcp, udp, tcp_udp, icmp, ...")
	cmd.Flags().StringVar(&f.src, "src", "", "source address or CIDR")
	cmd.Flags().StringVar(&f.srcPort, "src-port", "", "source port (or range like 1000:2000)")
	cmd.Flags().StringVar(&f.dst, "dst", "", "destination address or CIDR")
	cmd.Flags().StringVar(&f.dstPort, "dst-port", "", "destination port (or range)")
	cmd.Flags().IntVar(&f.index, "index", 0, "rule index (0 = pick next free user index, starting at 2000)")
	cmd.Flags().BoolVar(&f.enabled, "enabled", true, "whether the rule is enabled")
	cmd.Flags().BoolVar(&f.log, "log", false, "log packets matching the rule")

	cmd.RegisterFlagCompletionFunc("ruleset", func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		return knownRulesets, cobra.ShellCompDirectiveNoFileComp
	})
	cmd.RegisterFlagCompletionFunc("action", func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		return []string{"accept", "drop", "reject"}, cobra.ShellCompDirectiveNoFileComp
	})
	cmd.RegisterFlagCompletionFunc("protocol", func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		return []string{"all", "tcp", "udp", "tcp_udp", "icmp"}, cobra.ShellCompDirectiveNoFileComp
	})
}

var fwAddFlagVals fwRuleFlags

var fwAddCmd = &cobra.Command{
	Use:   "add",
	Short: "Add a firewall rule",
	Example: `  rampart fw add --name "Block telnet" --ruleset LAN_IN --action drop --protocol tcp --dst-port 23
  rampart fw add --name "Allow cam VLAN to NVR" --ruleset LAN_IN --action accept \
      --src 10.0.30.0/24 --dst 10.0.10.5 --protocol tcp --dst-port 7441`,
	RunE: func(cmd *cobra.Command, args []string) error {
		f := &fwAddFlagVals
		if f.name == "" {
			return fmt.Errorf("--name is required")
		}
		f.action = strings.ToLower(f.action)
		if f.action != "accept" && f.action != "drop" && f.action != "reject" {
			return fmt.Errorf("invalid --action %q (accept, drop or reject)", f.action)
		}
		f.ruleset = strings.ToUpper(strings.ReplaceAll(f.ruleset, "V6", "v6"))

		return withClient(cmd, func(ctx context.Context, c *unifi.Client) error {
			index := f.index
			if index == 0 {
				var err error
				if index, err = nextRuleIndex(ctx, c, f.ruleset); err != nil {
					return err
				}
			}
			rule := map[string]any{
				"name":                    f.name,
				"ruleset":                 f.ruleset,
				"rule_index":              index,
				"action":                  f.action,
				"enabled":                 f.enabled,
				"protocol":                strings.ToLower(f.protocol),
				"src_address":             f.src,
				"src_port":                f.srcPort,
				"dst_address":             f.dst,
				"dst_port":                f.dstPort,
				"logging":                 f.log,
				"protocol_match_excepted": false,
				"state_established":       false,
				"state_invalid":           false,
				"state_new":               false,
				"state_related":           false,
				"ipsec":                   "",
				"src_firewallgroup_ids":   []string{},
				"dst_firewallgroup_ids":   []string{},
				"src_mac_address":         "",
				"setting_preference":      "manual",
			}
			data, err := c.CreateFirewallRule(ctx, rule)
			if err != nil {
				return err
			}
			if flags.jsonOut {
				return printJSON(data)
			}
			var created []unifi.FirewallRule
			if json.Unmarshal(data, &created) == nil && len(created) > 0 {
				fmt.Printf("Created rule %s: %q (%s index %d)\n", created[0].ID, created[0].Name, created[0].Ruleset, created[0].RuleIndex.Int())
			} else {
				fmt.Println("Rule created.")
			}
			return nil
		})
	},
}

var fwSetFlagVals fwRuleFlags

var fwSetCmd = &cobra.Command{
	Use:               "set <rule-id>",
	Short:             "Modify a firewall rule (only the flags you pass are changed)",
	Example:           `  rampart fw set 6633aabb... --dst-port 2222 --log`,
	Args:              cobra.ExactArgs(1),
	ValidArgsFunction: completeRuleIDs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return withClient(cmd, func(ctx context.Context, c *unifi.Client) error {
			rule, err := c.GetFirewallRule(ctx, args[0])
			if err != nil {
				return err
			}
			f := &fwSetFlagVals
			changedAny := false
			apply := func(flag, key string, val any) {
				if cmd.Flags().Changed(flag) {
					rule[key] = val
					changedAny = true
				}
			}
			apply("name", "name", f.name)
			apply("ruleset", "ruleset", strings.ToUpper(strings.ReplaceAll(f.ruleset, "V6", "v6")))
			apply("action", "action", strings.ToLower(f.action))
			apply("protocol", "protocol", strings.ToLower(f.protocol))
			apply("src", "src_address", f.src)
			apply("src-port", "src_port", f.srcPort)
			apply("dst", "dst_address", f.dst)
			apply("dst-port", "dst_port", f.dstPort)
			apply("index", "rule_index", f.index)
			apply("enabled", "enabled", f.enabled)
			apply("log", "logging", f.log)
			if !changedAny {
				return fmt.Errorf("nothing to change: pass at least one flag (see rampart fw set --help)")
			}
			data, err := c.UpdateFirewallRule(ctx, args[0], rule)
			if err != nil {
				return err
			}
			if flags.jsonOut {
				return printJSON(data)
			}
			fmt.Printf("Updated rule %s.\n", args[0])
			return nil
		})
	},
}

// --- fw enable / disable / delete ---

func setEnabledCmd(enable bool) *cobra.Command {
	verb := "disable"
	if enable {
		verb = "enable"
	}
	return &cobra.Command{
		Use:               verb + " <rule-id>...",
		Short:             titleCase(verb) + " firewall rule(s)",
		Args:              cobra.MinimumNArgs(1),
		ValidArgsFunction: completeRuleIDs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return withClient(cmd, func(ctx context.Context, c *unifi.Client) error {
				for _, id := range args {
					if err := c.SetFirewallRuleEnabled(ctx, id, enable); err != nil {
						return err
					}
					if !flags.jsonOut {
						fmt.Printf("%sd rule %s.\n", titleCase(verb), id)
					}
				}
				if flags.jsonOut {
					return printJSON(map[string]any{"ok": true, "ids": args, "enabled": enable})
				}
				return nil
			})
		},
	}
}

var fwDeleteCmd = &cobra.Command{
	Use:               "delete <rule-id>...",
	Aliases:           []string{"rm", "remove"},
	Short:             "Delete firewall rule(s)",
	Args:              cobra.MinimumNArgs(1),
	ValidArgsFunction: completeRuleIDs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return withClient(cmd, func(ctx context.Context, c *unifi.Client) error {
			for _, id := range args {
				if err := c.DeleteFirewallRule(ctx, id); err != nil {
					return err
				}
				if !flags.jsonOut {
					fmt.Printf("Deleted rule %s.\n", id)
				}
			}
			if flags.jsonOut {
				return printJSON(map[string]any{"ok": true, "deleted": args})
			}
			return nil
		})
	},
}

// nextRuleIndex finds the next free user-rule index (>= 2000) in a ruleset.
func nextRuleIndex(ctx context.Context, c *unifi.Client, ruleset string) (int, error) {
	raw, err := c.ListFirewallRules(ctx)
	if err != nil {
		return 0, err
	}
	var rules []unifi.FirewallRule
	if err := json.Unmarshal(raw, &rules); err != nil {
		return 0, err
	}
	next := 2000
	for _, r := range rules {
		if r.Ruleset == ruleset && r.RuleIndex.Int() >= next {
			next = r.RuleIndex.Int() + 1
		}
	}
	return next, nil
}

// completeRuleIDs provides live shell completion of rule IDs (annotated with
// the rule name) by querying the controller. Errors are swallowed: completion
// must never break the shell.
func completeRuleIDs(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	var out []string
	_ = withClient(cmd, func(ctx context.Context, c *unifi.Client) error {
		raw, err := c.ListFirewallRules(ctx)
		if err != nil {
			return err
		}
		var rules []unifi.FirewallRule
		if err := json.Unmarshal(raw, &rules); err != nil {
			return err
		}
		for _, r := range rules {
			out = append(out, fmt.Sprintf("%s\t%s [%s]", r.ID, r.Name, r.Ruleset))
		}
		return nil
	})
	return out, cobra.ShellCompDirectiveNoFileComp
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func init() {
	fwListCmd.Flags().StringVar(&fwListFlags.ruleset, "ruleset", "", "filter by ruleset (e.g. WAN_IN)")
	fwListCmd.Flags().BoolVarP(&fwListFlags.interactive, "interactive", "i", false, "interactive TUI (space toggles enabled)")
	fwListCmd.RegisterFlagCompletionFunc("ruleset", func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		return knownRulesets, cobra.ShellCompDirectiveNoFileComp
	})

	addRuleFlags(fwAddCmd, &fwAddFlagVals)
	addRuleFlags(fwSetCmd, &fwSetFlagVals)

	fwCmd.AddCommand(fwListCmd, fwGetCmd, fwAddCmd, fwSetCmd, setEnabledCmd(true), setEnabledCmd(false), fwDeleteCmd)
	rootCmd.AddCommand(fwCmd)
}
