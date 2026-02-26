# Kindling — Roadmap

**Mission:** Make kindling the default way to build production-ready
multi-agent systems. The product arc: wire up CI in minutes → keep
building with a local development engine that grows with the project.

---

## Phase 1 — Three CI providers, generate parity, rock-solid reliability

Ship CircleCI support, achieve full `kindling generate` parity across
GitHub Actions, GitLab CI, and CircleCI, then fuzz test all three until
they're bulletproof. This is the foundation — every adoption effort
depends on generate working reliably for any repo on any CI platform.

### CircleCI generate + runners

Wire `kindling generate` to emit `.circleci/config.yml` using the same
AI-powered repo scanning pipeline as GitHub/GitLab. The provider
abstraction on `provider-abstraction` already has the interfaces — this
is implementation.

- [ ] CircleCI `WorkflowGenerator` — system prompt, example configs, YAML output
- [ ] CircleCI runner registration in Kind cluster (self-hosted runner)
- [ ] `kindling runners --platform circleci` CLI path
- [ ] End-to-end test: `generate` → push → CircleCI picks up job → build + deploy

### Generate parity across all three providers

Every provider should handle the same repo the same way — same dependency
detection, same secrets awareness, same multi-service support. Just
different output formats.

- [ ] Unified few-shot example injection (15–20 curated examples per provider)
- [ ] LLM validation pass after generation (second cheaper call to catch errors)
- [ ] Interactive ingress selection for multi-service repos
- [ ] `--platform` auto-detection from git remote origin
- [ ] Parity test suite: run the same 20 repos through all 3 providers,
  compare detection results (services, deps, secrets, OAuth flags)

### Fuzz testing all three providers

Clone a large corpus of real-world repos, run `kindling generate` against
each with every provider, record structured results.

**Per-repo result record:**

| Field | Description |
|---|---|
| `repo` | GitHub URL |
| `language` | Primary language |
| `has_dockerfile` | Whether a Dockerfile exists |
| `services_detected` | Number of services found |
| `provider` | github / gitlab / circleci |
| `exit_code` | `kindling generate` exit code |
| `workflow_valid` | Whether generated config parses |
| `failure_category` | `no_dockerfile`, `env_parse_error`, `crash`, `timeout`, etc. |

**Repo selection — bias toward agent projects:**
- Multi-agent repos (LangGraph, CrewAI, AutoGen projects on GitHub)
- Multi-service repos with docker-compose
- Monorepos with multiple Dockerfiles
- Python ML/AI repos (the primary agent audience)
- Standard web stacks (Go, Node, Rails, Spring) for baseline coverage

**Quality gates (must hit before public launch):**
- Zero panics across the entire corpus
- ≥80% success rate on repos with a Dockerfile
- ≥90% valid YAML output when generation succeeds
- Top 10 failure modes identified and fixed per provider

---

## Phase 2 — Multi-agent adoption (the main event)

With CI solid across three providers, everything shifts to making kindling
*the* way to build multi-agent systems. Framework-agnostic infrastructure —
kindling doesn't compete with LangGraph or CrewAI, it handles the part
they ignore.

### Agent-native dependency types

Add first-class operator support for infrastructure agents actually need:

- [ ] `qdrant` — vector store (most popular OSS option for agent RAG)
- [ ] `weaviate` — vector store (strong multi-modal support)
- [ ] `chroma` — vector store (lightweight, Python-native)
- [ ] `milvus` — vector store (high-scale)
- [ ] `langfuse` — LLM observability (traces, evals, prompt management)
- [ ] `temporal` — workflow orchestration (agent reliability patterns)

Same pattern as postgres/redis/kafka: declare in DSE YAML, operator provisions
it, connection URL injected automatically.

### Template repos (the adoption flywheel)

Each template is `git clone → kindling init → git push → running system`.
New devs start building immediately. Every tutorial links to a template.

- [ ] `kindling-sh/template-langgraph` — orchestrator + tool agents + Postgres + Redis + Qdrant
- [ ] `kindling-sh/template-crewai` — crew of agents + RAG service + vector store
- [ ] `kindling-sh/template-autogen` — AutoGen agents + shared memory service
- [ ] `kindling-sh/template-mcp` — MCP servers + agent orchestrator
- [ ] `kindling-sh/template-raw-agents` — no framework, pure Python/Go agents via queues

Each template includes:
1. Working Dockerfiles for every service
2. Pre-built CI workflow for all 3 providers (no API key needed)
3. README with 3-command getting started
4. Real agent logic — tool use, RAG retrieval, inter-agent messaging

### `kindling new` — interactive project scaffolding

The `create-react-app` moment for agent systems:

```
kindling new
? What are you building? → Multi-agent system
? Which framework? → CrewAI / LangGraph / None
? How many agents? → 3
? Need a vector store? → Yes (Qdrant)
? Additional dependencies? → Postgres, Redis
? CI provider? → GitHub Actions / GitLab CI / CircleCI

✅ Project scaffolded at ./my-agent-system
   3 agents + orchestrator + Qdrant + Postgres + Redis
   Dockerfiles, CI workflow, and DSE manifest included.

   Next: cd my-agent-system && kindling init && git push
```

### `kindling generate` agent awareness

Teach the scanner to recognize agent patterns:

