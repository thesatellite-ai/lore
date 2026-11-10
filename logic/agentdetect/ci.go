package agentdetect

import "strings"

// CI is the CI run lore is inside (section 6.5, decision OQ13): the
// provider slug, the repo that names the CI actor ci:<provider>/<repo>,
// and the per-run facts that go on the session row and the Lore-CI-Run
// trailer (never into a client repo, never into the machine profile).
type CI struct {
	Provider string `json:"provider"`
	Repo     string `json:"repo,omitempty"`
	RunID    string `json:"run_id,omitempty"`
	Attempt  string `json:"attempt,omitempty"`
	Workflow string `json:"workflow,omitempty"`
	Job      string `json:"job,omitempty"`
	Commit   string `json:"commit,omitempty"`
}

// ActorKey is the CI actor's stable key, ci:<provider>/<repo> (OQ13); a
// provider without a repo variable gives ci:<provider>.
func (c CI) ActorKey() string {
	if c.Repo == "" {
		return "ci:" + c.Provider
	}
	return "ci:" + c.Provider + "/" + c.Repo
}

// Trailer is the Lore-CI-Run trailer value (6.5): "github_actions run 1
// attempt 1, workflow ci, job lore-check, commit 4e6ed1c"; empty parts
// are left out.
func (c CI) Trailer() string {
	parts := []string{c.Provider}
	if c.RunID != "" {
		head := "run " + c.RunID
		if c.Attempt != "" {
			head += " attempt " + c.Attempt
		}
		parts[0] += " " + head
	}
	for _, kv := range [][2]string{{"workflow", c.Workflow}, {"job", c.Job}, {"commit", shortSHA(c.Commit)}} {
		if kv[1] != "" {
			parts = append(parts, kv[0]+" "+kv[1])
		}
	}
	return strings.Join(parts, ", ")
}

// shortSHAHex is how much of a commit the trailer shows (git short hash).
const shortSHAHex = 7

func shortSHA(s string) string {
	if len(s) > shortSHAHex {
		return s[:shortSHAHex]
	}
	return s
}

// ciProvider is one built-in CI provider (6.8 runtime table, 6.3).
type ciProvider struct {
	Slug        string
	Marker      Signal
	RepoEnv     []string // joined with "/" (CircleCI user + repo)
	RunEnv      []string // joined with "/"
	AttemptEnv  string
	WorkflowEnv string
	JobEnv      string
	CommitEnv   string
}

// ciProviders are checked in order; each name is from the provider's own
// predefined-variables page (6.8 runtime table).
func ciProviders() []ciProvider {
	return []ciProvider{
		{Slug: "github_actions", Marker: eq("GITHUB_ACTIONS", "true"), RepoEnv: []string{"GITHUB_REPOSITORY"}, RunEnv: []string{"GITHUB_RUN_ID"},
			AttemptEnv: "GITHUB_RUN_ATTEMPT", WorkflowEnv: "GITHUB_WORKFLOW", JobEnv: "GITHUB_JOB", CommitEnv: "GITHUB_SHA"},
		{Slug: "gitlab_ci", Marker: eq("GITLAB_CI", "true"), RepoEnv: []string{"CI_PROJECT_PATH"}, RunEnv: []string{"CI_PIPELINE_ID"},
			JobEnv: "CI_JOB_NAME", CommitEnv: "CI_COMMIT_SHA"},
		{Slug: "buildkite", Marker: eq("BUILDKITE", "true"), RepoEnv: []string{"BUILDKITE_REPO"}, RunEnv: []string{"BUILDKITE_BUILD_ID"}},
		{Slug: "circleci", Marker: eq("CIRCLECI", "true"), RepoEnv: []string{"CIRCLE_PROJECT_USERNAME", "CIRCLE_PROJECT_REPONAME"},
			RunEnv: []string{"CIRCLE_WORKFLOW_ID", "CIRCLE_BUILD_NUM"}},
		{Slug: "jenkins", Marker: p("JENKINS_URL"), RepoEnv: []string{"JOB_NAME"}, RunEnv: []string{"BUILD_TAG"}},
		{Slug: "azure_pipelines", Marker: eq("TF_BUILD", "True"), RepoEnv: []string{"BUILD_REPOSITORY_NAME"}, RunEnv: []string{"BUILD_BUILDID"}},
		{Slug: "bitbucket", Marker: p("BITBUCKET_BUILD_NUMBER"), RepoEnv: []string{"BITBUCKET_REPO_FULL_NAME"}, RunEnv: []string{"BITBUCKET_BUILD_NUMBER"}},
	}
}

// DetectCI returns the CI provider whose marker is set, or nil.
func DetectCI(getenv func(string) string) *CI {
	for _, c := range ciProviders() {
		if !c.Marker.matches(getenv) {
			continue
		}
		get := func(n string) string {
			if n == "" {
				return ""
			}
			return clean(getenv(n))
		}
		join := func(names []string) string {
			var vs []string
			for _, n := range names {
				if v := get(n); v != "" {
					vs = append(vs, v)
				}
			}
			return strings.Join(vs, "/")
		}
		return &CI{Provider: c.Slug, Repo: join(c.RepoEnv), RunID: join(c.RunEnv), Attempt: get(c.AttemptEnv),
			Workflow: get(c.WorkflowEnv), Job: get(c.JobEnv), Commit: get(c.CommitEnv)}
	}
	return nil
}
