package kubernetes

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestRestConfigTuning pins SRC-5: restConfig applies the QPS/Burst/Timeout
// defaults on top of whatever config source it resolves, instead of leaving
// client-go's conservative built-in throttle and unbounded request timeout.
func TestRestConfigTuning(t *testing.T) {
	const kubeconfig = `apiVersion: v1
kind: Config
clusters:
- name: test
  cluster:
    server: https://example.invalid:6443
    insecure-skip-tls-verify: true
contexts:
- name: test
  context:
    cluster: test
    user: test
current-context: test
users:
- name: test
  user:
    token: test-token
`
	dir := t.TempDir()
	path := filepath.Join(dir, "kubeconfig")
	if err := os.WriteFile(path, []byte(kubeconfig), 0o600); err != nil {
		t.Fatal(err)
	}

	c := &Client{cluster: "test", kubeconfigPath: path}
	cfg, err := c.restConfig()
	if err != nil {
		t.Fatalf("restConfig: %v", err)
	}
	if cfg.QPS != clientQPS {
		t.Errorf("QPS = %v, want %v", cfg.QPS, float32(clientQPS))
	}
	if cfg.Burst != clientBurst {
		t.Errorf("Burst = %d, want %d", cfg.Burst, clientBurst)
	}
	if cfg.Timeout != clientTimeout {
		t.Errorf("Timeout = %v, want %v", cfg.Timeout, 30*time.Second)
	}
}
