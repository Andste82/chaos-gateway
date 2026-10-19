package ctl

import (
	"bytes"
	"fmt"
	"net/url"
	"os"

	"github.com/spf13/cobra"
)

func (a *app) wgCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "wg", Short: "WireGuard networks and clients"}
	var format, out string
	export := &cobra.Command{
		Use:   "export <network> <client>",
		Short: "Download the configuration of a WireGuard client (wg-quick file or QR code)",
		Long: `Downloads the configuration of a client of a WireGuard network from the gateway: a wg-quick file, or its QR code
as PNG or SVG. The file holds the client's private key if the gateway generated it and keeps it, so it is
secret: it is written with mode 0600, and the call needs a token with scope full and is recorded in the audit
log. Without --out the file goes to standard output.`,
		Example: `  chaosctl wg export lab-wg laptop-1 --out laptop-1.conf`,
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			switch format {
			case "conf", "png", "svg":
			default:
				return failf(ExitError, "--format is conf, png or svg")
			}
			cl, err := a.client()
			if err != nil {
				return err
			}
			var buf bytes.Buffer
			path := "/networks/" + url.PathEscape(args[0]) + "/clients/" + url.PathEscape(args[1]) + "/export"
			if _, err := cl.download(cmd.Context(), path, url.Values{"format": {format}}, &buf); err != nil {
				return apiFail(err)
			}
			if out == "" {
				_, err := a.env.Stdout.Write(buf.Bytes())
				return err
			}
			if err := os.WriteFile(out, buf.Bytes(), 0o600); err != nil {
				return failf(ExitError, "%v", err)
			}
			fmt.Fprintf(a.env.Stderr, "wrote %s\n", out)
			return nil
		},
	}
	export.Flags().StringVar(&format, "format", "conf", "conf, png or svg")
	export.Flags().StringVarP(&out, "out", "o", "", "write to this file (mode 0600) instead of standard output")
	cmd.AddCommand(export)
	return cmd
}
