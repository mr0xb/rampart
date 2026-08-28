package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"

	"github.com/spf13/cobra"

	"github.com/mr0xb/rampart/internal/output"
	"github.com/mr0xb/rampart/internal/unifi"
)

var policyCmd = &cobra.Command{
	Use:     "policy",
	Aliases: []string{"policies", "zbf"},
	Short:   "Manage zone-based firewall policies (UniFi Network 9+)",
	Long: `Manage zone-based firewall policies, the firewall model used by
UniFi Network 9 and later. On older firmware use "rampart fw" instead.`,
}

var policyListFlags struct {
	interactive bool
	all         bool
}

var policyListCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "List firewall policies",
	RunE: func(cmd *cobra.Command, args []string) error {
		return withClient(cmd, func(ctx context.Context, c *unifi.Client) error {
			raw, err := c.ListFirewallPolicies(ctx)
			if err != nil {
				return err
			}
			items, err := splitRaw(raw)
			if err != nil {
				return fmt.Errorf("unexpected policy response (zone-based firewall may not be enabled on this gateway): %w", err)
			}
			policies := make([]unifi.FirewallPolicy, len(items))
			for i, it := range items {
				if err := json.Unmarshal(it, &policies[i]); err != nil {
					return err
				}
			}

			if !policyListFlags.all {
				var fp []unifi.FirewallPolicy
				var fi []json.RawMessage
				for i, p := range policies {
					if !p.Predefined {
						fp = append(fp, p)
						fi = append(fi, items[i])
					}
				}
				policies, items = fp, fi
			}

			order := make([]int, len(policies))
			for i := range order {
				order[i] = i
			}
			sort.SliceStable(order, func(a, b int) bool {
				return policies[order[a]].Index.Int() < policies[order[b]].Index.Int()
			})

			if flags.jsonOut {
				sorted := make([]json.RawMessage, len(order))
				for i, idx := range order {
					sorted[i] = items[idx]
				}
				return printJSON(sorted)
			}

			zoneName := map[string]string{}
			for _, z := range c.ListFirewallZones(ctx) {
				zoneName[z.ID] = z.Name
			}
			zone := func(id string) string {
				if n, ok := zoneName[id]; ok {
					return n
				}
				return orDash(id)
			}

			rows := make([][]string, len(order))
			for i, idx := range order {
				p := policies[idx]
				rows[i] = []string{
					p.ID,
					fmt.Sprint(p.Index.Int()),
					output.YesNo(p.Enabled),
					p.Action,
					orDash(p.Protocol),
					zone(p.Source.ZoneID),
					zone(p.Destination.ZoneID),
					output.YesNo(p.Predefined),
					p.Name,
				}
			}
			headers := []string{"ID", "INDEX", "ENABLED", "ACTION", "PROTO", "SRC ZONE", "DST ZONE", "PREDEF", "NAME"}

			if policyListFlags.interactive {
				return output.RunInteractiveTable(output.TableOpts{
					Title:      "rampart · firewall policies (zone-based)",
					Columns:    headers,
					Rows:       rows,
					IDColumn:   0,
					EnabledCol: 2,
					OnToggle: func(id string, enable bool) error {
						ctx, cancel := backgroundCtx()
						defer cancel()
						return c.SetFirewallPolicyEnabled(ctx, id, enable)
					},
				})
			}

			if len(rows) == 0 {
				fmt.Fprintln(os.Stderr, "No firewall policies found. On pre-9.x firmware use: rampart fw list (or pass --all to include predefined policies)")
				return nil
			}
			output.Table(os.Stdout, headers, rows)
			return nil
		})
	},
}

var policyGetCmd = &cobra.Command{
	Use:               "get <policy-id>",
	Short:             "Show the full JSON of one policy",
	Args:              cobra.ExactArgs(1),
	ValidArgsFunction: completePolicyIDs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return withClient(cmd, func(ctx context.Context, c *unifi.Client) error {
			p, err := c.GetFirewallPolicy(ctx, args[0])
			if err != nil {
				return err
			}
			return printJSON(p)
		})
	},
}

var policyAddFile string

