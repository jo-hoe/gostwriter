package config

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestHelmChartDefaultConfigParses renders the Helm chart with its default
// values and asserts the generated config.yaml parses and validates through the
// real Load path.
//
// Regression guard: the chart once emitted the LLM timeout fields as a bare
// YAML int `0` (the templates interpolated the "0" default without | quote),
// which the loader rejected with
//
//	cannot unmarshal !!int `0` into time.Duration
//
// crash-looping the pod on a default install. The fix quotes the timeout values
// in templates/{secret-config,configmap}.yaml so "0" survives as a string and
// resolves to the client's 5m default. This test renders both the Secret-backed
// (default) and ConfigMap-backed paths and runs each through Load().
func TestHelmChartDefaultConfigParses(t *testing.T) {
	helm, err := exec.LookPath("helm")
	if err != nil {
		t.Skip("helm not installed; skipping chart render regression test")
	}

	chartDir, err := filepath.Abs(filepath.Join("..", "..", "charts", "gostwriter"))
	if err != nil {
		t.Fatalf("resolve chart dir: %v", err)
	}

	// Minimal values that satisfy config validation (a target + token) while
	// leaving every LLM timeout at its "0" default — the field that regressed.
	common := []string{
		"template", chartDir,
		"--set", "target.github.enabled=true",
		"--set", "target.github.repositoryOwner=o",
		"--set", "target.github.repositoryName=r",
		"--set", "target.github.auth.token=dummy",
	}

	cases := []struct {
		name string
		args []string
	}{
		{"secret-default", nil},
		{"configmap", []string{"--set", "configAsSecret=false"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			// Point storageDir at a writable temp dir: Load() calls MkdirAll on
			// it, and the chart default (/app) is not writable under the CI
			// test user. This test guards timeout YAML parsing, not storage.
			args := append(append([]string{}, common...), tc.args...)
			args = append(args, "--set", "server.storageDir="+filepath.ToSlash(dir))
			// #nosec G204 -- helm is resolved via LookPath and all args are
			// test-controlled (constants + a t.TempDir path); no user input
			// reaches this command.
			out, err := exec.Command(helm, args...).CombinedOutput()
			if err != nil {
				t.Fatalf("helm template failed: %v\n%s", err, out)
			}

			configYAML := extractRenderedConfig(t, out)
			tmp := filepath.Join(dir, "config.yaml")
			if err := os.WriteFile(tmp, []byte(configYAML), 0o600); err != nil {
				t.Fatalf("write rendered config: %v", err)
			}

			cfg, err := Load(tmp)
			if err != nil {
				t.Fatalf("rendered chart config must parse+validate, got: %v\n--- config.yaml ---\n%s", err, configYAML)
			}
			if cfg.LLM.OpenAI.Timeout != 0 || cfg.LLM.Anthropic.Timeout != 0 {
				t.Fatalf("expected default LLM timeouts to parse to 0, got openai=%v anthropic=%v", cfg.LLM.OpenAI.Timeout, cfg.LLM.Anthropic.Timeout)
			}
		})
	}
}

// extractRenderedConfig pulls the config.yaml payload out of the rendered
// Secret (stringData) or ConfigMap (data) in a multi-document helm render.
func extractRenderedConfig(t *testing.T, rendered []byte) string {
	t.Helper()
	dec := yaml.NewDecoder(strings.NewReader(string(rendered)))
	for {
		var doc map[string]any
		if err := dec.Decode(&doc); err != nil {
			break
		}
		if doc == nil {
			continue
		}
		for _, key := range []string{"stringData", "data"} {
			section, ok := doc[key].(map[string]any)
			if !ok {
				continue
			}
			if cfg, ok := section["config.yaml"].(string); ok {
				return cfg
			}
		}
	}
	t.Fatalf("no config.yaml found in rendered chart output:\n%s", rendered)
	return ""
}
