package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mr0xb/rampart/internal/output"
	"github.com/mr0xb/rampart/internal/unifi"
)

var pfInterfaces = []string{"wan", "wan2"}
var pfProtocols = []string{"tcp_udp", "tcp", "udp"}

var pfCmd = &cobra.Command{
	Use:     "pf",
	Aliases: []string{"portforward", "port-forward", "forward"},
	Short:   "Manage port forwarding rules",
	Long: `Manage port-forwarding rules (NAT from a WAN port to an internal host).

Each rule forwards traffic arriving on a WAN port (--dst-port) to an internal
address (--fwd) and port (--fwd-port).`,
}

// --- pf list ---

var pfListFlags struct {
	interactive bool
}

var pfListCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "List port forwarding rules",
	RunE: func(cmd *cobra.Command, args []string) error {
		return withClient(cmd, func(ctx context.Context, c *unifi.Client) error {
			raw, err := c.ListPortForwards(ctx)
			if err != nil {
				return err
			}
			if flags.jsonOut {
				return printJSON(raw)
			}
			var rules []unifi.PortForward
			if err := json.Unmarshal(raw, &rules); err != nil {
				return err
			}

			headers := []string{"ID", "ENABLED", "IFACE", "PROTO", "SRC", "WAN-PORT", "FWD-TO", "FWD-PORT", "NAME"}
			rows := make([][]string, len(rules))
			for i, r := range rules {
				rows[i] = []string{
					r.ID,
					output.YesNo(r.Enabled),
					orDash(r.Interface),
					orDash(r.Proto),
					orDash(r.Src),
					orDash(r.DstPort),
					orDash(r.Fwd),
					orDash(r.FwdPort),
					r.Name,
				}
			}

			if pfListFlags.interactive {
				return output.RunInteractiveTable(output.TableOpts{
					Title:      "rampart · port forwards",
					Columns:    headers,
					Rows:       rows,
					IDColumn:   0,
					EnabledCol: 1,
					OnToggle: func(id string, enable bool) error {
						ctx, cancel := backgroundCtx()
						defer cancel()
						return c.SetPortForwardEnabled(ctx, id, enable)
					},
				})
			}

			if len(rows) == 0 {
				fmt.Fprintln(os.Stderr, "No port forwarding rules found.")
				return nil
			}
			output.Table(os.Stdout, headers, rows)
			return nil
		})
	},
}

// --- pf get ---

var pfGetCmd = &cobra.Command{
	Use:               "get <rule-id>",
	Short:             "Show the full JSON of one port forwarding rule",
	Args:              cobra.ExactArgs(1),
	ValidArgsFunction: completePortForwardIDs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return withClient(cmd, func(ctx context.Context, c *unifi.Client) error {
			rule, err := c.GetPortForward(ctx, args[0])
			if err != nil {
				return err
			}
			return printJSON(rule)
		})
	},
}

// --- pf add / set ---

type pfRuleFlags struct {
	name    string
	iface   string
	proto   string
	src     string
	dstPort string
	fwd     string
	fwdPort string
	enabled bool
	log     bool
}

func addPortForwardFlags(cmd *cobra.Command, f *pfRuleFlags) {
	cmd.Flags().StringVar(&f.name, "name", "", "rule name")
	cmd.Flags().StringVar(&f.iface, "interface", "wan", "WAN interface: wan or wan2")
	cmd.Flags().StringVar(&f.proto, "protocol", "tcp_udp", "protocol: tcp_udp, tcp or udp")
	cmd.Flags().StringVar(&f.src, "src", "any", "allowed source address/CIDR (\"any\" for unrestricted)")
	cmd.Flags().StringVar(&f.dstPort, "dst-port", "", "external WAN port (or range like 8000:8010)")
	cmd.Flags().StringVar(&f.fwd, "fwd", "", "internal address to forward to")
	cmd.Flags().StringVar(&f.fwdPort, "fwd-port", "", "internal port (defaults to --dst-port if omitted)")
	cmd.Flags().BoolVar(&f.enabled, "enabled", true, "whether the rule is enabled")
	cmd.Flags().BoolVar(&f.log, "log", false, "log packets matching the rule")

	cmd.RegisterFlagCompletionFunc("interface", func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		return pfInterfaces, cobra.ShellCompDirectiveNoFileComp
	})
	cmd.RegisterFlagCompletionFunc("protocol", func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		return pfProtocols, cobra.ShellCompDirectiveNoFileComp
	})
}

var pfAddFlagVals pfRuleFlags