var policyAddCmd = &cobra.Command{
	Use:   "add -f <policy.json>",
	Short: "Create a policy from a JSON file",
	Long: `Create a zone-based policy from a JSON definition.

The policy schema is rich (zones, matching targets, schedules); the easiest
way to author one is "rampart policy get <id>" on an existing policy, edit the
JSON (drop "_id"), and feed it back in. Use "-" to read from stdin.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if policyAddFile == "" {
			return fmt.Errorf("-f <file> is required (use - for stdin)")
		}
		var data []byte
		var err error
		if policyAddFile == "-" {
			data, err = os.ReadFile("/dev/stdin")
		} else {
			data, err = os.ReadFile(policyAddFile)
		}
		if err != nil {
			return err
		}
		var policy map[string]any
		if err := json.Unmarshal(data, &policy); err != nil {
			return fmt.Errorf("invalid policy JSON: %w", err)
		}
		delete(policy, "_id")
		return withClient(cmd, func(ctx context.Context, c *unifi.Client) error {
			created, err := c.CreateFirewallPolicy(ctx, policy)
			if err != nil {
				return err
			}
			if flags.jsonOut {
				return printJSON(created)
			}
			var p unifi.FirewallPolicy
			if json.Unmarshal(created, &p) == nil && p.ID != "" {
				fmt.Printf("Created policy %s: %q\n", p.ID, p.Name)
			} else {
				fmt.Println("Policy created.")
			}
			return nil
		})
	},
}

func policySetEnabledCmd(enable bool) *cobra.Command {
	verb := "disable"
	if enable {
		verb = "enable"
	}
	return &cobra.Command{
		Use:               verb + " <policy-id>...",
		Short:             titleCase(verb) + " firewall policy(ies)",
		Args:              cobra.MinimumNArgs(1),
		ValidArgsFunction: completePolicyIDs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return withClient(cmd, func(ctx context.Context, c *unifi.Client) error {
				for _, id := range args {
					if err := c.SetFirewallPolicyEnabled(ctx, id, enable); err != nil {
						return err
					}
					if !flags.jsonOut {
						fmt.Printf("%sd policy %s.\n", titleCase(verb), id)
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

var policyDeleteCmd = &cobra.Command{
	Use:               "delete <policy-id>...",
	Aliases:           []string{"rm", "remove"},
	Short:             "Delete firewall policy(ies)",
	Args:              cobra.MinimumNArgs(1),
	ValidArgsFunction: completePolicyIDs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return withClient(cmd, func(ctx context.Context, c *unifi.Client) error {
			for _, id := range args {
				if err := c.DeleteFirewallPolicy(ctx, id); err != nil {
					return err
				}
				if !flags.jsonOut {
					fmt.Printf("Deleted policy %s.\n", id)
				}
			}
			if flags.jsonOut {
				return printJSON(map[string]any{"ok": true, "deleted": args})
			}
			return nil
		})
	},
}

func completePolicyIDs(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	var out []string
	_ = withClient(cmd, func(ctx context.Context, c *unifi.Client) error {
		raw, err := c.ListFirewallPolicies(ctx)
		if err != nil {
			return err
		}
		var policies []unifi.FirewallPolicy
		if err := json.Unmarshal(raw, &policies); err != nil {
			return err
		}
		for _, p := range policies {
			out = append(out, fmt.Sprintf("%s\t%s", p.ID, p.Name))
		}
		return nil
	})
	return out, cobra.ShellCompDirectiveNoFileComp
}

func init() {
	policyListCmd.Flags().BoolVarP(&policyListFlags.interactive, "interactive", "i", false, "interactive TUI (space toggles enabled)")
	policyListCmd.Flags().BoolVarP(&policyListFlags.all, "all", "a", false, "include predefined policies")
	policyAddCmd.Flags().StringVarP(&policyAddFile, "file", "f", "", "JSON file with the policy definition (- for stdin)")

	policyCmd.AddCommand(policyListCmd, policyGetCmd, policyAddCmd, policySetEnabledCmd(true), policySetEnabledCmd(false), policyDeleteCmd)
	rootCmd.AddCommand(policyCmd)
}
