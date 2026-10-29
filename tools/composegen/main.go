// Command composegen makes deploy/compose.yaml, the one file an administrator runs (plan §3.8, M28), from the compose files
// of the containers in deploy/: compose.executor.yaml, compose.api.yaml, compose.kea.yaml, compose.dns.yaml (the holder of the
// service namespace and the DNS proxy), compose.tls.yaml, compose.bird.yaml, compose.tlsproxy.yaml and compose.teardown.yaml.
// The files stay what the tests of the lower levels start (the executor alone, the executor with BIRD, the proxy of the
// interception alone), so there is one definition of every container; compose.yaml is their union, with three changes that
// only make sense for the whole:
//
//   - a dependency on a service of another file that the single file has (`required: false` in the file, so that it can be
//     started without it) is a plain dependency: the start order of plan §3.8 is checked by Compose;
//   - the interception proxy is under the profile `interception` (optional, plan §3.8): COMPOSE_PROFILES=interception starts it;
//   - the project is named chaosgw, so the volumes and networks do not depend on the name of the directory.
//
// `go run ./tools/composegen` writes the file, `go run ./tools/composegen -check` fails when it is not current (make
// check-generated, and a Go test, run it).
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// files are the compose files of deploy/ in the order their services appear in compose.yaml: the start order, roughly.
var files = []string{
	"compose.executor.yaml",
	"compose.api.yaml",
	"compose.kea.yaml",
	"compose.dns.yaml",
	"compose.tls.yaml",
	"compose.bird.yaml",
	"compose.tlsproxy.yaml",
	"compose.teardown.yaml",
}

const header = `# Chaos Gateway: the containers of one gateway (plan §3.8). Made from the compose files of this directory by tools/composegen
# (go run ./tools/composegen; make check-generated fails when this file is not current): change a container in its own file.
#
#   docker compose up -d                       the gateway (the host setup, deploy/host-setup.sh, comes first: it makes the directories below)
#   COMPOSE_PROFILES=interception docker compose up -d     with the interception proxy
#   docker compose --profile teardown run --rm teardown [--purge]    remove what a killed gateway left in the kernel (and the data)
#
# Start order: exec first (healthy: its socket answers), then api, svcns (the executor attaches the veth pair to its namespace) and
# kea, then the services that join the service namespace (dns, tls, tls-proxy) once the API is healthy, and bird. The persistent
# data is in /var/lib/chaosgw on the host (plan §3.6); the sockets between the containers are in named volumes.
name: chaosgw
`

func main() {
	dir := flag.String("dir", "deploy", "directory of the compose files; compose.yaml is written there")
	check := flag.Bool("check", false, "do not write compose.yaml, fail when it is not what the compose files give")
	flag.Parse()
	out, err := Generate(*dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "composegen:", err)
		os.Exit(1)
	}
	target := filepath.Join(*dir, "compose.yaml")
	if *check {
		have, err := os.ReadFile(target)
		if err != nil || !bytes.Equal(have, out) {
			fmt.Fprintf(os.Stderr, "composegen: %s is not current: run `go run ./tools/composegen`\n", target)
			os.Exit(1)
		}
		return
	}
	if err := os.WriteFile(target, out, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "composegen:", err)
		os.Exit(1)
	}
}

// Generate reads the compose files in dir and returns the text of compose.yaml.
func Generate(dir string) ([]byte, error) {
	services := &yaml.Node{Kind: yaml.MappingNode}
	volumes := &yaml.Node{Kind: yaml.MappingNode}
	for _, f := range files {
		raw, err := os.ReadFile(filepath.Join(dir, f))
		if err != nil {
			return nil, err
		}
		var doc yaml.Node
		if err := yaml.Unmarshal(raw, &doc); err != nil {
			return nil, fmt.Errorf("%s: %w", f, err)
		}
		if len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
			return nil, fmt.Errorf("%s: not a mapping", f)
		}
		first := true
		for i := 0; i+1 < len(doc.Content[0].Content); i += 2 {
			key, val := doc.Content[0].Content[i], doc.Content[0].Content[i+1]
			switch key.Value {
			case "services":
				for j := 0; j+1 < len(val.Content); j += 2 {
					name, svc := val.Content[j], val.Content[j+1]
					if find(services, name.Value) != nil {
						return nil, fmt.Errorf("%s: the service %s is defined twice", f, name.Value)
					}
					if first {
						// the comment above the file's first service says what the file is for
						name.HeadComment = joinComments(key.HeadComment, name.HeadComment)
						first = false
					}
					services.Content = append(services.Content, name, svc)
				}
			case "volumes":
				for j := 0; j+1 < len(val.Content); j += 2 {
					if find(volumes, val.Content[j].Value) == nil {
						volumes.Content = append(volumes.Content, val.Content[j], val.Content[j+1])
					}
				}
			default:
				return nil, fmt.Errorf("%s: the top-level key %q is not one composegen merges", f, key.Value)
			}
		}
	}
	if err := adapt(services); err != nil {
		return nil, err
	}
	root := &yaml.Node{Kind: yaml.MappingNode}
	root.Content = append(root.Content, scalar("services"), services)
	if len(volumes.Content) > 0 {
		root.Content = append(root.Content, scalar("volumes"), volumes)
	}
	var body bytes.Buffer
	enc := yaml.NewEncoder(&body)
	enc.SetIndent(2)
	if err := enc.Encode(root); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return append([]byte(header), body.Bytes()...), nil
}

func joinComments(a, b string) string {
	switch {
	case a == "":
		return b
	case b == "":
		return a
	}
	return a + "\n" + b
}

func scalar(v string) *yaml.Node { return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v} }

// find returns the value of a key of a mapping node, or nil.
func find(m *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

// adapt makes the changes described in the package comment.
func adapt(services *yaml.Node) error {
	for i := 0; i+1 < len(services.Content); i += 2 {
		name, svc := services.Content[i].Value, services.Content[i+1]
		if deps := find(svc, "depends_on"); deps != nil {
			if deps.Kind != yaml.MappingNode {
				return fmt.Errorf("%s: depends_on is not a mapping", name)
			}
			for j := 0; j+1 < len(deps.Content); j += 2 {
				if find(services, deps.Content[j].Value) == nil {
					return fmt.Errorf("%s depends on %s, which no compose file defines", name, deps.Content[j].Value)
				}
				d := deps.Content[j+1]
				for k := 0; k+1 < len(d.Content); k += 2 {
					if d.Content[k].Value == "required" {
						d.Content = append(d.Content[:k], d.Content[k+2:]...)
						break
					}
				}
			}
		}
		if name == "tls-proxy" {
			if find(svc, "profiles") != nil {
				return errors.New("tls-proxy already has a profile")
			}
			svc.Content = append(svc.Content, scalar("profiles"), &yaml.Node{Kind: yaml.SequenceNode, Style: yaml.FlowStyle, Content: []*yaml.Node{scalar("interception")}})
		}
	}
	return nil
}