- [ ] Detect `CrewAgent`, `@tool`, `StateGraph`, `AssistantAgent`, `autogen`,
  `langchain`, `llama_index` imports → tag as agent service
- [ ] Auto-detect LLM provider usage → suggest `kindling secrets set`
- [ ] Detect vector store client usage → auto-add matching dependency
- [ ] Generate appropriate health checks for agent services

---

## Phase 3 — Get in front of the right people

### Content: agent framework tutorials

Tutorials that start with a popular framework and end with kindling:

- [ ] "Build a Multi-Agent RAG System with LangGraph + Kindling"
- [ ] "CrewAI → Production-Ready on Your Laptop in 15 Minutes"
- [ ] "AutoGen Agents with Shared Memory: Local Dev with Kindling"
- [ ] "Build an MCP Server + Agent Orchestrator with Kindling"
- [ ] "Your First Multi-Agent System: Zero to Running in 5 Minutes"
  (targets less experienced devs, no framework, raw agents + kindling)

Each tutorial has a companion template repo. Cross-post to Dev.to,
Hashnode, and the framework's community channels.

### Framework ecosystem integration

Get into the canonical flow for each framework:

- [ ] PR deployment guides to LangGraph / CrewAI / AutoGen docs
- [ ] Get listed on `awesome-langchain`, `awesome-llm`, `awesome-agents`
- [ ] Active presence in framework Discords — be helpful, mention kindling
  when it genuinely solves someone's problem
- [ ] MCP server support — deploy MCP servers as a dependency type,
  plugging into the Anthropic ecosystem

### Show HN + launch content

- [ ] Show HN post — position as "the local dev engine for multi-agent apps"
- [ ] README hero demo — screen recording GIF showing `kindling init` →
  `git push` → multi-agent system running on localhost
- [ ] YouTube walkthrough — full multi-agent build, 5 minutes
- [ ] Short-form clips for Twitter/LinkedIn from the video

### Community targeting

Go where agent builders are, not where K8s people are:

- [ ] r/LocalLLaMA, r/LangChain, r/MachineLearning — help with infra questions
- [ ] LangChain Discord, CrewAI Discord, AutoGen Discord
- [ ] AI/ML Twitter — engage with agent framework discussions
- [ ] AI hackathon sponsorship — kindling as scaffolding for every team
- [ ] University AI club hack days

### Hackathon strategy

AI hackathons are the best distribution channel. Every team needs to deploy
a multi-agent system in 24–48 hours. Kindling eliminates the infra setup.

- [ ] Sponsor 2–3 online AI hackathons
- [ ] Provide `kindling new` templates pre-configured for common hackathon stacks
- [ ] Offer mentorship/support during events
- [ ] Write up winning projects that used kindling

---

## Phase 4 — Deepen the product

Features that make kindling stickier once people are using it.

### `kindling diagnose` — make K8s less scary

Most agent developers aren't K8s experts. When something breaks, they
need clear answers, not `kubectl describe pod` output.

```
kindling diagnose
kindling diagnose --fix    # LLM-powered remediation suggestions
```

Detects: CrashLoopBackOff, ImagePullBackOff, missing secrets/configmaps,
service selector mismatches, ingress routing gaps, probe failures.
`--fix` sends errors to the configured LLM for concrete fix suggestions.

### `kindling export` — production-ready manifests

Export the running cluster state as a Helm chart or Kustomize overlay.
By the time a dev has iterated in kindling, the cluster contains
battle-tested manifests — export snapshots them for production.

```
kindling export helm --output ./chart
kindling export kustomize --output ./k8s
```

### Stable tunnel URLs

`kindling expose` gets a new URL every reconnection. Agent systems using
webhook callbacks (Slack, Stripe, OAuth) break. Add a stable relay URL
at `<user>.relay.kindling.dev` that auto-updates on reconnect.

### VS Code extension

Status panel, deploy button, logs, tunnel control — native VS Code
experience. The marketplace puts kindling in front of every developer
browsing for K8s or AI dev tools.

### Education angle

- [ ] University CS / AI programs — kindling for coursework
- [ ] Bootcamps — adopt kindling for agent-building labs
- [ ] "Kindling 101" curriculum / workshop materials

---

## Phase 5 — OSS infrastructure (when there's community)

- [ ] `CONTRIBUTING.md` with dev setup, test instructions, PR expectations
- [ ] `good-first-issue` labels for approachable tasks
- [ ] Issue & PR templates
- [ ] `CODE_OF_CONDUCT.md`
- [ ] Dynamic README badges (CI status, coverage, release)
- [ ] Contributor shout-outs in release notes

---

## Done ✅

- [x] Homebrew formula (`brew install kindling-sh/tap/kindling`)
- [x] One-liner install script
- [x] Provider abstraction interfaces (`pkg/ci/`)
- [x] GitHub Actions provider (generate, runners, workflow)
- [x] GitLab CI provider (generate, runners, workflow)
- [x] 15 auto-provisioned dependency types
- [x] `kindling sync` — live file sync with 30+ runtime strategies
- [x] `kindling dashboard` — web UI with sync/load/deploy
- [x] `kindling generate` — AI workflow generation (OpenAI + Anthropic)
- [x] `kindling secrets` — credential management with local backup
- [x] `kindling expose` — public HTTPS tunnels (cloudflared + ngrok)
- [x] Comprehensive test coverage (342 tests across 22 files)
