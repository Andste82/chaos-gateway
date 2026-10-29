package ctl

import (
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/Andste82/chaos-gateway/internal/model"
)

// The HTTPS certificate of the gateway (M29): `chaosctl certificate show` prints the one the management listeners present,
// `install` replaces the gateway's self-signed one with a chain and key from files, `reset` goes back to the self-signed one.
// Installing and resetting need a token with the scope full (show needs read). The key is read from a file and never
// from the command line, so it does not show up in the list of processes.

func (a *app) certificateCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "certificate", Short: "The HTTPS certificate of the web UI and the API"}

	show := &cobra.Command{
		Use:   "show",
		Short: "Print the certificate the gateway presents",
		Long: `Prints the certificate the management listeners present: whether it is the gateway's own self-signed one or an installed
one, the names it is valid for, its validity and its SHA-256 fingerprint, which --fingerprint (CHAOSGW_FINGERPRINT) pins.`,
		Example: `  chaosctl certificate show
  chaosctl certificate show --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cl, err := a.client()
			if err != nil {
				return err
			}
			var info model.CertificateInfo
			if _, err := cl.call(cmd.Context(), http.MethodGet, "/system/certificate", nil, nil, &info); err != nil {
				return apiFail(err)
			}
			a.printCertificate(info)
			return nil
		},
	}

	var certFile, keyFile string
	install := &cobra.Command{
		Use:   "install --cert FILE --key FILE",
		Short: "Replace the gateway's certificate with your own",
		Long: `Installs a certificate chain (PEM, the certificate first, then the intermediates) and its private key (PEM, without a password)
for the web UI and the API. The gateway checks that the certificate is valid now and names a DNS name or an address, that the
key belongs to it and that the chain is in order, and refuses it with the reason otherwise. New connections get the certificate
at once; established ones keep the old one. It needs a token with the scope full.

The service containers keep verifying the gateway's own certificate on the loopback and the service address, so the installed
one does not have to name those.`,
		Example: `  chaosctl certificate install --cert fullchain.pem --key privkey.pem`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if certFile == "" || keyFile == "" {
				return failf(ExitError, "--cert and --key are required")
			}
			certPEM, err := os.ReadFile(certFile)
			if err != nil {
				return failf(ExitError, "%v", err)
			}
			keyPEM, err := os.ReadFile(keyFile)
			if err != nil {
				return failf(ExitError, "%v", err)
			}
			cl, err := a.client()
			if err != nil {
				return err
			}
			var info model.CertificateInfo
			body := model.CertificateUpload{Certificate: string(certPEM), PrivateKey: string(keyPEM)}
			if _, err := cl.call(cmd.Context(), http.MethodPut, "/system/certificate", nil, body, &info); err != nil {
				return apiFail(err)
			}
			if !a.asJSON {
				fmt.Fprintln(a.env.Stdout, "installed:")
			}
			a.printCertificate(info)
			return nil
		},
	}
	install.Flags().StringVar(&certFile, "cert", "", "PEM file with the certificate and the intermediates")
	install.Flags().StringVar(&keyFile, "key", "", "PEM file with the private key (no password)")

	reset := &cobra.Command{
		Use:     "reset",
		Short:   "Go back to the gateway's own self-signed certificate",
		Long:    `Removes the installed certificate and its key; the gateway presents its own self-signed certificate again. It needs a token with the scope full.`,
		Example: `  chaosctl certificate reset`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cl, err := a.client()
			if err != nil {
				return err
			}
			var info model.CertificateInfo
			if _, err := cl.call(cmd.Context(), http.MethodDelete, "/system/certificate", nil, nil, &info); err != nil {
				return apiFail(err)
			}
			if !a.asJSON {
				fmt.Fprintln(a.env.Stdout, "reset:")
			}
			a.printCertificate(info)
			return nil
		},
	}

	cmd.AddCommand(show, install, reset)
	return cmd
}

// printCertificate prints what the gateway says about a certificate.
func (a *app) printCertificate(c model.CertificateInfo) {
	if a.asJSON {
		a.printJSON(c)
		return
	}
	str := func(p *string) string {
		if p == nil {
			return "-"
		}
		return printable(*p)
	}
	list := func(p *[]string) string {
		if p == nil || len(*p) == 0 {
			return "-"
		}
		s := ""
		for i, v := range *p {
			if i > 0 {
				s += ", "
			}
			s += printable(v)
		}
		return s
	}
	when := func(t *time.Time) string {
		if t == nil {
			return "-"
		}
		return t.UTC().Format(time.RFC3339)
	}
	source := "-"
	if c.Source != nil {
		source = string(*c.Source)
	}
	fmt.Fprintf(a.env.Stdout, "source:      %s\nsubject:     %s\nissuer:      %s\nvalid:       %s to %s\nDNS names:   %s\nIPs:         %s\nSHA-256:     %s\n",
		source, str(c.Subject), str(c.Issuer), when(c.NotBefore), when(c.NotAfter), list(c.DnsNames), list(c.IpAddresses), str(c.FingerprintSha256))
}
