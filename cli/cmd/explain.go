package cmd

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

// explainTopic is a short, on-demand knowledge-base entry. Unlike the old
// `kindling intel` files (which pushed a static essay into every repo, for
// every agent, on every session), this content ships inside the binary and
// is pulled on demand via `kindling explain <topic>` — always in sync with
// the installed version, no repo footprint, no per-agent duplication.
type explainTopic struct {
	Name    string
	Summary string // one-liner shown in the topic list
	Body    string
}

var explainTopics = []explainTopic{
	{
		Name:    "overview",
		Summary: "What kindling is and how it fits together",
		Body: `kindling turns your laptop into a personal CI/CD environment: a local
Kind cluster runs an operator, an in-cluster image registry, and
self-hosted CI runners, so pushes to your repo build and deploy the
same way a real staging environment would — without needing a real
staging environment.

Core idioms (prefer these over generic Kubernetes/Docker commands):
  - Deploy with 'kindling deploy', not 'kubectl apply' on raw manifests.
  - Builds run via Kaniko inside the CI runner, not 'docker build'.
  - Dependencies (Postgres, Redis, ...) go in a DSE YAML's
    spec.dependencies[], not Docker Compose or a Helm chart you write.
  - Check state with 'kindling status' / 'kindling logs', not raw kubectl.

Run 'kindling explain <topic>' for more (see 'kindling explain' for the
topic list).`,
	},
	{
		Name:    "debugging",
		Summary: "The fastest loop for fixing a running service",
		Body: `Prefer 'kindling sync' for live debugging over rebuilding images.

  kindling sync -d <deployment>

This live-syncs local source files straight into the running pod
(and, for compiled languages, recompiles + hot-swaps the binary)
instead of rebuilding a Docker image and redeploying — the rebuild
loop is minutes, sync is seconds.

Use 'kindling debug -d <deployment>' to attach a real debugger to a
running service instead of relying on print statements/logs.

Reach for a full rebuild ('kindling load -s <svc>') only when the
change affects the Dockerfile itself (new deps, base image, etc.) —
not for routine source edits.

To see why something is actually broken, in order of cost:
  1. kindling status         — quick health overview
  2. kindling logs           — controller logs
  3. kubectl get pods / describe / logs — pod-level detail
An "exec format error" in pod logs means an architecture mismatch
(image built for the wrong CPU arch) — check 'kubectl get nodes -o
wide' vs. the image's actual arch, not the Dockerfile.`,
	},
	{
		Name:    "dependencies",
		Summary: "How Postgres/Redis/etc. get wired into a service",
		Body: `Declare dependencies in a DSE (DevStagingEnvironment) YAML's
spec.dependencies[] — not in Docker Compose or a hand-written Helm
chart. The operator provisions them and auto-injects a connection URL
as an environment variable:

  postgres       -> DATABASE_URL
  mysql          -> DATABASE_URL
  redis          -> REDIS_URL
  mongodb        -> MONGO_URL
  rabbitmq       -> AMQP_URL
  minio          -> S3_ENDPOINT
  elasticsearch  -> ELASTICSEARCH_URL
  kafka          -> KAFKA_BROKER_URL
  nats           -> NATS_URL
  memcached      -> MEMCACHED_URL

Do not duplicate these in spec.env[] — they're already injected.`,
	},
	{
		Name:    "builds",
		Summary: "Kaniko build constraints vs. plain docker build",
		Body: `CI builds run via Kaniko (in a sidecar next to the CI runner), not
Docker/BuildKit. This matters when writing or editing a Dockerfile:

  - No BuildKit platform ARGs (TARGETARCH, BUILDPLATFORM, ...) — empty.
  - No .git directory in the build context — Go builds need
    '-buildvcs=false' or they'll fail trying to read VCS info.
  - Poetry installs need '--no-root'.
  - npm needs a cache redirect: ENV npm_config_cache=/tmp/.npm
  - 'RUN --mount=type=cache' is silently ignored (safe, just no cache).

Builds always target linux/amd64 regardless of the host machine's
architecture (Kaniko is invoked with --custom-platform=linux/amd64),
so local Apple Silicon vs. CI/prod amd64 mismatches shouldn't occur
through the normal 'kindling load' / CI pipeline — they generally
mean an image was built or pushed outside that pipeline.`,
	},
	{
		Name:    "secrets",
		Summary: "How credentials flow from CLI to running pods",
		Body: `kindling secrets set NAME VALUE
  -> creates a Kubernetes Secret
  -> referenced via secretKeyRef in the generated CI workflow
  -> injected into the container as an env var

Never hardcode secrets in DSE YAML, workflow files, or .env files
checked into the repo — always go through 'kindling secrets set'.

For plain (non-secret) configuration, use 'kindling env set KEY=VALUE'
or spec.env[] in the DSE YAML instead.`,
	},
	{
		Name:    "production",
		Summary: "Graduating from local Kind to a real cluster",
		Body: `'kindling snapshot -r <registry> --deploy --context <prod-context>'
exports the current DSEs as a Helm chart (or Kustomize overlay),
pushes images to a real registry, and deploys to a production
cluster — it's the bridge from the local Kind loop to a real target.

'kindling production tls --context <ctx> --domain <domain> --email
<email>' sets up cert-manager + Let's Encrypt for that cluster.

Image pushes fail hard (not silently) if any service can't be pushed
— don't assume a partially-successful snapshot deploy is safe to
retry blindly; check which service failed and why first.`,
	},
}

var explainCmd = &cobra.Command{
	Use:   "explain [topic]",
	Short: "On-demand kindling concepts and workflow guidance",
	Long: `Prints short, current guidance on how kindling works — pulled on demand
instead of injected into every coding-agent session.

Run with no arguments to list available topics.`,
	Args: cobra.MaximumNArgs(1),
	RunE: runExplain,
}

func init() {
	rootCmd.AddCommand(explainCmd)
}

func runExplain(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		names := make([]string, 0, len(explainTopics))
		byName := make(map[string]explainTopic, len(explainTopics))
		for _, t := range explainTopics {
			names = append(names, t.Name)
			byName[t.Name] = t
		}
		sort.Strings(names)

		fmt.Println("Topics (run: kindling explain <topic>):")
		fmt.Println()
		for _, n := range names {
			fmt.Printf("  %-14s %s\n", n, byName[n].Summary)
		}
		return nil
	}

	query := strings.ToLower(strings.TrimSpace(args[0]))
	for _, t := range explainTopics {
		if t.Name == query {
			fmt.Println(strings.TrimSpace(t.Body))
			return nil
		}
	}

	var names []string
	for _, t := range explainTopics {
		names = append(names, t.Name)
	}
	return fmt.Errorf("unknown topic %q — available topics: %s", args[0], strings.Join(names, ", "))
}
