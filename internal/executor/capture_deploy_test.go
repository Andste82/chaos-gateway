package executor_test

import (
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// composeVolume returns the volume mounted at target in a service of a compose file, and its command.
func composeVolume(t *testing.T, file, svc, target string) (volume string, command []string) {
	t.Helper()
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Services map[string]struct {
			Command []string    `yaml:"command"`
			Volumes []yaml.Node `yaml:"volumes"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	s := doc.Services[svc]
	for _, v := range s.Volumes {
		switch v.Kind {
		case yaml.ScalarNode:
			if src, dst, ok := strings.Cut(v.Value, ":"); ok && strings.HasPrefix(dst, target) {
				return src, s.Command
			}
		case yaml.MappingNode:
			var m struct {
				Source string `yaml:"source"`
				Target string `yaml:"target"`
			}
			if err := v.Decode(&m); err != nil {
				t.Fatal(err)
			}
			if m.Target == target {
				return m.Source, s.Command
			}
		}
	}
	return "", s.Command
}

func flagValue(cmd []string, flag string) string {
	for i := 0; i+1 < len(cmd); i++ {
		if cmd[i] == flag {
			return cmd[i+1]
		}
	}
	return ""
}

// The executor writes the packet files and the API container serves and deletes them: both mount the same volume at
// the path their --captures-dir names (M17), and the image owns the directory by the API's user, so the volume starts
// with an owner that can make the directory of a capture.
func TestTheCapturesVolumeIsTheSameInTheExecutorAndTheAPI(t *testing.T) {
	const dir = "/var/lib/chaosgw/captures"
	execVol, execCmd := composeVolume(t, "../../deploy/compose.executor.yaml", "exec", dir)
	apiVol, apiCmd := composeVolume(t, "../../deploy/compose.api.yaml", "api", dir)
	if execVol == "" || execVol != apiVol {
		t.Errorf("the executor mounts %q at %s, the API %q: it is one volume", execVol, dir, apiVol)
	}
	if flagValue(execCmd, "--captures-dir") != dir || flagValue(apiCmd, "--captures-dir") != dir {
		t.Errorf("--captures-dir: executor %q, API %q, want %s", flagValue(execCmd, "--captures-dir"), flagValue(apiCmd, "--captures-dir"), dir)
	}
	docker, err := os.ReadFile("../../deploy/Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(docker), "chown 65532:65532") || strings.Count(string(docker), dir) < 2 {
		t.Errorf("the image does not make %s and give it to the API's user", dir)
	}
	for _, f := range []string{"../../deploy/compose.executor.yaml", "../../deploy/compose.api.yaml"} {
		raw, _ := os.ReadFile(f)
		if !strings.Contains(string(raw), "\n  chaosgw-captures:") {
			t.Errorf("%s does not declare the volume chaosgw-captures", f)
		}
	}
}
