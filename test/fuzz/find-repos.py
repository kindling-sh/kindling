#!/usr/bin/env python3
"""
find-repos.py — Search GitHub for repos with self-contained Dockerfiles
that are good candidates for kindling generate fuzz testing.

The key insight: we dig deep into fewer repos rather than skimming many.
For each candidate we actually read the Dockerfiles and docker-compose,
check for self-containment (no pre-build steps), and score buildability.

Criteria:
  1. Has a docker-compose.yml with "build:" directives (multi-service, buildable)
  2. OR has ≥1 Dockerfile that is self-contained (builds from fresh clone)
  3. Not archived, not a fork, has recent activity
  4. No Nx/Turborepo/Bazel/Lerna monorepo tooling
  5. Dockerfiles don't COPY from build-artifact dirs (dist/, build/, out/, target/)

Output tiers:
  - RECOMMENDED: score ≥ 60, self-contained, no pipeline gaps
  - STRETCH: buildable but exercises unimplemented pipeline features
    (build args, build target, env_file, HEALTHCHECK, RUN --mount, etc.)
    Each gap lists which file to fix (run.sh, generate.go, analyze.py)
  - MAYBE: score 40-59, might need manual review
  - SKIPPED: monorepo, no Dockerfiles, or low score

Usage:
  python3 find-repos.py [--token GITHUB_TOKEN] [--out repos-candidates.txt] [--limit 30]

Without a token you get 10 search requests/min. With a token: 30/min.
"""

import argparse
import base64
import json
import os
import re
import sys
import time
import urllib.request
import urllib.error
import urllib.parse


API = "https://api.github.com"
HEADERS = {
    "Accept": "application/vnd.github+json",
    "X-GitHub-Api-Version": "2022-11-28",
}

# Repos to skip (already in repos.txt, or known bad fits)
SKIP_OWNERS = {
    "kindling-sh", "jeff-vincent",  # our own repos
}

# Languages we care about
LANGUAGES = [
    "Go", "Python", "JavaScript", "TypeScript", "Java", "Ruby",
    "PHP", "Rust", "C#", "Elixir", "Kotlin", "Scala",
]


def api_get(path: str, token: str | None = None, params: dict = None) -> dict:
    """Make a GitHub API GET request with rate-limit retry."""
    url = f"{API}{path}"
    if params:
        url += "?" + urllib.parse.urlencode(params)

    headers = dict(HEADERS)
    if token:
        headers["Authorization"] = f"Bearer {token}"

    for attempt in range(5):
        req = urllib.request.Request(url, headers=headers)
        try:
            with urllib.request.urlopen(req, timeout=30) as resp:
                remaining = resp.headers.get("X-RateLimit-Remaining", "?")
                reset = resp.headers.get("X-RateLimit-Reset", "0")
                data = json.loads(resp.read().decode())

                # If we're running low on rate limit, pause
                if remaining != "?" and int(remaining) < 3:
                    wait = max(int(reset) - int(time.time()), 5) + 1
                    print(f"  ⏳ Rate limit low ({remaining} left), waiting {wait}s...",
                          file=sys.stderr)
                    time.sleep(wait)

                return data

        except urllib.error.HTTPError as e:
            if e.code == 403:  # rate limited
                reset = e.headers.get("X-RateLimit-Reset", "0")
                wait = max(int(reset) - int(time.time()), 10) + 1
                print(f"  ⏳ Rate limited, waiting {wait}s (attempt {attempt+1}/5)...",
                      file=sys.stderr)
                time.sleep(wait)
                continue
            elif e.code == 422:  # validation error (search too broad)
                return {"items": [], "total_count": 0}
            else:
                print(f"  ❌ HTTP {e.code}: {e.read().decode()[:200]}", file=sys.stderr)
                return {"items": [], "total_count": 0}
        except Exception as e:
            print(f"  ❌ Request error: {e}", file=sys.stderr)
            if attempt < 4:
                time.sleep(5)
                continue
            return {"items": [], "total_count": 0}

    return {"items": [], "total_count": 0}


def search_repos_with_compose(token: str | None, page: int = 1) -> list[dict]:
    """Search for repos that have docker-compose files with build directives."""
    # GitHub code search: find docker-compose files with "build:" in them
    data = api_get("/search/code", token, {
        "q": "filename:docker-compose.yml build path:/",
        "per_page": 100,
        "page": page,
    })
    return data.get("items", [])


def search_repos_with_compose_v2(token: str | None, lang: str, page: int = 1) -> list[dict]:
    """Search for repos by language that have docker-compose files."""
    data = api_get("/search/repositories", token, {
        "q": f"language:{lang} docker-compose in:readme stars:>50 pushed:>2024-01-01",
        "sort": "stars",
        "order": "desc",
        "per_page": 30,
        "page": page,
    })
    return data.get("items", [])


