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
                results[repo][stage] = r

        print("### Per-Repo Breakdown")
        print()
        print("| Repo | Generate | YAML | Static | Issues |")
        print("|------|----------|------|--------|--------|")

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
            issues = sa.get("detail", "")
            print(f"| {repo} | {gen} | {yml} | {sa_status} | {issues} |")


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


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description="Summarize fuzz test results")
    parser.add_argument("results_dir", help="Path to results directory")
    parser.add_argument("mode", nargs="?", default="static", help="Run mode")
    parser.add_argument("provider", nargs="?", default="openai", help="LLM provider")
    parser.add_argument("--check-rate", type=float, metavar="THRESHOLD",
                        help="Check generate pass rate >= THRESHOLD%% and exit non-zero if below")
    args = parser.parse_args()

    if args.check_rate is not None:
        check_rate(args.results_dir, args.check_rate)
    else:
        print_summary(args.results_dir, args.mode, args.provider)
