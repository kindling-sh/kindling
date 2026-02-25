#!/usr/bin/env python3
"""
summarize.py — Generate a GitHub Actions step summary from fuzz results.

Usage:
  python3 summarize.py <results-dir> [--mode MODE] [--provider PROVIDER]

Writes Markdown to stdout (pipe into $GITHUB_STEP_SUMMARY).
"""

import argparse
import json
import sys
import os


def print_summary(results_dir, mode="static", provider="openai"):
    """Print Markdown summary to stdout."""
    summary_path = os.path.join(results_dir, "summary.json")
    results_path = os.path.join(results_dir, "results.jsonl")

    if not os.path.exists(summary_path):
        print("❌ No summary.json found — fuzz run may have crashed.")
        return

    with open(summary_path) as f:
        s = json.load(f)

    total = s["total"]
    gen_ok = s["generate_ok"]
    yaml_ok = s["yaml_ok"]
    static_ok = s["static_net_ok"]
    deploy_ok = s.get("deploy_ok", 0)
    e2e_ok = s.get("e2e_ok", 0)
    gen_rate = s["generate_rate"]

    print("## Fuzz Test Results")
    print()
    print("| Metric | Result |")
    print("|--------|--------|")
    print(f"| Mode | `{mode}` |")
    print(f"| Provider | `{provider}` |")
    print(f"| Repos | {total} |")
    print(f"| Generate OK | {gen_ok} / {total} ({gen_rate}) |")
    print(f"| Valid YAML | {yaml_ok} / {gen_ok} |")
    print(f"| Static Analysis Clean | {static_ok} / {yaml_ok} |")
    print(f"| Deploy OK | {deploy_ok} |")
    print(f"| E2E OK | {e2e_ok} |")
    print()

    # Per-repo breakdown
    if os.path.exists(results_path):
        results = {}
        with open(results_path) as f:
            for line in f:
                line = line.strip()
                if not line:
                    continue
                r = json.loads(line)
                repo = r["repo"].split("/")[-1]
                stage = r["stage"]
                if repo not in results:
                    results[repo] = {}
                # For rollout, keep the worst status (fail > partial > pass)
                # and accumulate categories across DSE services
                if stage == "rollout" and stage in results[repo]:
                    existing = results[repo][stage]
                    priority = {"fail": 0, "partial": 1, "pass": 2, "skip": 3}
                    if priority.get(r["status"], 9) < priority.get(existing["status"], 9):
                        # Carry forward the category from the worse entry
                        results[repo][stage] = r
                    elif r.get("category") and not existing.get("category"):
                        existing["category"] = r.get("category", "")
                else:
                    results[repo][stage] = r

        print("### Per-Repo Breakdown")
        print()
        print("| Repo | Generate | YAML | Static | Build | Deploy | Category | Detail |")
        print("|------|----------|------|--------|-------|--------|----------|--------|")

        for repo, stages in sorted(results.items()):
            gen = "✅" if stages.get("generate", {}).get("status") == "pass" else "❌"
            yml = "✅" if stages.get("yaml_validate", {}).get("status") == "pass" else "❌"
            sa = stages.get("static_analysis", {})
            if sa.get("status") == "pass":
                sa_status = "✅"
            elif sa.get("status") == "warn":
                sa_status = "⚠️"
            else:
                sa_status = "❌"

            # Build status
            build_entries = [s for k, s in stages.items() if k == "docker_build"]
            if build_entries:
                b = build_entries[0]
                build_status = "✅" if b.get("status") == "pass" else ("⏭️" if b.get("status") == "skip" else "❌")
            else:
                build_status = "—"

            # Deploy/rollout status
            rollout = stages.get("rollout", {})
            if rollout.get("status") == "pass":
                deploy_status = "✅"
            elif rollout.get("status") == "partial":
                deploy_status = "⚠️"
            elif rollout.get("status") == "fail":
                deploy_status = "❌"
            elif rollout.get("status") == "skip":
                deploy_status = "⏭️"
            else:
                deploy_status = "—"

            category = rollout.get("category", "")
            detail = rollout.get("detail", sa.get("detail", ""))
            # Truncate detail for table readability
            if len(detail) > 80:
                detail = detail[:77] + "..."

            print(f"| {repo} | {gen} | {yml} | {sa_status} | {build_status} | {deploy_status} | {category} | {detail} |")

        # ── Failure category summary ──────────────────────────
        categories = {}
        for repo, stages in results.items():
            rollout = stages.get("rollout", {})
            cat = rollout.get("category", "")
            if cat and rollout.get("status") in ("fail", "partial"):
                categories.setdefault(cat, []).append(repo)

        if categories:
            print()
            print("### Rollout Failure Categories")
            print()
            print("| Category | Count | Repos |")
            print("|----------|-------|-------|")
            for cat, repos in sorted(categories.items(), key=lambda x: -len(x[1])):
                print(f"| `{cat}` | {len(repos)} | {', '.join(repos)} |")