def search_repos_multi_dockerfile(token: str | None, lang: str, page: int = 1) -> list[dict]:
    """Search for repos by language that mention microservices and have stars."""
    data = api_get("/search/repositories", token, {
        "q": f"language:{lang} topic:microservices stars:>20 pushed:>2024-01-01",
        "sort": "stars",
        "order": "desc",
        "per_page": 30,
        "page": page,
    })
    return data.get("items", [])


def search_repos_docker_topic(token: str | None, lang: str, page: int = 1) -> list[dict]:
    """Search for repos with docker topic."""
    data = api_get("/search/repositories", token, {
        "q": f"language:{lang} topic:docker stars:>100 pushed:>2024-01-01",
        "sort": "updated",
        "order": "desc",
        "per_page": 30,
        "page": page,
    })
    return data.get("items", [])


def check_repo_has_compose_with_build(token: str | None, owner: str, repo: str) -> dict | None:
    """Fetch and parse docker-compose.yml. Returns parsed content or None."""
    for name in ["docker-compose.yml", "docker-compose.yaml", "compose.yml", "compose.yaml"]:
        data = api_get(f"/repos/{owner}/{repo}/contents/{name}", token)
        if "content" not in data:
            continue
        try:
            content = base64.b64decode(data["content"]).decode("utf-8", errors="replace")
            if "build:" in content or "build :" in content:
                return {"filename": name, "content": content}
        except Exception:
            continue
    return None


def fetch_file_content(token: str | None, owner: str, repo: str, path: str) -> str | None:
    """Fetch a single file's content from the GitHub API."""
    data = api_get(f"/repos/{owner}/{repo}/contents/{path}", token)
    if "content" not in data:
        return None
    try:
        return base64.b64decode(data["content"]).decode("utf-8", errors="replace")
    except Exception:
        return None


def find_dockerfiles(token: str | None, owner: str, repo: str) -> list[dict]:
    """Find all Dockerfiles in the repo and return their paths + content."""
    data = api_get("/search/code", token, {
        "q": f"filename:Dockerfile repo:{owner}/{repo}",
        "per_page": 20,
    })
    time.sleep(0.5)

    dockerfiles = []
    for item in data.get("items", []):
        path = item.get("path", "")
        # Skip test/example/ci Dockerfiles
        lower_path = path.lower()
        if any(skip in lower_path for skip in [
            "test/", "tests/", "example/", "examples/", ".ci/",
            "ci/", "hack/", "scripts/", "deploy/", ".devcontainer",
        ]):
            continue

        content = fetch_file_content(token, owner, repo, path)
        time.sleep(0.3)
        if content:
            dockerfiles.append({"path": path, "content": content})

    return dockerfiles


# ── Monorepo / build-tool detection ────────────────────────────────────────

MONOREPO_MARKERS = [
    "nx.json",           # Nx
    "turbo.json",        # Turborepo
    "lerna.json",        # Lerna
    "pnpm-workspace.yaml",  # pnpm workspaces
    "BUILD",             # Bazel
    "WORKSPACE",         # Bazel
    "pants.toml",        # Pants
]


def check_monorepo_markers(token: str | None, owner: str, repo: str) -> list[str]:
    """Check if the repo root has monorepo build-tool config files."""
    data = api_get(f"/repos/{owner}/{repo}/contents", token)
    if not isinstance(data, list):
        return []
    root_files = {item["name"] for item in data if item.get("type") == "file"}
    found = [m for m in MONOREPO_MARKERS if m in root_files]
    return found


# ── Dockerfile quality analysis ────────────────────────────────────────────

# Directories that are typically build artifacts (not in git)
BUILD_ARTIFACT_DIRS = {"dist", "build", "out", "target", ".next", "output", "artifacts"}


