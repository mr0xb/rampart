package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

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
	edit        bool
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
			zoneID := map[string]string{}
			var zoneNames []string
			for _, z := range c.ListFirewallZones(ctx) {
				zoneName[z.ID] = z.Name
				zoneID[z.Name] = z.ID
				zoneNames = append(zoneNames, z.Name)
			}
			sort.Strings(zoneNames)
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

			if policyListFlags.interactive || policyListFlags.edit {
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
					EditFields:  policyEditFields(zoneNames),
					StartInEdit: policyListFlags.edit,
					EditGuard:   policyEditGuard,
					OnEdit: func(id string, values map[string]string) error {
						ctx, cancel := backgroundCtx()
						defer cancel()
						return updateFirewallPolicyFields(ctx, c, id, values, zoneID)
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

// policyEditFields describes the policy fields editable from the TUI. Column
// indices refer to the `policy list` table headers. Zone fields are offered
// only when the controller told us the zone names, since editing a zone means
// mapping a name back to its id.
func policyEditFields(zoneNames []string) []output.EditField {
	fields := []output.EditField{
		{Label: "name", Key: "name", Column: 8, Kind: output.FieldText,
			Validate: required("a name")},
		{Label: "action", Key: "action", Column: 3, Kind: output.FieldChoice,
			Options: []string{"ALLOW", "BLOCK", "REJECT"}},
		{Label: "proto", Key: "protocol", Column: 4, Kind: output.FieldText},
	}
	if len(zoneNames) > 0 {
		fields = append(fields,
			output.EditField{Label: "src zone", Key: "source.zone_id", Column: 5,
				Kind: output.FieldChoice, Options: zoneNames},
			output.EditField{Label: "dst zone", Key: "destination.zone_id", Column: 6,
				Kind: output.FieldChoice, Options: zoneNames},
		)
	}
	return append(fields, output.EditField{
		Label: "index", Key: "index", Column: 1, Kind: output.FieldText,
		Validate: validIndex,
	})
}

// policyEditGuard blocks editing predefined policies: the controller owns them
// and rejects most changes.
func policyEditGuard(row []string) error {
	if len(row) > 7 && row[7] == "yes" {
		return fmt.Errorf("policy is predefined")
	}
	return nil
}

// updateFirewallPolicyFields round-trips the policy so fields rampart does not
// display survive the edit. Zone names are mapped back to ids; a name the
// controller never reported is left alone rather than guessed at.
func updateFirewallPolicyFields(ctx context.Context, c *unifi.Client, id string, values map[string]string, zoneID map[string]string) error {
	policy, err := c.GetFirewallPolicy(ctx, id)
	if err != nil {
		return err
	}
	for k, v := range values {
		switch k {
		case "index":
			n, err := strconv.Atoi(v)
			if err != nil {
				return fmt.Errorf("index %q is not a number", v)
			}
			policy[k] = n
		case "source.zone_id", "destination.zone_id":
			zid, ok := zoneID[v]
			if !ok {
				continue // unknown zone name: leave the existing id in place
			}
			parent := strings.SplitN(k, ".", 2)[0]
			nested, ok := policy[parent].(map[string]any)
			if !ok {
				nested = map[string]any{}
			}
			nested["zone_id"] = zid
			policy[parent] = nested
		case "action":
			policy[k] = strings.ToUpper(v)
		default:
			policy[k] = v
		}
	}
	_, err = c.UpdateFirewallPolicy(ctx, id, policy)
	return err
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
	policyListCmd.Flags().BoolVarP(&policyListFlags.interactive, "interactive", "i", false, "interactive TUI (space toggles enabled, e edits)")
	policyListCmd.Flags().BoolVarP(&policyListFlags.edit, "edit", "e", false, "interactive TUI opened straight into the edit form")
	policyListCmd.Flags().BoolVarP(&policyListFlags.all, "all", "a", false, "include predefined policies")
	policyAddCmd.Flags().StringVarP(&policyAddFile, "file", "f", "", "JSON file with the policy definition (- for stdin)")

	policyCmd.AddCommand(policyListCmd, policyGetCmd, policyAddCmd, policySetEnabledCmd(true), policySetEnabledCmd(false), policyDeleteCmd)
	rootCmd.AddCommand(policyCmd)
}
