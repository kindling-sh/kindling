#!/usr/bin/env python3
"""
find-repos.py — Search GitHub for multi-service repos with Dockerfiles
that are good candidates for kindling generate fuzz testing.

Criteria:
  1. Has a docker-compose.yml with "build:" directives (multi-service, buildable)
  2. OR has multiple Dockerfiles in subdirectories (microservice layout)
  3. Not archived, not a fork, has recent activity
  4. Reasonable size (not a monorepo monster)

Usage:
  python3 find-repos.py [--token GITHUB_TOKEN] [--out repos.txt] [--limit 100]

Without a token you get 10 search requests/min. With a token: 30/min.
"""

import argparse
import json
import os
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


def check_repo_has_compose_with_build(token: str | None, owner: str, repo: str) -> bool:
    """Check if the repo's docker-compose.yml has build: directives."""
    # Try to fetch docker-compose.yml content
    for name in ["docker-compose.yml", "docker-compose.yaml", "compose.yml", "compose.yaml"]:
        data = api_get(f"/repos/{owner}/{repo}/contents/{name}", token)
        if "content" not in data:
            continue
        import base64
        try:
            content = base64.b64decode(data["content"]).decode("utf-8", errors="replace")
            if "build:" in content or "build :" in content:
                return True
        except Exception:
            continue
    return False


def count_dockerfiles(token: str | None, owner: str, repo: str) -> int:
    """Count Dockerfiles in the repo using search."""
    data = api_get("/search/code", token, {
        "q": f"filename:Dockerfile repo:{owner}/{repo}",
        "per_page": 1,
    })
    return data.get("total_count", 0)


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


def main():
    parser = argparse.ArgumentParser(description="Find repos for kindling fuzz testing")
    parser.add_argument("--token", default=os.environ.get("GITHUB_TOKEN", ""),
                        help="GitHub API token (or set GITHUB_TOKEN env var)")
    parser.add_argument("--out", default="/Users/jeffvincent/dev/kindling/test/fuzz/repos-candidates.txt",
                        help="Output file path")
    parser.add_argument("--limit", type=int, default=100,
                        help="Max repos to output")
    args = parser.parse_args()

    token = args.token or None
    if not token:
        print("⚠️  No GitHub token — rate limited to 10 search requests/min", file=sys.stderr)
        print("   Set GITHUB_TOKEN or pass --token for 30/min", file=sys.stderr)

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
                if info and info["full_name"] not in seen:
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
                if info and info["full_name"] not in seen:
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
                if info and info["full_name"] not in seen:
                    seen.add(info["full_name"])
                    info["source"] = f"compose-{lang}"
                    candidates.append(info)
                    lang_count += 1
        print(f" +{lang_count}", file=sys.stderr, flush=True)
        time.sleep(1)

    print(f"\n📊 Total unique candidates: {len(candidates)}", file=sys.stderr)

    # ── Validate: check each candidate for docker-compose with build ───────
    # Cap validation to avoid hanging on huge candidate lists
    max_to_check = min(len(candidates), args.limit * 3)  # check 3x limit, then stop
    print(f"\n🔬 Validating up to {max_to_check} candidates (checking for docker-compose + Dockerfiles)...",
          file=sys.stderr)

    validated = []
    for i, c in enumerate(candidates[:max_to_check]):
        if len(validated) >= args.limit:
            print(f"\n  🎯 Reached target of {args.limit} validated repos, stopping early.",
                  file=sys.stderr)
            break

        owner, repo = c["full_name"].split("/", 1)
        print(f"  [{i+1}/{max_to_check}] Checking {c['full_name']}...",
              file=sys.stderr, end="", flush=True)

        # Check for docker-compose with build directives
        has_compose = check_repo_has_compose_with_build(token, owner, repo)
        time.sleep(0.5)  # rate limit safety

        # Only count Dockerfiles if compose check failed (save an API call)
        n_dockerfiles = 0
        if not has_compose:
            n_dockerfiles = count_dockerfiles(token, owner, repo)
            time.sleep(0.5)

        c["has_compose_build"] = has_compose
        c["dockerfile_count"] = n_dockerfiles

        # Accept if it has compose with build, or multiple Dockerfiles
        if has_compose or n_dockerfiles >= 2:
            validated.append(c)
            status = "✅"
        else:
            status = "⏭️ "

        print(
            f" {status} compose_build={has_compose}, dockerfiles={n_dockerfiles}",
            file=sys.stderr, flush=True
        )

    print(f"\n✅ Validated: {len(validated)} repos", file=sys.stderr)

    # ── Sort: prefer repos with compose+build, then by dockerfile count, then stars ──
    validated.sort(key=lambda x: (
        x.get("has_compose_build", False),
        x.get("dockerfile_count", 0),
        x.get("stars", 0),
    ), reverse=True)

    # ── Write output ───────────────────────────────────────────────────────
    with open(args.out, "w") as f:
        f.write("# ─────────────────────────────────────────────────────────────────\n")
        f.write("# repos-candidates.txt — Auto-discovered repos for fuzz testing\n")
        f.write(f"# Generated: {time.strftime('%Y-%m-%d %H:%M:%S')}\n")
        f.write(f"# Total: {len(validated)} repos\n")
        f.write("# Criteria: has docker-compose with build OR ≥2 Dockerfiles\n")
        f.write("# ─────────────────────────────────────────────────────────────────\n\n")

        current_lang = None
        for c in validated:
            lang = c.get("language") or "Unknown"
            if lang != current_lang:
                current_lang = lang
                f.write(f"\n# ── {lang} {'─' * (55 - len(lang))}\n")

            compose_tag = " [compose+build]" if c.get("has_compose_build") else ""
            df_tag = f" [{c['dockerfile_count']} Dockerfiles]" if c.get("dockerfile_count", 0) >= 2 else ""
            f.write(f"{c['url']}  # ⭐{c['stars']}{compose_tag}{df_tag}\n")

    print(f"\n📝 Written to {args.out}", file=sys.stderr)

    # Also print a summary to stderr
    print("\n── Top 20 ──", file=sys.stderr)
    for c in validated[:20]:
        compose_tag = " [compose+build]" if c.get("has_compose_build") else ""
        df_tag = f" [{c['dockerfile_count']}df]" if c.get("dockerfile_count", 0) >= 2 else ""
        print(f"  ⭐{c['stars']:>6}  {c['full_name']:<45} {c.get('language',''):>12}{compose_tag}{df_tag}",
              file=sys.stderr)


if __name__ == "__main__":
    main()
