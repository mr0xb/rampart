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

var groupsCmd = &cobra.Command{
	Use:     "groups",
	Aliases: []string{"group", "fwgroups"},
	Short:   "List firewall groups (address/port groups referenced by rules)",
	RunE: func(cmd *cobra.Command, args []string) error {
		return withClient(cmd, func(ctx context.Context, c *unifi.Client) error {
			raw, err := c.ListFirewallGroups(ctx)
			if err != nil {
				return err
			}
			if flags.jsonOut {
				return printJSON(raw)
			}
			var groups []unifi.FirewallGroup
			if err := json.Unmarshal(raw, &groups); err != nil {
				return err
			}
			headers := []string{"ID", "NAME", "TYPE", "MEMBERS"}
			rows := make([][]string, len(groups))
			for i, g := range groups {
				rows[i] = []string{g.ID, g.Name, g.Type, strings.Join(g.Members, ",")}
			}
			if len(rows) == 0 {
				fmt.Fprintln(os.Stderr, "No firewall groups found.")
				return nil
			}
			output.Table(os.Stdout, headers, rows)
			return nil
		})
	},
}

func init() {
	rootCmd.AddCommand(groupsCmd)
}