def analyze_dockerfile(content: str, path: str) -> dict:
    """Analyze a Dockerfile for self-containment and buildability.

    Returns a dict with:
      - score: 0-100 quality score
      - flags: list of green/yellow/red flag strings
      - is_self_contained: bool
      - has_multi_stage: bool
      - base_images: list of FROM images
      - expose_ports: list of exposed ports
    """
    lines = content.splitlines()
    flags = []
    score = 50  # start neutral

    # ── Parse FROM lines ───────────────────────────────────────
    from_lines = [l for l in lines if re.match(r'^\s*FROM\s', l, re.IGNORECASE)]
    base_images = []
    has_multi_stage = len(from_lines) > 1
    has_builder_stage = False

    for fl in from_lines:
        # Extract image name (skip --platform flags)
        parts = fl.split()
        img = None
        for p in parts[1:]:
            if p.startswith("--"):
                continue
            if p.upper() == "AS":
                break
            img = p
            break
        if img and not img.startswith("$"):
            base_images.append(img)
        # Check for AS builder pattern
        if " AS " in fl.upper() or " as " in fl:
            has_builder_stage = True

    if has_multi_stage:
        flags.append("🟢 multi-stage build (self-contained)")
        score += 15
    if has_builder_stage:
        score += 5

    # ── Check for COPY from build-artifact directories ─────────
    copy_lines = [l for l in lines
                  if re.match(r'^\s*COPY\s', l, re.IGNORECASE)
                  and not re.match(r'^\s*COPY\s+--from=', l, re.IGNORECASE)]

    copies_artifacts = False
    for cl in copy_lines:
        parts = cl.split()
        for src in parts[1:-1]:  # skip COPY and the dest (last arg)
            src_dir = src.strip("./").split("/")[0]
            if src_dir in BUILD_ARTIFACT_DIRS:
                # In multi-stage, COPY --from=builder dist/ is fine,
                # but plain COPY dist/ means it expects pre-built artifacts
                copies_artifacts = True
                flags.append(f"🔴 COPY from '{src_dir}/' — likely requires pre-build step")
                score -= 30

    # ── Check for COPY --from= (multi-stage is fine) ───────────
    copy_from_lines = [l for l in lines
                       if re.match(r'^\s*COPY\s+--from=', l, re.IGNORECASE)]
    if copy_from_lines and not copies_artifacts:
        flags.append("🟢 uses COPY --from= (proper multi-stage)")
        score += 5

    # ── Check for package manager install ──────────────────────
    run_lines = [l for l in lines if re.match(r'^\s*RUN\s', l, re.IGNORECASE)]
    run_text = " ".join(run_lines).lower()

    has_install = any(pm in run_text for pm in [
        "npm install", "npm ci", "yarn install", "pnpm install",
        "pip install", "poetry install", "go build", "go mod",
        "cargo build", "mvn ", "gradle", "dotnet restore",
        "bundle install", "composer install", "mix deps.get",
    ])
    if has_install:
        flags.append("🟢 has dependency install step")
        score += 10

    # ── Check for EXPOSE ───────────────────────────────────────
    expose_ports = []
    for l in lines:
        if re.match(r'^\s*EXPOSE\s', l, re.IGNORECASE):
            for token in l.split()[1:]:
                m = re.match(r'(\d+)', token)
                if m:
                    expose_ports.append(m.group(1))
    if expose_ports:
        flags.append(f"🟢 EXPOSE {', '.join(expose_ports)}")
        score += 5

    # ── Check for CMD/ENTRYPOINT ───────────────────────────────
    has_cmd = any(re.match(r'^\s*(CMD|ENTRYPOINT)\s', l, re.IGNORECASE) for l in lines)
    if has_cmd:
        score += 5
    else:
        flags.append("🟡 no CMD/ENTRYPOINT")
        score -= 5

    # ── Check for known Kaniko issues (fixable, not blocking) ──
    if "poetry install" in run_text and "--no-root" not in run_text:
        flags.append("🟡 poetry install without --no-root (fixable via patch)")

    if any(f"${{{v}}}" in content or f"${v}" in content
           for v in ["TARGETARCH", "BUILDPLATFORM", "TARGETPLATFORM"]):
        flags.append("🟡 BuildKit platform ARGs (fixable via patch)")

    if "go build" in run_text and "-buildvcs=false" not in run_text:
        flags.append("🟡 go build without -buildvcs=false (fixable via patch)")

    if any(npm in run_text for npm in ["npm install", "npm ci", "npm run"]):
        if "npm_config_cache" not in content:
            flags.append("🟡 npm without cache redirect (fixable via patch)")

    # ── Private/unusual base images ────────────────────────────
    for img in base_images:
        # Standard public registries
        if any(img.startswith(r) for r in [
            "node:", "python:", "golang:", "ruby:", "php:", "rust:",
            "openjdk:", "amazoncorretto:", "eclipse-temurin:", "maven:",
            "gradle:", "mcr.microsoft.com/", "docker.io/", "nginx:",
            "alpine:", "ubuntu:", "debian:", "centos:", "fedora:",
            "elixir:", "composer:", "dotnet/", "scratch",
        ]):
            continue
        # Docker Hub official images (no slash = official)
        if "/" not in img:
            continue
        # ghcr.io / quay.io are public
        if img.startswith("ghcr.io/") or img.startswith("quay.io/"):
            continue
        flags.append(f"🟡 non-standard base image: {img}")
        score -= 5

    # ── Detect pipeline edge cases (not yet handled) ──────────
    #
    # These are features that will BUILD but expose gaps in
    # run.sh / analyze.py / generate.go that have clear fix paths.
    edge_cases = []

    # ARG before first FROM → parameterised base image
    first_from_idx = next((i for i, l in enumerate(lines)
                           if re.match(r'^\s*FROM\s', l, re.IGNORECASE)), len(lines))
    args_before_from = [l for i, l in enumerate(lines)
                        if i < first_from_idx
                        and re.match(r'^\s*ARG\s', l, re.IGNORECASE)]
    if args_before_from:
        edge_cases.append({
            "id": "arg-before-from",
            "desc": "ARG before FROM — parameterised base image",
            "fix": "run.sh: resolve ARG default or pass --build-arg",
        })

    # HEALTHCHECK instruction — Kaniko builds it, but pipeline
    # doesn't map it to a K8s readinessProbe
    if any(re.match(r'^\s*HEALTHCHECK\s', l, re.IGNORECASE) for l in lines):
        edge_cases.append({
            "id": "healthcheck",
            "desc": "HEALTHCHECK instruction in Dockerfile",
            "fix": "generate.go: map HEALTHCHECK to K8s readinessProbe",
        })

    # RUN --mount=type=secret or type=cache (BuildKit-only)
    mount_lines = [l for l in lines
                   if re.search(r'--mount=type=(secret|cache|ssh)', l, re.IGNORECASE)]
    if mount_lines:
        mount_types = set()
        for ml in mount_lines:
            m = re.search(r'--mount=type=(\w+)', ml, re.IGNORECASE)
            if m:
                mount_types.add(m.group(1).lower())
        edge_cases.append({
            "id": "buildkit-mount",
            "desc": f"RUN --mount=type={','.join(sorted(mount_types))}",
            "fix": "run.sh: strip --mount flags for Kaniko or emulate with build args",
        })

    # ADD with URL (network during build)
    add_url_lines = [l for l in lines
                     if re.match(r'^\s*ADD\s', l, re.IGNORECASE)
                     and re.search(r'https?://', l)]
    if add_url_lines:
        edge_cases.append({
            "id": "add-url",
            "desc": "ADD from URL — needs network during build",
            "fix": "run.sh: ensure Kaniko has outbound network access",
        })

    # COPY --link (BuildKit feature, Kaniko doesn't support it)
    if any(re.search(r'COPY\s+--link', l, re.IGNORECASE) for l in lines):
        edge_cases.append({
            "id": "copy-link",
            "desc": "COPY --link (BuildKit optimisation)",
            "fix": "run.sh: strip --link flag via sed before Kaniko build",
        })

    # COPY --chmod / --chown with numeric IDs (Kaniko handles, but can be slow)
    if any(re.search(r'COPY\s+--chmod=', l, re.IGNORECASE) for l in lines):
        edge_cases.append({
            "id": "copy-chmod",
            "desc": "COPY --chmod (BuildKit feature)",
            "fix": "run.sh: strip --chmod flag via sed before Kaniko build",
        })

    is_self_contained = not copies_artifacts and (has_install or has_multi_stage)

    return {
        "score": max(0, min(100, score)),
        "flags": flags,
        "is_self_contained": is_self_contained,
        "has_multi_stage": has_multi_stage,
        "base_images": base_images,
        "expose_ports": expose_ports,
        "edge_cases": edge_cases,
    }


