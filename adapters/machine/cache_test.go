package machine

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func envMap(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

// TestLoadCache pins CFG11: the first Load writes machine.json, later ones
// read it; a broken file is recomputed; a runtime key wins uncached.
func TestLoadCache(t *testing.T) {
	home := t.TempDir()
	a, src, err := Load(home, envMap(nil))
	if err != nil {
		t.Fatal(err)
	}
	if src != SourceOS && src != SourceRandom {
		t.Fatalf("source %q", src)
	}
	b, _ := os.ReadFile(filepath.Clean(filepath.Join(home, CacheFile)))
	if !strings.Contains(string(b), a.String()) {
		t.Fatalf("cache %s lacks %s", b, a)
	}
	again, _, _ := Load(home, envMap(nil))
	if again != a {
		t.Fatal("second Load gave another id")
	}
	_ = os.WriteFile(filepath.Join(home, CacheFile), []byte("{not json"), 0o600)
	if fixed, _, ferr := Load(home, envMap(nil)); ferr != nil || (src == SourceOS && fixed != a) {
		t.Fatalf("broken cache: %v %v", fixed, ferr)
	}
	ci, src, err := Load(home, envMap(map[string]string{"GITHUB_ACTIONS": "true", "GITHUB_REPOSITORY": "acme/x"}))
	want, _ := Derive("ci:github_actions/acme/x")
	if err != nil || src != SourceRuntime || ci != want {
		t.Fatalf("runtime key: %v %v %v", ci, src, err)
	}
	// A random cached id is kept as is (a container without an OS id).
	rnd := "mch_" + strings.Repeat("ab", 32)
	_ = os.WriteFile(filepath.Join(home, CacheFile), []byte(`{"id":"`+rnd+`","id_source":"random"}`), 0o600)
	if got, src, _ := Load(home, envMap(nil)); got.String() != rnd || src != SourceRandom {
		t.Fatalf("random kept: %v %v", got, src)
	}
}

type fakeFS struct {
	files  map[string]string
	opened []string
}

func (f *fakeFS) ReadFile(n string) ([]byte, error) {
	f.opened = append(f.opened, n)
	if s, ok := f.files[n]; ok {
		return []byte(s), nil
	}
	return nil, errors.New("absent")
}
func (f *fakeFS) Exists(n string) bool { _, ok := f.files[n]; return ok }

// TestDetectRuntime runs one case per runtime with fake env and files.
func TestDetectRuntime(t *testing.T) {
	cases := []struct {
		env   map[string]string
		files map[string]string
		want  Runtime
	}{
		{map[string]string{"AWS_LAMBDA_FUNCTION_NAME": "hub_sync", "AWS_REGION": "eu-west-1"}, nil, Runtime{Type: RuntimeLambda, Function: "hub_sync", Region: "eu-west-1"}},
		{map[string]string{"ECS_CONTAINER_METADATA_URI_V4": "http://x", "AWS_EXECUTION_ENV": "AWS_ECS_FARGATE"}, nil, Runtime{Type: RuntimeECS, Launch: "AWS_ECS_FARGATE"}},
		{map[string]string{"K_SERVICE": "api", "K_REVISION": "api-1"}, nil, Runtime{Type: RuntimeCloudRun, Service: "api", Revision: "api-1"}},
		{map[string]string{"KUBERNETES_SERVICE_HOST": "10.0.0.1", "KUBERNETES_SERVICE_PORT": "443", "HOSTNAME": "pod-1"},
			map[string]string{k8sNamespaceFile: "prod\n", "/var/run/secrets/kubernetes.io/serviceaccount/token": "x", "/.dockerenv": ""},
			Runtime{Type: RuntimeKubernetes, Pod: "pod-1", Namespace: "prod", Inside: RuntimeDocker}},
		{map[string]string{"CODESPACES": "true", "CODESPACE_NAME": "cs"}, nil, Runtime{Type: RuntimeCodespaces, Name: "cs"}},
		{map[string]string{"GITPOD_WORKSPACE_ID": "gp"}, nil, Runtime{Type: RuntimeGitpod, Name: "gp"}},
		{map[string]string{"REMOTE_CONTAINERS": "true"}, nil, Runtime{Type: RuntimeDevcontainer}},
		{map[string]string{"GITLAB_CI": "true", "CI_PROJECT_PATH": "acme/x"}, nil, Runtime{Type: RuntimeCI, Provider: "gitlab_ci", Repo: "acme/x"}},
		{map[string]string{"WSL_DISTRO_NAME": "Ubuntu"}, nil, Runtime{Type: RuntimeWSL2, Distro: "Ubuntu"}},
		{nil, map[string]string{"/run/.containerenv": ""}, Runtime{Type: RuntimeDocker}},
		{nil, nil, Runtime{Type: RuntimeBare}},
	}
	for _, c := range cases {
		fs := &fakeFS{files: c.files}
		if got := DetectRuntime(envMap(c.env), fs); got != c.want {
			t.Errorf("%v: got %+v want %+v", c.env, got, c.want)
		}
		for _, o := range fs.opened {
			if strings.HasSuffix(o, "/token") {
				t.Fatal("the service-account token was opened")
			}
		}
	}
	if k := RuntimeKey(envMap(map[string]string{"AWS_LAMBDA_FUNCTION_NAME": "f", "AWS_REGION": "r"})); k != "lambda:f:r" {
		t.Fatalf("lambda key %q", k)
	}
}

// TestCollectReal is the real-machine smoke: no error, the basics filled.
func TestCollectReal(t *testing.T) {
	p := Collect(os.Getenv, OSFiles{})
	if p.Arch == "" || p.CPU.Cores == 0 || p.Runtime.Type == "" {
		t.Fatalf("profile %+v", p)
	}
}