var pfAddCmd = &cobra.Command{
	Use:   "add",
	Short: "Add a port forwarding rule",
	Example: `  rampart pf add --name "Web" --dst-port 80 --fwd 192.168.1.10 --fwd-port 8080 --protocol tcp
  rampart pf add --name "Game server" --dst-port 25565 --fwd 192.168.1.50 --src 203.0.113.0/24`,
	RunE: func(cmd *cobra.Command, args []string) error {
		f := &pfAddFlagVals
		if f.name == "" {
			return fmt.Errorf("--name is required")
		}
		if f.dstPort == "" {
			return fmt.Errorf("--dst-port is required (the WAN port to forward)")
		}
		if f.fwd == "" {
			return fmt.Errorf("--fwd is required (the internal address to forward to)")
		}
		f.proto = strings.ToLower(f.proto)
		f.iface = strings.ToLower(f.iface)
		fwdPort := f.fwdPort
		if fwdPort == "" {
			fwdPort = f.dstPort
		}
		src := f.src
		if src == "" {
			src = "any"
		}

		return withClient(cmd, func(ctx context.Context, c *unifi.Client) error {
			rule := map[string]any{
				"name":           f.name,
				"enabled":        f.enabled,
				"pfwd_interface": f.iface,
				"proto":          f.proto,
				"src":            src,
				"dst_port":       f.dstPort,
				"fwd":            f.fwd,
				"fwd_port":       fwdPort,
				"log":            f.log,
				"destination_ip": "any",
			}
			data, err := c.CreatePortForward(ctx, rule)
			if err != nil {
				return err
			}
			if flags.jsonOut {
				return printJSON(data)
			}
			var created []unifi.PortForward
			if json.Unmarshal(data, &created) == nil && len(created) > 0 {
				r := created[0]
				fmt.Printf("Created port forward %s: %q (%s:%s -> %s:%s)\n", r.ID, r.Name, orDash(r.Interface), r.DstPort, r.Fwd, r.FwdPort)
			} else {
				fmt.Println("Port forward created.")
			}
			return nil
		})
	},
}

var pfSetFlagVals pfRuleFlags

var pfSetCmd = &cobra.Command{
	Use:               "set <rule-id>",
	Short:             "Modify a port forwarding rule (only the flags you pass are changed)",
	Example:           `  rampart pf set 6633aabb... --fwd-port 9090 --log`,
	Args:              cobra.ExactArgs(1),
	ValidArgsFunction: completePortForwardIDs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return withClient(cmd, func(ctx context.Context, c *unifi.Client) error {
			rule, err := c.GetPortForward(ctx, args[0])
			if err != nil {
				return err
			}
			f := &pfSetFlagVals
			changedAny := false
			apply := func(flag, key string, val any) {
				if cmd.Flags().Changed(flag) {
					rule[key] = val
					changedAny = true
				}
			}
			apply("name", "name", f.name)
			apply("interface", "pfwd_interface", strings.ToLower(f.iface))
			apply("protocol", "proto", strings.ToLower(f.proto))
			apply("src", "src", f.src)
			apply("dst-port", "dst_port", f.dstPort)
			apply("fwd", "fwd", f.fwd)
			apply("fwd-port", "fwd_port", f.fwdPort)
			apply("enabled", "enabled", f.enabled)
			apply("log", "log", f.log)
			if !changedAny {
				return fmt.Errorf("nothing to change: pass at least one flag (see rampart pf set --help)")
			}
			data, err := c.UpdatePortForward(ctx, args[0], rule)
			if err != nil {
				return err
			}
			if flags.jsonOut {
				return printJSON(data)
			}
			fmt.Printf("Updated port forward %s.\n", args[0])
			return nil
		})
	},
}

// --- pf enable / disable / delete ---

func pfSetEnabledCmd(enable bool) *cobra.Command {
	verb := "disable"
	if enable {
		verb = "enable"
	}
	return &cobra.Command{
		Use:               verb + " <rule-id>...",
		Short:             titleCase(verb) + " port forwarding rule(s)",
		Args:              cobra.MinimumNArgs(1),
		ValidArgsFunction: completePortForwardIDs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return withClient(cmd, func(ctx context.Context, c *unifi.Client) error {
				for _, id := range args {
					if err := c.SetPortForwardEnabled(ctx, id, enable); err != nil {
						return err
					}
					if !flags.jsonOut {
						fmt.Printf("%sd port forward %s.\n", titleCase(verb), id)
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

var pfDeleteCmd = &cobra.Command{
	Use:               "delete <rule-id>...",
	Aliases:           []string{"rm", "remove"},
	Short:             "Delete port forwarding rule(s)",
	Args:              cobra.MinimumNArgs(1),
	ValidArgsFunction: completePortForwardIDs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return withClient(cmd, func(ctx context.Context, c *unifi.Client) error {
			for _, id := range args {
				if err := c.DeletePortForward(ctx, id); err != nil {
					return err
				}
				if !flags.jsonOut {
					fmt.Printf("Deleted port forward %s.\n", id)
				}
			}
			if flags.jsonOut {
				return printJSON(map[string]any{"ok": true, "deleted": args})
			}
			return nil
		})
	},
}

// completePortForwardIDs provides live shell completion of port forward IDs
// (annotated with name and target) by querying the controller. Errors are
// swallowed: completion must never break the shell.
func completePortForwardIDs(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	var out []string
	_ = withClient(cmd, func(ctx context.Context, c *unifi.Client) error {
		raw, err := c.ListPortForwards(ctx)
		if err != nil {
			return err
		}
		var rules []unifi.PortForward
		if err := json.Unmarshal(raw, &rules); err != nil {
			return err
		}
		for _, r := range rules {
			out = append(out, fmt.Sprintf("%s\t%s [%s->%s:%s]", r.ID, r.Name, r.DstPort, r.Fwd, r.FwdPort))
		}
		return nil
	})
	return out, cobra.ShellCompDirectiveNoFileComp
}

func init() {
	pfListCmd.Flags().BoolVarP(&pfListFlags.interactive, "interactive", "i", false, "interactive TUI (space toggles enabled)")

	addPortForwardFlags(pfAddCmd, &pfAddFlagVals)
	addPortForwardFlags(pfSetCmd, &pfSetFlagVals)

	pfCmd.AddCommand(pfListCmd, pfGetCmd, pfAddCmd, pfSetCmd, pfSetEnabledCmd(true), pfSetEnabledCmd(false), pfDeleteCmd)
	rootCmd.AddCommand(pfCmd)
}