def parse_compose_services(content: str) -> list[dict]:
    """Lightweight docker-compose parser — extract services with build directives."""
    services = []
    # Very simple: find service blocks with build:
    # We don't pull in pyyaml to keep this dependency-free
    in_services = False
    current_service = None
    indent_level = 0

    for line in content.splitlines():
        stripped = line.strip()
        if stripped.startswith("#") or not stripped:
            continue

        # Detect services: block
        if re.match(r'^services:\s*$', stripped):
            in_services = True
            continue

        if not in_services:
            continue

        # Top-level key under services (2-space indent typically)
        # Detect by: starts with non-space content at service-level indent
        leading = len(line) - len(line.lstrip())

        if leading <= 0 and stripped and not stripped.startswith("-"):
            # Left-aligned = new top-level block, end of services
            in_services = False
            continue

        if leading == 2 and stripped.endswith(":") and not stripped.startswith("-"):
            svc_name = stripped.rstrip(":").strip()
            current_service = {"name": svc_name, "has_build": False,
                               "build_context": "", "dockerfile": "",
                               "depends_on": [], "ports": []}
            services.append(current_service)
            indent_level = 2
            continue

        if current_service and leading > indent_level:
            if "build:" in stripped:
                current_service["has_build"] = True
            if stripped.startswith("context:"):
                current_service["build_context"] = stripped.split(":", 1)[1].strip().strip('"').strip("'")
            if stripped.startswith("dockerfile:"):
                current_service["dockerfile"] = stripped.split(":", 1)[1].strip().strip('"').strip("'")

    return [s for s in services if s["has_build"]]


