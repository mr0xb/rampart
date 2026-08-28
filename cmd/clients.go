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

var clientsInteractive bool

var clientsCmd = &cobra.Command{
	Use:     "clients",
	Aliases: []string{"client", "sta"},
	Short:   "List connected client devices",
	RunE: func(cmd *cobra.Command, args []string) error {
		return withClient(cmd, func(ctx context.Context, c *unifi.Client) error {
			raw, err := c.ListClients(ctx)
			if err != nil {
				return err
			}
			if flags.jsonOut {
				return printJSON(raw)
			}
			var stas []unifi.Sta
			if err := json.Unmarshal(raw, &stas); err != nil {
				return err
			}
			sort.Slice(stas, func(a, b int) bool { return stas[a].DisplayName() < stas[b].DisplayName() })

			headers := []string{"NAME", "IP", "MAC", "NETWORK", "TYPE", "UPTIME"}
			rows := make([][]string, len(stas))
			for i, s := range stas {
				typ := "wireless"
				if s.IsWired {
					typ = "wired"
				}
				rows[i] = []string{
					s.DisplayName(),
					orDash(s.IP),
					s.MAC,
					orDash(s.Network),
					typ,
					output.Duration(s.Uptime.Int()),
				}
			}
			if clientsInteractive {
				return output.RunInteractiveTable(output.TableOpts{
					Title:      "rampart · clients",
					Columns:    headers,
					Rows:       rows,
					IDColumn:   -1,
					EnabledCol: -1,
				})
			}
			if len(rows) == 0 {
				fmt.Fprintln(os.Stderr, "No connected clients found.")
				return nil
			}
			output.Table(os.Stdout, headers, rows)
			return nil
		})
	},
}

func init() {
	clientsCmd.Flags().BoolVarP(&clientsInteractive, "interactive", "i", false, "interactive TUI")
	rootCmd.AddCommand(clientsCmd)
}
