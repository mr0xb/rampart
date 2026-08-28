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

var devicesInteractive bool

var devicesCmd = &cobra.Command{
	Use:     "devices",
	Aliases: []string{"device", "dev"},
	Short:   "List UniFi devices (gateways, switches, APs)",
	RunE: func(cmd *cobra.Command, args []string) error {
		return withClient(cmd, func(ctx context.Context, c *unifi.Client) error {
			raw, err := c.ListDevices(ctx)
			if err != nil {
				return err
			}
			if flags.jsonOut {
				return printJSON(raw)
			}
			var devices []unifi.Device
			if err := json.Unmarshal(raw, &devices); err != nil {
				return err
			}
			sort.Slice(devices, func(a, b int) bool { return devices[a].Name < devices[b].Name })

			headers := []string{"NAME", "MODEL", "TYPE", "IP", "MAC", "VERSION", "STATE", "UPTIME"}
			rows := make([][]string, len(devices))
			for i, d := range devices {
				rows[i] = []string{
					orDash(d.Name),
					orDash(d.Model),
					orDash(d.Type),
					orDash(d.IP),
					d.MAC,
					orDash(d.Version),
					d.StateString(),
					output.Duration(d.Uptime.Int()),
				}
			}
			if devicesInteractive {
				return output.RunInteractiveTable(output.TableOpts{
					Title:      "rampart · devices",
					Columns:    headers,
					Rows:       rows,
					IDColumn:   -1,
					EnabledCol: -1,
				})
			}
			if len(rows) == 0 {
				fmt.Fprintln(os.Stderr, "No devices found.")
				return nil
			}
			output.Table(os.Stdout, headers, rows)
			return nil
		})
	},
}

func init() {
	devicesCmd.Flags().BoolVarP(&devicesInteractive, "interactive", "i", false, "interactive TUI")
	rootCmd.AddCommand(devicesCmd)
}