def detect_compose_edge_cases(content: str) -> list[dict]:
    """Detect compose-level features that the pipeline doesn't handle yet.

    Each edge case is a dict with:
      - id: short identifier
      - desc: human-readable description
      - fix: which file(s) need changes to support it
    """
    edge_cases = []
    lines_lower = content.lower()

    # build.args — run.sh doesn't pass --build-arg to Kaniko
    if re.search(r'^\s+args:\s*$', content, re.MULTILINE):
        edge_cases.append({
            "id": "compose-build-args",
            "desc": "build.args in compose — not passed to Kaniko",
            "fix": "run.sh: extract args from compose, pass as --build-arg",
        })

    # build.target — run.sh doesn't pass --target to Kaniko
    if re.search(r'^\s+target:\s*\S', content, re.MULTILINE):
        edge_cases.append({
            "id": "compose-build-target",
            "desc": "build.target in compose — not passed to Kaniko",
            "fix": "run.sh: extract target from compose, pass as --target",
        })

    # env_file — pipeline doesn't read .env or env_file entries
    if "env_file:" in lines_lower or "env_file " in lines_lower:
        edge_cases.append({
            "id": "compose-env-file",
            "desc": "env_file in compose — not loaded by pipeline",
            "fix": "run.sh: parse env_file references, generate K8s ConfigMap",
        })

    # profiles — modern compose feature, pipeline ignores
    if "profiles:" in lines_lower:
        edge_cases.append({
            "id": "compose-profiles",
            "desc": "compose profiles — pipeline doesn't select profiles",
            "fix": "generate.go: detect profiles, include in workflow",
        })

    # extends — compose service inheritance
    if re.search(r'^\s+extends:\s*$', content, re.MULTILINE):
        edge_cases.append({
            "id": "compose-extends",
            "desc": "compose extends — service inheritance not resolved",
            "fix": "generate.go: resolve extends before building service list",
        })

    # healthcheck in compose (not Dockerfile HEALTHCHECK)
    if re.search(r'^\s+healthcheck:\s*$', content, re.MULTILINE):
        edge_cases.append({
            "id": "compose-healthcheck",
            "desc": "compose healthcheck — not mapped to K8s probes",
            "fix": "generate.go: map compose healthcheck to readinessProbe",
        })

    # deploy config (replicas, resources, etc.)
    if re.search(r'^\s+deploy:\s*$', content, re.MULTILINE):
        edge_cases.append({
            "id": "compose-deploy",
            "desc": "compose deploy config — replicas/resources not mapped",
            "fix": "generate.go: map deploy.replicas to K8s replicas",
        })

    # Multiple compose files (override pattern)
    # Can't detect from content alone, but if the compose references
    # ${COMPOSE_FILE} or includes:, it's a sign
    if "include:" in lines_lower or "!include" in lines_lower:
        edge_cases.append({
            "id": "compose-include",
            "desc": "compose include/merge — multi-file not supported",
            "fix": "generate.go: detect and merge multiple compose files",
        })

    return edge_cases


def check_repo_quality(repo: dict) -> dict | None:
    """Filter out repos that aren't good candidates."""
    full_name = repo.get("full_name", "")
    owner = full_name.split("/")[0] if "/" in full_name else ""

    # Skip our own repos
    if owner in SKIP_OWNERS:
        return None

    # Skip forks (we want originals)
    if repo.get("fork"):
        return None

    # Skip archived repos
    if repo.get("archived"):
        return None

    # Skip tiny repos (likely not real projects)
    if (repo.get("size") or 0) < 50:
        return None

    # Skip massive repos (>500MB — too slow to clone)
    if (repo.get("size") or 0) > 500000:
        return None

    return {
        "url": repo.get("html_url", ""),
        "full_name": full_name,
        "stars": repo.get("stargazers_count", 0),
        "language": repo.get("language", ""),
        "size_kb": repo.get("size", 0),
        "description": (repo.get("description") or "")[:100],
        "updated": repo.get("pushed_at", ""),
    }