def check_rate(results_dir, threshold):
    """Check generate pass rate against threshold. Exits non-zero if below."""
    summary_path = os.path.join(results_dir, "summary.json")

    if not os.path.exists(summary_path):
        print("::error::No summary found", file=sys.stderr)
        sys.exit(1)

    with open(summary_path) as f:
        s = json.load(f)

    total = s["total"]
    gen_ok = s["generate_ok"]

    if total == 0:
        print("::error::No repos were tested", file=sys.stderr)
        sys.exit(1)

    rate = gen_ok / total * 100
    print(f"Generate pass rate: {rate:.1f}% ({gen_ok}/{total})")

    if rate < threshold:
        print(f"::error::Generate pass rate {rate:.1f}% is below {threshold}% threshold",
              file=sys.stderr)
        sys.exit(1)

    print("✅ Pass rate OK")


# ── Mapping from issue types to source files and suggested actions ──

ISSUE_FILE_MAP = {
    "generate": {
        "files": ["cli/cmd/generate.go", "cli/cmd/genai.go"],
        "action": "Check generate stderr log. If LLM response parsing failed, "
                  "review the prompt and response-cleaning in genai.go. "
                  "If it's a timeout/rate-limit, re-run.",
    },
    "yaml_validate": {
        "files": ["cli/cmd/genai.go"],
        "action": "The LLM output was not valid YAML. Check workflows/<repo>.yml "
                  "for markdown fences or truncation. Tighten the prompt or "
                  "post-processing in genai.go.",
    },
    "port_mismatch": {
        "files": ["cli/cmd/genai.go", "cli/cmd/helpers.go"],
        "action": "The generated port doesn't match the Dockerfile EXPOSE. "
                  "Check if helpers.go correctly parses EXPOSE and whether "
                  "the LLM prompt includes port information.",
    },
    "dangling_service_ref": {
        "files": ["cli/cmd/genai.go", "test/fuzz/analyze.py"],
        "action": "A service references another service that wasn't generated. "
                  "Check if the LLM prompt includes all services from docker-compose.",
    },
    "missing_dependency": {
        "files": ["cli/cmd/genai.go"],
        "action": "A database/cache dependency was not declared. Check if the "
                  "docker-compose shows the dependency and whether the prompt "
                  "instructs the LLM to extract dependencies.",
    },
    "missing_health_check": {
        "files": [],
        "action": "Expected — most repos don't expose /healthz. No fix needed.",
    },
    "docker_build": {
        "files": ["test/fuzz/fix-dockerfile.py"],
        "action": "Docker build failed even after LLM fix attempt. Check "
                  "logs/<repo>.build.<service>.log. Usually repo-specific, "
                  "not a kindling bug unless pattern repeats across repos.",
    },
    "rollout": {
        "files": ["cli/cmd/genai.go", "api/v1alpha1/devstagingenvironment_types.go"],
        "action": "Pods didn't become Ready. Check pod logs for crash reasons. "
                  "Verify env vars and dependency resolution in the DSE manifest.",
    },
    "crash_loop": {
        "files": ["cli/cmd/genai.go", "test/fuzz/fix-dockerfile.py"],
        "action": "Container crashes on startup. Check if the entrypoint/CMD "
                  "requires args, env vars, or a config file not present. "
                  "Review pod logs in the diagnostics block.",
    },
    "missing_dependency": {
        "files": ["cli/cmd/genai.go"],
        "action": "App can't reach a backing service (DB, Redis, etc.). Check "
                  "if the LLM-generated workflow includes the dependency and "
                  "whether the DSE manifest wires env vars to the right hostname.",
    },
    "missing_database": {
        "files": ["cli/cmd/genai.go"],
        "action": "Database exists but is not initialised (missing schema/migrations). "
                  "Consider adding an init container or startup migration step.",
    },
    "image_pull": {
        "files": ["test/fuzz/run.sh", "test/fuzz/analyze.py"],
        "action": "Image wasn't found in the cluster. Check kind load step or "
                  "whether the DSE image tag matches what was built.",
    },
    "config_error": {
        "files": ["cli/cmd/genai.go"],
        "action": "Missing ConfigMap, Secret, or env var reference. Check the "
                  "DSE manifest for references to objects that don't exist.",
    },
    "oom_killed": {
        "files": [],
        "action": "Container was OOMKilled. The app needs more memory than the "
                  "default resource limits. Not a kindling bug.",
    },
    "app_crash": {
        "files": ["cli/cmd/genai.go", "test/fuzz/fix-dockerfile.py"],
        "action": "App crashed on startup (panic, uncaught exception, missing module). "
                  "Check if the Dockerfile or entrypoint is correct for this repo.",
    },
    "e2e": {
        "files": ["cli/cmd/genai.go"],
        "action": "Networking check failed. Verify port and service name in "
                  "the generated DSE manifest match the actual app.",
    },
}


