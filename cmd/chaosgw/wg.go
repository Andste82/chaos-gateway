package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Andste82/chaos-gateway/internal/linkexport"

	"github.com/Andste82/chaos-gateway/internal/model"
	"github.com/Andste82/chaos-gateway/internal/secrets"
	"github.com/Andste82/chaos-gateway/internal/store"
	"github.com/Andste82/chaos-gateway/internal/wireguard"
)

// runWG is `chaosgw wg ...`: today `export`, which renders the configuration of a client or the
// remote side of a link from the active revision and the secrets store (plan §2.2.1).
func runWG(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "export" {
		fmt.Fprintln(stderr, "usage: chaosgw wg export --state-dir <dir> --secrets-dir <dir> --network <name> (--client <name> | --all | --link) [--format conf|png|svg|zip] [--out <file>]")
		return 2
	}
	fs := flag.NewFlagSet("chaosgw wg export", flag.ContinueOnError)
	fs.SetOutput(stderr)
	stateDir := fs.String("state-dir", "", "the store with the revisions")
	secretsDir := fs.String("secrets-dir", "", "the secrets store")
	network := fs.String("network", "", "name or UUID of the WireGuard network")
	client := fs.String("client", "", "name or UUID of the client")
	all := fs.Bool("all", false, "every client of the hub, as a zip")
	link := fs.Bool("link", false, "the remote side of the link (the network must be a link)")
	format := fs.String("format", "conf", "conf, png (QR code), svg (QR code) or zip")
	out := fs.String("out", "", "write to this file instead of standard output")
	uplink := fs.String("uplink-address", "", "the gateway's address for clients, when the network names no public endpoint")
	birdSnippet := fs.Bool("bird", false, "with --link: the BIRD configuration of the remote side instead of the WireGuard one")
	remoteIface := fs.String("remote-interface", "wg0", "with --bird: the name of the tunnel interface on the remote machine")
	keep := fs.Bool("keep-key", false, "do not delete the private key of an `export_once` client")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if *stateDir == "" || *secretsDir == "" || *network == "" || fs.NArg() != 0 || (*client == "" && !*all && !*link) {
		fmt.Fprintln(stderr, "chaosgw wg export: --state-dir, --secrets-dir, --network and one of --client, --all, --link are required")
		return 2
	}
	st, err := store.Open(*stateDir)
	if err != nil {
		fmt.Fprintf(stderr, "chaosgw wg export: %v\n", err)
		return 1
	}
	defer func() { _ = st.Close() }()
	_, cfg, err := st.Active()
	if err != nil {
		fmt.Fprintf(stderr, "chaosgw wg export: no active revision: %v\n", err)
		return 1
	}
	sec, err := secrets.Open(*secretsDir)
	if err != nil {
		fmt.Fprintf(stderr, "chaosgw wg export: %v\n", err)
		return 1
	}
	in := wireguard.ExportInput{Config: cfg, Secrets: sec, UplinkAddress: *uplink}
	netID, hub, err := findWG(cfg, *network)
	if err != nil {
		fmt.Fprintf(stderr, "chaosgw wg export: %v\n", err)
		return 1
	}

	if *birdSnippet {
		if !*link {
			fmt.Fprintln(stderr, "chaosgw wg export: --bird needs --link")
			return 2
		}
		text, err := linkexport.RemoteBird(cfg, sec, netID, *remoteIface)
		if err != nil {
			fmt.Fprintf(stderr, "chaosgw wg export: %v\n", err)
			return 1
		}
		if *out != "" {
			if err := writeExport(*out, []byte(text), 0o644); err != nil {
				fmt.Fprintf(stderr, "chaosgw wg export: %v\n", err)
				return 1
			}
			return 0
		}
		_, _ = io.WriteString(stdout, text)
		return 0
	}

	var exports []wireguard.Export
	var consumed []string
	switch {
	case *link:
		e, err := wireguard.LinkRemoteConfig(in, netID)
		if err != nil {
			fmt.Fprintf(stderr, "chaosgw wg export: %v\n", err)
			return 1
		}
		exports = append(exports, e)
	case *all:
		if hub.Clients == nil {
			fmt.Fprintf(stderr, "chaosgw wg export: %q has no clients\n", hub.Name)
			return 1
		}
		ids := make([]string, 0, len(*hub.Clients))
		for id := range *hub.Clients {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			e, err := wireguard.ClientConfig(in, netID, id)
			if err != nil {
				fmt.Fprintf(stderr, "chaosgw wg export: %v\n", err)
				return 1
			}
			exports = append(exports, e)
			consumed = append(consumed, id)
		}
		*format = "zip"
	default:
		id, err := findClient(hub, *client)
		if err != nil {
			fmt.Fprintf(stderr, "chaosgw wg export: %v\n", err)
			return 1
		}
		e, err := wireguard.ClientConfig(in, netID, id)
		if err != nil {
			fmt.Fprintf(stderr, "chaosgw wg export: %v\n", err)
			return 1
		}
		exports = append(exports, e)
		consumed = append(consumed, id)
	}

	var data []byte
	switch *format {
	case "conf":
		data = []byte(exports[0].Conf)
	case "png":
		data, err = wireguard.QRPNG(exports[0].Conf, 512)
	case "svg":
		var s string
		s, err = wireguard.QRSVG(exports[0].Conf)
		data = []byte(s)
	case "zip":
		data, err = wireguard.Zip(exports)
	default:
		fmt.Fprintf(stderr, "chaosgw wg export: unknown format %q\n", *format)
		return 2
	}
	if err != nil {
		fmt.Fprintf(stderr, "chaosgw wg export: %v\n", err)
		return 1
	}
	secret := false
	for _, e := range exports {
		secret = secret || e.Secret()
	}
	if *out != "" {
		mode := os.FileMode(0o644)
		if secret {
			mode = 0o600
		}
		if err := writeExport(*out, data, mode); err != nil {
			fmt.Fprintf(stderr, "chaosgw wg export: %v\n", err)
			return 1
		}
	} else if _, err := stdout.Write(data); err != nil {
		return 1
	}
	if secret {
		fmt.Fprintln(stderr, "warning: the export contains private keys; keep it secret")
	}
	if !*keep {
		for _, id := range consumed {
			if done, err := wireguard.ConsumePrivateKey(in, netID, id); err != nil {
				fmt.Fprintf(stderr, "chaosgw wg export: %v\n", err)
				return 1
			} else if done {
				fmt.Fprintln(stderr, "the private key was deleted after this export (export once)")
			}
		}
	}
	return 0
}

func findWG(cfg *model.Configuration, ref string) (string, model.WireGuardNetwork, error) {
	if cfg.Networks != nil {
		for id, n := range *cfg.Networks {
			wg, err := n.AsWireGuardNetwork()
			if err != nil || wg.Type != model.WireGuardNetworkTypeWireguard {
				continue
			}
			if id == ref || strings.EqualFold(wg.Name, ref) {
				return id, wg, nil
			}
		}
	}
	return "", model.WireGuardNetwork{}, fmt.Errorf("no WireGuard network %q", ref)
}

func findClient(hub model.WireGuardNetwork, ref string) (string, error) {
	if hub.Clients != nil {
		for id, c := range *hub.Clients {
			if id == ref || strings.EqualFold(c.Name, ref) {
				return id, nil
			}
		}
	}
	return "", errors.New("no client " + ref)
}

// writeExport writes a file with exactly the given mode, also over an existing file: the file is
// created next to its destination with that mode and moved into place, so a key never lands in a
// file that is readable by others, not even for a moment.
func writeExport(path string, data []byte, mode os.FileMode) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".export-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer func() { _ = os.Remove(tmp) }()
	if err := f.Chmod(mode); err != nil {
		_ = f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
