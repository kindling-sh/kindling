# Kindling — Roadmap

**Mission:** Make kindling the default way to build production-ready
multi-agent systems — as reflexive as `git init`.

**Product arc:** Wire up CI in minutes → build locally with an engine
that grows with the project → export and deploy to production.

**Guiding principle:** Adoption is the entire job for the next 6–12
months. Revenue, sponsorship, and partnerships follow from being the
standard. The patterns and tooling defaults for multi-agent systems are
still up for grabs — win by being present and useful before the niche
solidifies. As a solo builder, ruthless prioritization is essential. The
product must stay ahead of the marketing — bringing people in before the
experience is solid burns the community before it forms.

---

## Phase 1 — Stabilize & define the v1.0 story

**Trigger:** CircleCI support complete. Three CI providers is the
credible, press-ready milestone — the difference between "interesting
project" and "this is real." Don't invest heavily in outreach before
this is done.

**Exit criteria:** A stranger can clone a repo, run `kindling init`, and
have a working multi-agent system deployed locally in under 15 minutes
without asking anyone anything.

### CircleCI generate + runners

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

**Quality gates (must hit before going public):**
- Zero panics across the entire corpus
- ≥80% success rate on repos with a Dockerfile
- ≥90% valid YAML output when generation succeeds
- Top 10 failure modes identified and fixed per provider

### New-user walkthrough

Run through the full `kindling init` → `kindling generate` → first deploy
flow yourself as a brand-new user. Fix every rough edge. Write a clean,
honest CHANGELOG entry that frames this as v1.0.

---

## Phase 2 — Build the flagship example + agent infrastructure

**One great example beats ten mediocre ones.** Build the flagship *before*
doing outreach. It becomes the demo, the hackathon template, the first
blog post, and the thing you link in every community interaction.

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

### Flagship: LangGraph template

Pick LangGraph first — largest community, most active, most likely to hit
production pain. Build a real multi-agent app, not a toy:

- [ ] Orchestrator + at least 2 tool-calling agents + real infra
  (Postgres + Redis or Qdrant)
- [ ] Good candidates: research agent system, customer support pipeline,
  code review agent with memory
- [ ] Fully deployable with `kindling init → git push`
- [ ] Step-by-step documentation
- [ ] Pre-built CI workflows for all 3 providers (no API key needed)

This becomes the flagship demo, the hackathon starter, and the basis
for the first blog post.

### Then build (spaced so each gets its own moment):

- [ ] `kindling-sh/template-crewai` — crew of agents + RAG service + vector store
- [ ] `kindling-sh/template-autogen` — AutoGen agents + shared memory service
- [ ] `kindling-sh/template-mcp` — MCP servers + agent orchestrator
- [ ] `kindling-sh/template-raw-agents` — no framework, pure Python/Go agents via queues

Each template includes:
1. Working Dockerfiles for every service
2. Pre-built CI workflow for all 3 providers
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

- [ ] Detect `CrewAgent`, `@tool`, `StateGraph`, `AssistantAgent`, `autogen`,
  `langchain`, `llama_index` imports → tag as agent service
- [ ] Auto-detect LLM provider usage → suggest `kindling secrets set`
- [ ] Detect vector store client usage → auto-add matching dependency
- [ ] Generate appropriate health checks for agent services

### `kindling export` + `kindling deploy-prod` — local to production

This is the closer. Kindling takes you from `kindling init` all the way
to production.

**`kindling export`** — generate production-ready manifests from the
running cluster:

```bash
kindling export helm --output ./chart
kindling export kustomize --output ./k8s
```

- Helm chart with parameterized `values.yaml` (image tags, replicas,
  hosts, resource limits, secret refs)
- Kustomize base + production overlay with placeholder patches
- Strips all kindling-specific and Kind-specific resources
- Redacts secret values with `# TODO: set me` placeholders
- Normalizes namespaces, converts localhost hosts to placeholders

**`kindling deploy-prod`** — deploy to a real cluster:

```bash
kindling deploy-prod --context my-prod-cluster
kindling deploy-prod --context my-prod-cluster --from ./chart
kindling deploy-prod --context my-prod-cluster --dry-run
```