def deep_validate(token: str | None, candidate: dict) -> dict:
    """Deep-validate a candidate repo. Returns the candidate dict enriched
    with Dockerfile analysis, compose parsing, and a buildability score."""
    owner, repo = candidate["full_name"].split("/", 1)

    # ── Check for monorepo markers ─────────────────────────────
    mono_markers = check_monorepo_markers(token, owner, repo)
    time.sleep(0.3)
    if mono_markers:
        candidate["monorepo_markers"] = mono_markers
        candidate["score"] = 0
        candidate["verdict"] = f"skip:monorepo ({', '.join(mono_markers)})"
        candidate["flags"] = [f"🔴 monorepo tooling: {', '.join(mono_markers)}"]
        return candidate

    # ── Fetch and parse docker-compose ─────────────────────────
    compose = check_repo_has_compose_with_build(token, owner, repo)
    time.sleep(0.3)
    candidate["has_compose_build"] = compose is not None

    compose_services = []
    if compose:
        compose_services = parse_compose_services(compose["content"])
        candidate["compose_services"] = len(compose_services)

    # ── Find and analyze Dockerfiles ───────────────────────────
    dockerfiles = find_dockerfiles(token, owner, repo)
    candidate["dockerfile_count"] = len(dockerfiles)

    if not dockerfiles and not compose:
        candidate["score"] = 0
        candidate["verdict"] = "skip:no-dockerfiles"
        candidate["flags"] = ["🔴 no Dockerfiles found"]
        return candidate

    # Analyze each Dockerfile
    analyses = []
    all_flags = []
    for df in dockerfiles:
        analysis = analyze_dockerfile(df["content"], df["path"])
        analysis["path"] = df["path"]
        analyses.append(analysis)
        for flag in analysis["flags"]:
            all_flags.append(f"  {df['path']}: {flag}")

    candidate["dockerfiles_analyzed"] = len(analyses)
    candidate["flags"] = all_flags

    # ── Compute overall score ──────────────────────────────────
    if not analyses:
        candidate["score"] = 20 if compose else 0
        candidate["verdict"] = "skip:no-analyzable-dockerfiles"
        return candidate

    # Score = average of Dockerfile scores, bonus for compose
    avg_score = sum(a["score"] for a in analyses) / len(analyses)
    if compose:
        avg_score += 10  # bonus for having compose
    if len(analyses) >= 2:
        avg_score += 5   # bonus for multi-service

    # Penalty if ANY Dockerfile is not self-contained
    non_self_contained = [a for a in analyses if not a["is_self_contained"]]
    if non_self_contained:
        avg_score -= 15 * len(non_self_contained)

    candidate["score"] = max(0, min(100, int(avg_score)))

    # Collect expose ports across all Dockerfiles
    all_ports = []
    for a in analyses:
        all_ports.extend(a.get("expose_ports", []))
    candidate["expose_ports"] = all_ports

    # ── Collect edge cases (pipeline gaps) ─────────────────────
    all_edge_cases = []
    for a in analyses:
        all_edge_cases.extend(a.get("edge_cases", []))

    # Compose-level edge cases
    if compose:
        compose_edge = detect_compose_edge_cases(compose["content"])
        all_edge_cases.extend(compose_edge)

    # De-duplicate by id
    seen_ids = set()
    unique_edge_cases = []
    for ec in all_edge_cases:
        if ec["id"] not in seen_ids:
            seen_ids.add(ec["id"])
            unique_edge_cases.append(ec)
    candidate["edge_cases"] = unique_edge_cases

    # ── Verdict ────────────────────────────────────────────────
    has_red = any("🔴" in f for f in all_flags)
    has_yellow = any("🟡" in f for f in all_flags)

    if candidate["score"] >= 60 and not unique_edge_cases:
        candidate["verdict"] = "recommended"
    elif candidate["score"] >= 40 and unique_edge_cases and not has_red:
        # Decent score + exercises unimplemented features = stretch
        candidate["verdict"] = "stretch"
    elif candidate["score"] >= 60 and unique_edge_cases:
        # Good score but has edge cases — still stretch because
        # the edge cases are the whole point of including it
        candidate["verdict"] = "stretch"
    elif candidate["score"] >= 60:
        candidate["verdict"] = "recommended"
    elif candidate["score"] >= 40:
        candidate["verdict"] = "maybe"
    else:
        candidate["verdict"] = "skip:low-score"

    # Check for fixable-only issues (yellow flags but no red)
    if has_yellow and not has_red:
        candidate["actionable"] = True
    else:
        candidate["actionable"] = False

    return candidate