def write_action_items(results_dir, output_path=None):
    """Analyze results and write a structured action-items.json for Copilot."""
    results_path = os.path.join(results_dir, "results.jsonl")
    if output_path is None:
        output_path = os.path.join(results_dir, "action-items.json")

    if not os.path.exists(results_path):
        with open(output_path, "w") as f:
            json.dump([], f)
        return

    items = []

    with open(results_path) as f:
        for line in f:
            line = line.strip()
            if not line:
                continue
            r = json.loads(line)

            repo = r["repo"].split("/")[-1]
            stage = r["stage"]
            status = r["status"]
            detail = r.get("detail", "")
            issues = r.get("issues", [])

            # Generate or YAML failures are always actionable
            if status == "fail" and stage in ("generate", "yaml_validate"):
                info = ISSUE_FILE_MAP.get(stage, {})
                items.append({
                    "severity": "error",
                    "repo": repo,
                    "stage": stage,
                    "summary": f"{stage} failed for {repo}",
                    "detail": detail,
                    "files_to_check": info.get("files", []),
                    "suggested_action": info.get("action", "Investigate the failure."),
                })

            # Docker build / rollout failures
            elif status == "fail" and stage in ("docker_build", "rollout", "e2e"):
                category = r.get("category", stage)
                info = ISSUE_FILE_MAP.get(category, ISSUE_FILE_MAP.get(stage, {}))
                items.append({
                    "severity": "warning",
                    "repo": repo,
                    "stage": stage,
                    "category": category,
                    "summary": f"{stage} failed for {repo} [{category}]",
                    "detail": detail,
                    "files_to_check": info.get("files", []),
                    "suggested_action": info.get("action", "Investigate the failure."),
                })

            # Static analysis issues (skip pure info-level missing_health_check)
            elif stage == "static_analysis" and issues:
                for issue in issues:
                    itype = issue.get("type", "unknown")
                    severity = issue.get("severity", "info")

                    # missing_health_check is noise — only include if there
                    # are other, more serious issues for the same repo
                    if itype == "missing_health_check":
                        continue

                    info = ISSUE_FILE_MAP.get(itype, {})
                    items.append({
                        "severity": "warning" if severity != "info" else "info",
                        "repo": repo,
                        "stage": stage,
                        "summary": f"{itype}: {issue.get('detail', '')}",
                        "detail": f"Service: {issue.get('service', '?')} — {issue.get('detail', '')}",
                        "files_to_check": info.get("files", []),
                        "suggested_action": info.get("action", "Review the generated manifest."),
                    })

    # Sort: errors first, then warnings, then info
    severity_order = {"error": 0, "warning": 1, "info": 2}
    items.sort(key=lambda x: severity_order.get(x["severity"], 9))

    with open(output_path, "w") as f:
        json.dump(items, f, indent=2)

    print(f"Wrote {len(items)} action item(s) to {output_path}", file=sys.stderr)


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description="Summarize fuzz test results")
    parser.add_argument("results_dir", help="Path to results directory")
    parser.add_argument("mode", nargs="?", default="static", help="Run mode")
    parser.add_argument("provider", nargs="?", default="openai", help="LLM provider")
    parser.add_argument("--check-rate", type=float, metavar="THRESHOLD",
                        help="Check generate pass rate >= THRESHOLD%% and exit non-zero if below")
    parser.add_argument("--action-items", action="store_true",
                        help="Write action-items.json to the results directory")
    args = parser.parse_args()

    if args.check_rate is not None:
        check_rate(args.results_dir, args.check_rate)
    elif args.action_items:
        write_action_items(args.results_dir)
    else:
        print_summary(args.results_dir, args.mode, args.provider)