- [ ] `kindling export helm` — snapshot cluster state to Helm chart
- [ ] `kindling export kustomize` — snapshot to Kustomize base + overlay
- [ ] `kindling deploy-prod --context <ctx>` — deploy to any K8s cluster
- [ ] Interactive secret prompting (or `--secrets-from <file>`)
- [ ] Pre-deploy validation — cluster reachable, namespaces exist
- [ ] `--dry-run` and `--diff` modes
- [ ] Image registry translation (`--registry ghcr.io/org`)

The full product story:
```
kindling init          → local cluster
kindling generate      → CI pipeline
git push               → build + deploy locally
kindling sync          → iterate fast
kindling deploy-prod   → ship to production
```

---

## Phase 3 — First community outreach (careful, manual seeding)

**Trigger:** v1.0 stable + LangGraph flagship example live.

Don't blast. Seed carefully and manually. The goal from this phase:
**10–20 people who have genuinely used kindling** and can answer "has
anyone tried Kindling?" positively.

### Target communities & channels

**LangChain Community Slack** — [langchain.com/join-community](https://www.langchain.com/join-community)
- Classified as a **vendor** (OSS maintainer counts)
- Post only in: `#vendor-content`, `#events`, or `#vendor-specific`
- No unsolicited DMs, no posting in help threads
- Frame around the problem, not the product

**CrewAI Community**
- Discord: [discord.com/invite/X4JWnZnxPb](https://discord.com/invite/X4JWnZnxPb)
- Forum: [community.crewai.com](https://community.crewai.com) — better for
  detailed intro posts since it's searchable and indexed (someone Googling
  "deploy CrewAI production" may find it later)
- Culture is more permissive — builder-sharing is welcomed

**Latent Space Discord** — [discord.gg/xJJMRaWCRt](https://discord.gg/xJJMRaWCRt)
- High signal, practitioner-heavy (~9k members)
- The swyx/Alessio podcast community — exactly the right audience
- Builder-friendly culture, look for `#show-and-tell` or `#tools`

**Reddit**
- r/LocalLLaMA, r/LangChain, r/MachineLearning
- Help with infra questions first, mention kindling when genuinely relevant

### How to post

Lead with the problem, not the product. Target message:

> *"I've been frustrated by how hard it is to run a full multi-agent
> system locally with Postgres, Redis, and vector stores all wired
> together — so I built [Kindling](https://kindling.sh). It's open
> source, and I'm looking for 5–10 people to try the LangGraph example
> and tell me where it breaks."*

The small number ("5–10 people") converts better than a general call to
action. It signals you want feedback, not users.

### Framework ecosystem integration

- [ ] PR deployment guides to LangGraph / CrewAI / AutoGen docs
- [ ] Get listed on `awesome-langchain`, `awesome-llm`, `awesome-agents`
- [ ] MCP server support — deploy MCP servers as a dependency type

---

## Phase 4 — Content that compounds

**Trigger:** Real user feedback in hand, know where people struggle.

These are not thought leadership pieces — they're search-optimized,
community-shareable, genuinely useful content. One framework example
per month, one blog post per example.

### Priority posts

1. **"Why multi-agent systems fail in production (and how to fix it
   before you start)"** — anchor content, no mention of kindling, just
   owns the problem space. Ranks and gets shared independently.

2. **"Running a full LangGraph system locally with Postgres, Redis, and
   Qdrant"** — step-by-step, tied to the flagship example. Targets the
   search query people actually have.

3. **"Kindling + CrewAI: from prototype to locally deployable in 10
   minutes"** — framework-specific, targets CrewAI community searches.

4. One post per additional framework example as they're built.

### Distribution for each post

- Post to the relevant community channel (LangChain Slack, CrewAI forum,
  Latent Space Discord)
- Cross-post to Hacker News (Show HN for launch, Ask HN for problem piece)
- Twitter/X thread with terminal output — the `kindling init` visual
  proof of value travels well
- Cross-post to Dev.to, Hashnode

---

## Phase 5 — The launch moment

**Trigger:** CircleCI shipped + 2–3 polished examples + 20+ genuine
GitHub stars.

This is the coordinated push:

- [ ] **Show HN** — "Show HN: Kindling – local Kubernetes CI for
  multi-agent systems, auto-generated from your repo." Technical,
  specific, novel — exactly what HN responds to.
- [ ] **Product Hunt** — schedule same week as Show HN
- [ ] **README hero demo** — screen recording GIF showing `kindling init`
  → `git push` → multi-agent system running on localhost
- [ ] **YouTube walkthrough** — full multi-agent build, 5 minutes
- [ ] **Short-form clips** for Twitter/LinkedIn from the video
- [ ] Simultaneous cross-posts to all community channels
- [ ] GitHub star push — ask early users explicitly

The "full CI support across GitHub Actions, GitLab CI, and CircleCI"
story is the hook — cleanest single-sentence pitch, signals maturity.

---

## Phase 6 — Hackathon

**Don't run a hackathon cold.** The sequencing:

1. 20–50 genuine users exist
2. A few have built real projects with kindling
3. You can feature their work as examples
4. *Then* run the hackathon — seed with those examples, have real mentors

**Format:** Virtual first, in-person later. Focused challenge: *"Build
the best production-ready multi-agent system with Kindling."*

**Strategic move:** Partner with LangChain or LlamaIndex to co-host.
Instantly inherit their community's attention, they get a showcase event.
Reach out to DevRel teams — mutually beneficial ask.

- [ ] Sponsor 2–3 online AI hackathons
- [ ] Provide `kindling new` templates pre-configured for hackathon stacks
- [ ] Offer mentorship/support during events
- [ ] Write up winning projects that used kindling

---

## Phase 7 — Deepen the product

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

### Stable tunnel URLs

`kindling expose` gets a new URL every reconnection. Agent systems using
webhook callbacks (Slack, Stripe, OAuth) break. Add a stable relay URL
at `<user>.relay.kindling.dev` that auto-updates on reconnect.

### VS Code extension

Status panel, deploy button, logs, tunnel control — native VS Code
experience. The marketplace puts kindling in front of every developer
browsing for K8s or AI dev tools.

### Dashboard: visual agent builder (drag & drop)

Evolve the dashboard from a monitoring UI into a visual builder for
multi-agent systems. Drag agent nodes onto a canvas, connect them with
message flows, drop in infrastructure (vector stores, queues, databases),
and kindling generates the service code, Dockerfiles, DSE manifest, and
CI workflow.

Think n8n / Langflow, but for the full-stack architecture — not just
prompt chains, the actual deployed services.

- [ ] Canvas-based agent graph editor (React Flow / xyflow)
- [ ] Node types: Agent, Tool, Orchestrator, RAG Pipeline, API Gateway
- [ ] Dependency nodes: Postgres, Redis, Qdrant, Kafka, etc.
- [ ] Edge types: HTTP, queue-based messaging, shared memory
- [ ] "Deploy" button generates all artifacts and runs `kindling init` + `git push`
- [ ] Round-trip: import an existing kindling project back into the canvas
- [ ] Export the graph as a shareable project template

### Education angle

- [ ] University CS / AI programs — kindling for coursework
- [ ] Bootcamps — adopt kindling for agent-building labs
- [ ] "Kindling 101" curriculum / workshop materials

---

## Phase 8 — OSS infrastructure (when there's community)

- [ ] `CONTRIBUTING.md` with dev setup, test instructions, PR expectations
- [ ] `good-first-issue` labels for approachable tasks
- [ ] Issue & PR templates
- [ ] `CODE_OF_CONDUCT.md`
- [ ] Dynamic README badges (CI status, coverage, release)
- [ ] Contributor shout-outs in release notes

---

## Solo builder timeline

| When | Focus |
|---|---|
| **Now** | Finish CircleCI, stabilize the core experience |
| **Next** | Build the LangGraph flagship example |
| **After that** | v1.0 announcement + first community seeding |
| **Ongoing** | One framework example/month, one blog post/example |
| **3–6 months** | Show HN / Product Hunt launch moment |
| **6–12 months** | Hackathon, partnerships |

The test at every stage: *does this make it more likely that the next
person who starts a multi-agent project reaches for kindling?*
If yes, do it. If not, skip it.

---

## Key reference links

- LangChain Community Slack: [langchain.com/join-community](https://www.langchain.com/join-community)
- CrewAI Discord: [discord.com/invite/X4JWnZnxPb](https://discord.com/invite/X4JWnZnxPb)
- CrewAI Forum: [community.crewai.com](https://community.crewai.com)
- Latent Space Discord: [discord.gg/xJJMRaWCRt](https://discord.gg/xJJMRaWCRt)
- Kindling GitHub: [github.com/kindling-sh/kindling](https://github.com/kindling-sh/kindling)

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