def main():
    parser = argparse.ArgumentParser(description="Find repos for kindling fuzz testing")
    parser.add_argument("--token", default=os.environ.get("GITHUB_TOKEN", ""),
                        help="GitHub API token (or set GITHUB_TOKEN env var)")
    parser.add_argument("--out", default="/Users/jeffvincent/dev/kindling/test/fuzz/repos-candidates.txt",
                        help="Output file path")
    parser.add_argument("--limit", type=int, default=30,
                        help="Max repos to deeply validate")
    args = parser.parse_args()

    token = args.token or None
    if not token:
        print("⚠️  No GitHub token — rate limited to 10 search requests/min", file=sys.stderr)
        print("   Set GITHUB_TOKEN or pass --token for 30/min", file=sys.stderr)

    # ── Load existing repos to skip ────────────────────────────
    existing = set()
    for list_file in ["repos.txt", "repos-e2e.txt"]:
        list_path = os.path.join(os.path.dirname(__file__), list_file)
        if os.path.exists(list_path):
            with open(list_path) as f:
                for line in f:
                    line = line.strip()
                    if line and not line.startswith("#"):
                        # Normalize: strip trailing comments
                        url = line.split("#")[0].strip().split()[0]
                        existing.add(url)
    if existing:
        print(f"📋 Skipping {len(existing)} repos already in lists", file=sys.stderr)

    seen = set()
    candidates = []

    # ── Strategy 1: Search for repos with microservices topic per language ──
    print("\n🔍 Strategy 1: Repos with 'microservices' topic...", file=sys.stderr, flush=True)
    for lang in LANGUAGES:
        print(f"  Searching {lang}...", file=sys.stderr, end="", flush=True)
        lang_count = 0
        for page in range(1, 3):
            repos = search_repos_multi_dockerfile(token, lang, page)
            if not repos:
                break
            for repo in repos:
                info = check_repo_quality(repo)
                if info and info["full_name"] not in seen and info["url"] not in existing:
                    seen.add(info["full_name"])
                    info["source"] = f"microservices-{lang}"
                    candidates.append(info)
                    lang_count += 1
            time.sleep(1)
        print(f" +{lang_count}", file=sys.stderr, flush=True)

    print(f"  📊 {len(candidates)} candidates so far", file=sys.stderr, flush=True)

    # ── Strategy 2: Search for repos with docker topic per language ─────────
    print("\n🔍 Strategy 2: Repos with 'docker' topic...", file=sys.stderr, flush=True)
    for lang in LANGUAGES[:8]:  # top 8 languages to save API calls
        print(f"  Searching {lang}...", file=sys.stderr, end="", flush=True)
        lang_count = 0
        repos = search_repos_docker_topic(token, lang, page=1)
        if repos:
            for repo in repos:
                info = check_repo_quality(repo)
                if info and info["full_name"] not in seen and info["url"] not in existing:
                    seen.add(info["full_name"])
                    info["source"] = f"docker-{lang}"
                    candidates.append(info)
                    lang_count += 1
        print(f" +{lang_count}", file=sys.stderr, flush=True)
        time.sleep(1)

    print(f"  📊 {len(candidates)} candidates so far", file=sys.stderr, flush=True)

    # ── Strategy 3: docker-compose + specific language queries ──────────────
    print("\n🔍 Strategy 3: Repos mentioning docker-compose in README...", file=sys.stderr, flush=True)
    for lang in LANGUAGES[:8]:
        print(f"  Searching {lang}...", file=sys.stderr, end="", flush=True)
        lang_count = 0
        repos = search_repos_with_compose_v2(token, lang, page=1)
        if repos:
            for repo in repos:
                info = check_repo_quality(repo)
                if info and info["full_name"] not in seen and info["url"] not in existing:
                    seen.add(info["full_name"])
                    info["source"] = f"compose-{lang}"
                    candidates.append(info)
                    lang_count += 1
        print(f" +{lang_count}", file=sys.stderr, flush=True)
        time.sleep(1)

    print(f"\n📊 Total unique candidates: {len(candidates)}", file=sys.stderr)

    # ── Sort candidates by stars (more stars = more likely maintained) ──────
    candidates.sort(key=lambda x: x.get("stars", 0), reverse=True)

    # ── Deep validation: read Dockerfiles, check self-containment ──────────
    to_check = min(len(candidates), args.limit)
    print(f"\n🔬 Deep-validating top {to_check} candidates "
          f"(reading Dockerfiles, checking compose, scanning for monorepo markers)...",
          file=sys.stderr)

    validated = []
    for i, c in enumerate(candidates[:to_check]):
        print(f"\n  [{i+1}/{to_check}] {c['full_name']} (⭐{c['stars']})...",
              file=sys.stderr, flush=True)

        c = deep_validate(token, c)

        verdict_icon = {
            "recommended": "✅",
            "stretch": "🧪",
            "maybe": "🟡",
        }.get(c["verdict"], "⏭️ ")

        print(f"    {verdict_icon} score={c['score']} verdict={c['verdict']}",
              file=sys.stderr, flush=True)

        # Print flags
        for flag in c.get("flags", []):
            print(f"    {flag}", file=sys.stderr, flush=True)

        # Print edge cases
        for ec in c.get("edge_cases", []):
            print(f"    🧪 {ec['desc']} → {ec['fix']}", file=sys.stderr, flush=True)

        validated.append(c)

    # ── Separate into tiers ────────────────────────────────────
    recommended = [c for c in validated if c["verdict"] == "recommended"]
    stretch = [c for c in validated if c["verdict"] == "stretch"]
    maybe = [c for c in validated if c["verdict"] == "maybe"]
    skipped = [c for c in validated if c["verdict"].startswith("skip")]

    recommended.sort(key=lambda x: x["score"], reverse=True)
    stretch.sort(key=lambda x: len(x.get("edge_cases", [])), reverse=True)
    maybe.sort(key=lambda x: x["score"], reverse=True)

    print(f"\n{'─' * 60}", file=sys.stderr)
    print(f"📊 Results: {len(recommended)} recommended, {len(stretch)} stretch, "
          f"{len(maybe)} maybe, {len(skipped)} skipped", file=sys.stderr)

    # ── Write output ───────────────────────────────────────────
    with open(args.out, "w") as f:
        f.write("# ─────────────────────────────────────────────────────────────────\n")
        f.write("# repos-candidates.txt — Deeply validated repos for fuzz testing\n")
        f.write(f"# Generated: {time.strftime('%Y-%m-%d %H:%M:%S')}\n")
        f.write(f"# Validated: {len(validated)} repos\n")
        f.write(f"# Recommended: {len(recommended)}, Stretch: {len(stretch)}, "
                f"Maybe: {len(maybe)}, Skipped: {len(skipped)}\n")
        f.write("# ─────────────────────────────────────────────────────────────────\n")

        if recommended:
            f.write("\n# ═══ RECOMMENDED (score ≥ 60, self-contained, no pipeline gaps) ═\n\n")
            for c in recommended:
                compose_tag = " compose+build" if c.get("has_compose_build") else ""
                df_tag = f" {c.get('dockerfile_count', 0)}df"
                ports = f" ports:{','.join(c.get('expose_ports', []))}" if c.get("expose_ports") else ""
                actionable_tag = " [fixable-issues]" if c.get("actionable") else ""
                f.write(f"# score={c['score']} ⭐{c['stars']} {c.get('language', '?')}"
                        f"{compose_tag}{df_tag}{ports}{actionable_tag}\n")
                for flag in c.get("flags", []):
                    f.write(f"#   {flag}\n")
                f.write(f"{c['url']}\n\n")

        if stretch:
            f.write("\n# ═══ STRETCH (will surface pipeline bugs with clear fix paths) ══\n")
            f.write("# These repos are buildable but exercise features the pipeline\n")
            f.write("# doesn't handle yet. Each 🧪 lists the gap and which file to fix.\n\n")
            for c in stretch:
                compose_tag = " compose+build" if c.get("has_compose_build") else ""
                df_tag = f" {c.get('dockerfile_count', 0)}df"
                ports = f" ports:{','.join(c.get('expose_ports', []))}" if c.get("expose_ports") else ""
                n_gaps = len(c.get('edge_cases', []))
                f.write(f"# score={c['score']} ⭐{c['stars']} {c.get('language', '?')}"
                        f"{compose_tag}{df_tag}{ports} [{n_gaps} pipeline gap(s)]\n")
                for flag in c.get("flags", []):
                    f.write(f"#   {flag}\n")
                for ec in c.get("edge_cases", []):
                    f.write(f"#   🧪 {ec['desc']}\n")
                    f.write(f"#     fix → {ec['fix']}\n")
                f.write(f"{c['url']}\n\n")

        if maybe:
            f.write("\n# ═══ MAYBE (score 40-59, might need manual review) ═════════════\n\n")
            for c in maybe:
                compose_tag = " compose+build" if c.get("has_compose_build") else ""
                df_tag = f" {c.get('dockerfile_count', 0)}df"
                actionable_tag = " [fixable-issues]" if c.get("actionable") else ""
                f.write(f"# score={c['score']} ⭐{c['stars']} {c.get('language', '?')}"
                        f"{compose_tag}{df_tag}{actionable_tag}\n")
                for flag in c.get("flags", []):
                    f.write(f"#   {flag}\n")
                for ec in c.get("edge_cases", []):
                    f.write(f"#   🧪 {ec['desc']} → {ec['fix']}\n")
                f.write(f"{c['url']}\n\n")

        if skipped:
            f.write("\n# ═══ SKIPPED (not suitable) ═══════════════════════════════════\n\n")
            for c in skipped:
                f.write(f"# {c['full_name']}: {c['verdict']}\n")

    print(f"\n📝 Written to {args.out}", file=sys.stderr)

    # ── Summary to stderr ──────────────────────────────────────
    if recommended:
        print("\n── Recommended ──", file=sys.stderr)
        for c in recommended:
            ports = f" ports:{','.join(c.get('expose_ports', []))}" if c.get("expose_ports") else ""
            print(f"  score={c['score']:>3}  ⭐{c['stars']:>6}  "
                  f"{c['full_name']:<45} {c.get('language',''):>12}{ports}",
                  file=sys.stderr)

    if stretch:
        print("\n── Stretch (pipeline bugs to fix) ──", file=sys.stderr)
        for c in stretch:
            gaps = ", ".join(ec["id"] for ec in c.get("edge_cases", []))
            print(f"  score={c['score']:>3}  ⭐{c['stars']:>6}  "
                  f"{c['full_name']:<45} 🧪 {gaps}",
                  file=sys.stderr)

    if maybe:
        print("\n── Maybe ──", file=sys.stderr)
        for c in maybe[:10]:
            print(f"  score={c['score']:>3}  ⭐{c['stars']:>6}  "
                  f"{c['full_name']:<45} {c.get('language',''):>12}",
                  file=sys.stderr)


if __name__ == "__main__":
    main()
