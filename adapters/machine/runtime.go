package machine

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/thesatellite-ai/lore/logic/agentdetect"
)

// Files is the file access runtime and profile detection make (a seam:
// tests pass a fake, never the real file system).
type Files interface {
	ReadFile(name string) ([]byte, error)
	Exists(name string) bool
}

// OSFiles reads the real file system.
type OSFiles struct{}

// ReadFile reads name.
func (OSFiles) ReadFile(name string) ([]byte, error) { return os.ReadFile(filepath.Clean(name)) }

// Exists reports whether name exists.
func (OSFiles) Exists(name string) bool { _, err := os.Stat(name); return err == nil }

// Runtime types (6.8 runtime table), checked in this order.
const (
	RuntimeLambda       = "aws_lambda"
	RuntimeECS          = "aws_ecs"
	RuntimeCloudRun     = "gcp_cloud_run"
	RuntimeKubernetes   = "kubernetes"
	RuntimeCodespaces   = "codespaces"
	RuntimeGitpod       = "gitpod"
	RuntimeDevcontainer = "devcontainer"
	RuntimeCI           = "ci"
	RuntimeWSL2         = "wsl2"
	RuntimeDocker       = "docker"
	RuntimeBare         = "bare"
)

// Runtime is the profile's runtime block: the type, the container it sits
// in, and its identity (only the fields its type has).
type Runtime struct {
	Type      string `json:"type"`
	Inside    string `json:"inside,omitempty"`
	Function  string `json:"function,omitempty"`
	Version   string `json:"version,omitempty"`
	Region    string `json:"region,omitempty"`
	Launch    string `json:"launch_type,omitempty"`
	Service   string `json:"service,omitempty"`
	Revision  string `json:"revision,omitempty"`
	Job       string `json:"job,omitempty"`
	Pod       string `json:"pod,omitempty"`
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name,omitempty"`
	Repo      string `json:"repo,omitempty"`
	Provider  string `json:"provider,omitempty"`
	Distro    string `json:"distro,omitempty"`
	Container string `json:"container,omitempty"`
}

// k8sNamespaceFile is the only service-account file read (never token).
const k8sNamespaceFile = "/var/run/secrets/kubernetes.io/serviceaccount/namespace"

// containerIDLen is the short container id shown (docker ps form).
const containerIDLen = 12

// DetectRuntime runs the 6.8 runtime table: env vars and local files only,
// no network call, no subprocess.
func DetectRuntime(getenv func(string) string, fsys Files) Runtime {
	inside := ""
	if isDocker(fsys, getenv) {
		inside = RuntimeDocker
	}
	r := func(rt Runtime) Runtime {
		if rt.Type != RuntimeDocker {
			rt.Inside = inside
		}
		return rt
	}
	switch {
	case getenv("AWS_LAMBDA_FUNCTION_NAME") != "" || strings.HasPrefix(getenv("AWS_EXECUTION_ENV"), "AWS_Lambda_"):
		return r(Runtime{Type: RuntimeLambda, Function: getenv("AWS_LAMBDA_FUNCTION_NAME"), Version: getenv("AWS_LAMBDA_FUNCTION_VERSION"), Region: getenv("AWS_REGION")})
	case getenv("ECS_CONTAINER_METADATA_URI_V4") != "" || getenv("ECS_CONTAINER_METADATA_URI") != "":
		region := getenv("AWS_REGION")
		if region == "" {
			region = getenv("AWS_DEFAULT_REGION")
		}
		return r(Runtime{Type: RuntimeECS, Launch: getenv("AWS_EXECUTION_ENV"), Region: region})
	case getenv("K_SERVICE") != "":
		return r(Runtime{Type: RuntimeCloudRun, Service: getenv("K_SERVICE"), Revision: getenv("K_REVISION")})
	case getenv("CLOUD_RUN_JOB") != "":
		return r(Runtime{Type: RuntimeCloudRun, Job: getenv("CLOUD_RUN_JOB")})
	case getenv("KUBERNETES_SERVICE_HOST") != "" && getenv("KUBERNETES_SERVICE_PORT") != "":
		ns := ""
		if b, err := fsys.ReadFile(k8sNamespaceFile); err == nil {
			ns = strings.TrimSpace(string(b))
		}
		return r(Runtime{Type: RuntimeKubernetes, Pod: getenv("HOSTNAME"), Namespace: ns})
	case getenv("CODESPACES") == "true":
		return r(Runtime{Type: RuntimeCodespaces, Name: getenv("CODESPACE_NAME"), Repo: getenv("GITHUB_REPOSITORY")})
	case getenv("GITPOD_WORKSPACE_ID") != "":
		return r(Runtime{Type: RuntimeGitpod, Name: getenv("GITPOD_WORKSPACE_ID")})
	case getenv("REMOTE_CONTAINERS") == "true":
		return r(Runtime{Type: RuntimeDevcontainer})
	}
	if ci := agentdetect.DetectCI(getenv); ci != nil {
		return r(Runtime{Type: RuntimeCI, Provider: ci.Provider, Repo: ci.Repo})
	}
	if d := getenv("WSL_DISTRO_NAME"); d != "" {
		return r(Runtime{Type: RuntimeWSL2, Distro: d})
	}
	if b, err := fsys.ReadFile("/proc/sys/kernel/osrelease"); err == nil && strings.Contains(string(b), "microsoft-standard-WSL2") {
		return r(Runtime{Type: RuntimeWSL2})
	}
	if inside != "" {
		return Runtime{Type: RuntimeDocker, Container: containerID(fsys)}
	}
	return Runtime{Type: RuntimeBare}
}

// isDocker is the docker / podman file and cgroup test.
func isDocker(fsys Files, getenv func(string) string) bool {
	if fsys.Exists("/.dockerenv") || fsys.Exists("/run/.containerenv") || getenv("container") == "podman" {
		return true
	}
	b, err := fsys.ReadFile("/proc/1/cgroup")
	return err == nil && (strings.Contains(string(b), "docker") || strings.Contains(string(b), "containerd"))
}

// containerID is the first 12 hex of a 64-hex docker id in mountinfo.
func containerID(fsys Files) string {
	b, err := fsys.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return ""
	}
	s := string(b)
	const marker = "/containers/"
	if i := strings.Index(s, marker); i >= 0 {
		rest := s[i+len(marker):]
		if len(rest) >= containerIDLen {
			return rest[:containerIDLen]
		}
	}
	return ""
}

// RuntimeKey is the runtime-derived key of an ephemeral runtime (6.8):
// ci:<provider>/<repo>, lambda:<function>:<region>, cloud_run:<service or
// job>; "" for a lasting machine. aws_ecs has no key: its cluster and
// family are only on the metadata endpoint, a network call.
func RuntimeKey(getenv func(string) string) string {
	if fn := getenv("AWS_LAMBDA_FUNCTION_NAME"); fn != "" {
		return "lambda:" + fn + ":" + getenv("AWS_REGION")
	}
	if s := getenv("K_SERVICE"); s != "" {
		return "cloud_run:" + s
	}
	if j := getenv("CLOUD_RUN_JOB"); j != "" {
		return "cloud_run:" + j
	}
	if ci := agentdetect.DetectCI(getenv); ci != nil {
		return ci.ActorKey()
	}
	return ""
}

// Ephemeral reports whether the runtime starts a new machine every run
// (the hostname is then a random container name and is not shown).
func Ephemeral(rt Runtime) bool {
	switch rt.Type {
	case RuntimeLambda, RuntimeCloudRun, RuntimeCI, RuntimeECS:
		return true
	}
	return false
}
