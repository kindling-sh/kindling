#!/usr/bin/env python3
"""
analyze.py — Parse a kindling-generated workflow YAML and cross-validate
networking between services.

Reports:
  - Port mismatches: env var URL port ≠ target service's declared port
  - Dangling refs:   env var URL references a service not in the workflow
  - Dockerfile port mismatches: EXPOSE port ≠ declared port
  - Missing health check paths

Output: JSON to stdout
  {
    "services_count": N,
    "services": [...],
    "issues": [{"severity": "...", "service": "...", "detail": "..."}, ...]
  }
"""

import json
import os
import re
import sys
from pathlib import Path

import yaml


def parse_workflow(path: str) -> dict:
    with open(path) as f:
        return yaml.safe_load(f)


def extract_services(data: dict) -> list[dict]:
    """Pull out every kindling-deploy step as a service record."""
    services = []
    for job_name, job in (data.get("jobs") or {}).items():
        for step in job.get("steps") or []:
            uses = step.get("uses", "")
            if "kindling-deploy" not in uses:
                continue
            w = step.get("with") or {}
            name_raw = w.get("name", "")
            # Strip ${{ github.actor }}- prefix pattern
            name_clean = re.sub(
                r'\$\{\{\s*github\.actor\s*\}\}-', '', name_raw
            )
            # Parse env list (YAML string of list)
            env_vars = {}
            env_raw = w.get("env", "")
            if env_raw:
                try:
                    env_list = yaml.safe_load(env_raw)
                    if isinstance(env_list, list):
                        for e in env_list:
                            if isinstance(e, dict):
                                env_vars[e.get("name", "")] = e.get("value", "")
                except Exception:
                    pass

            # Parse dependencies
            deps = []
            dep_raw = w.get("dependencies", "")
            if dep_raw:
                try:
                    dep_list = yaml.safe_load(dep_raw)
                    if isinstance(dep_list, list):
                        deps = dep_list
                except Exception:
                    pass

            services.append({
                "name": name_clean,
                "name_raw": name_raw,
                "port": w.get("port", ""),
                "health_check_path": w.get("health-check-path", ""),
                "context": w.get("context", "").replace(
                    "${{ github.workspace }}/", ""
                ).replace("${{ github.workspace }}", "."),
                "image": w.get("image", ""),
                "env": env_vars,
                "dependencies": deps,
                "ingress_host": w.get("ingress-host", ""),
            })

    return services


def extract_builds(data: dict) -> list[dict]:
    """Pull out every kindling-build step."""
    builds = []
    for job_name, job in (data.get("jobs") or {}).items():
        for step in job.get("steps") or []:
            uses = step.get("uses", "")
            if "kindling-build" not in uses:
                continue
            w = step.get("with") or {}
            builds.append({
                "name": w.get("name", ""),
                "context": w.get("context", "").replace(
                    "${{ github.workspace }}/", ""
                ).replace("${{ github.workspace }}", "."),
                "dockerfile": w.get("dockerfile", ""),
                "image": w.get("image", ""),
            })
    return builds


def resolve_dockerfile(clone_dir: str, context: str, dockerfile: str) -> Path | None:
    """Resolve the Dockerfile path for a build step.

    If the build step specifies a 'dockerfile' field (e.g. "backend/Dockerfile"),
    resolve it relative to the context directory. Otherwise fall back to
    <context>/Dockerfile.
    """
    base = Path(clone_dir) / context
    if dockerfile:
        # dockerfile field is relative to context
        candidate = base / dockerfile
        if candidate.exists():
            return candidate
        # Also try relative to clone_dir (some workflows use repo-relative paths)
        candidate = Path(clone_dir) / dockerfile
        if candidate.exists():
            return candidate
        return None
    # Default: look for Dockerfile at context root
    candidate = base / "Dockerfile"
    if candidate.exists():
        return candidate
    candidate = base / "dockerfile"
    if candidate.exists():
        return candidate
    return None


def get_dockerfile_expose(clone_dir: str, context: str,
                          dockerfile_field: str = "") -> list[str]:
    """Read EXPOSE directives from a Dockerfile."""
    dockerfile = resolve_dockerfile(clone_dir, context, dockerfile_field)
    if dockerfile is None:
        return []
    ports = []
    try:
        for line in dockerfile.read_text().splitlines():
            if re.match(r'^\s*EXPOSE\s', line, re.IGNORECASE):
                for token in line.split()[1:]:
                    port = re.match(r'(\d+)', token)
                    if port:
                        ports.append(port.group(1))
    except Exception:
        pass
    return ports


def validate_networking(services: list[dict], builds: list[dict],
                        clone_dir: str) -> list[dict]:
    """Cross-validate service networking."""
    issues = []

    # Build a lookup: service_name -> service
    svc_by_name = {}
    for svc in services:
        svc_by_name[svc["name"]] = svc

    # Also build a list of known service name fragments
    svc_names = set(svc_by_name.keys())

    for svc in services:
        # ── Check env var URLs reference real services with correct ports ──
        for env_name, env_value in svc.get("env", {}).items():
            # Match patterns like http://xxx-orders:5000 or redis://xxx-redis:6379
            url_match = re.search(
                r'(?:https?|redis|mongodb|amqp|grpc)://([^:/\s]+):(\d+)',
                env_value
            )
            if not url_match:
                # Also check for host:port without scheme
                url_match = re.search(r'([a-zA-Z][\w.-]*):(\d+)', env_value)
            if not url_match:
                continue

            target_host = url_match.group(1)
            target_port = url_match.group(2)

            # Strip ${{ github.actor }}- prefix
            target_host_clean = re.sub(
                r'\$\{\{\s*github\.actor\s*\}\}-', '', target_host
            )

            # Find which service this references
            target_svc = None
            for sn in svc_names:
                # Match if the target host ends with the service name
                # e.g. "user-orders" matches service "orders"
                # or exact match like "orders-redis" for dependency
                if target_host_clean == sn or target_host_clean.endswith(f"-{sn}"):
                    target_svc = svc_by_name[sn]
                    break

            if target_svc:
                # Check port matches
                declared_port = target_svc.get("port", "")
                if declared_port and target_port != declared_port:
                    issues.append({
                        "severity": "error",
                        "service": svc["name"],
                        "type": "port_mismatch",
                        "detail": (
                            f"Env {env_name} references {target_host_clean}:{target_port} "
                            f"but service '{target_svc['name']}' declares port {declared_port}"
                        ),
                    })
            else:
                # Check if it might be a dependency (redis, postgres, etc.)
                dep_suffixes = ["redis", "postgres", "postgresql", "mongodb",
                                "mongo", "mysql", "rabbitmq", "nats", "kafka"]
                is_dep = any(target_host_clean.endswith(s) for s in dep_suffixes)

                if not is_dep:
                    issues.append({
                        "severity": "warning",
                        "service": svc["name"],
                        "type": "dangling_ref",
                        "detail": (
                            f"Env {env_name} references '{target_host_clean}' "
                            f"which is not a declared service in the workflow"
                        ),
                    })

        # ── Check Dockerfile EXPOSE matches declared port ──────────
        if svc.get("context") and svc.get("port"):
            # Find the matching build to get the dockerfile field
            svc_df = ""
            for b in builds:
                if b.get("name") == svc["name"]:
                    svc_df = b.get("dockerfile", "")
                    break
            expose_ports = get_dockerfile_expose(
                clone_dir, svc["context"], svc_df)
            if expose_ports and svc["port"] not in expose_ports:
                issues.append({
                    "severity": "warning",
                    "service": svc["name"],
                    "type": "expose_mismatch",
                    "detail": (
                        f"Service declares port {svc['port']} but "
                        f"Dockerfile EXPOSEs {', '.join(expose_ports)}"
                    ),
                })

        # ── Check health check path is set ─────────────────────────
        if not svc.get("health_check_path"):
            issues.append({
                "severity": "info",
                "service": svc["name"],
                "type": "missing_health_check",
                "detail": "No health-check-path specified",
            })

    # ── Check build contexts have Dockerfiles ──────────────────────
    for build in builds:
        ctx = build.get("context", "")
        df_field = build.get("dockerfile", "")
        resolved = resolve_dockerfile(clone_dir, ctx or ".", df_field)
        if resolved is None:
            if df_field:
                detail = f"No Dockerfile found at {df_field} (context: {ctx or '.'})"
            else:
                detail = f"No Dockerfile found at {ctx or '.'}/Dockerfile"
            issues.append({
                "severity": "error",
                "service": build["name"],
                "type": "missing_dockerfile",
                "detail": detail,
            })

    return issues


def services_to_dse_manifests(
    services: list[dict],
    builds: list[dict],
    prefix: str = "fuzz",
    image_registry: str = "localhost",
) -> list[dict]:
    """Convert parsed workflow services into DevStagingEnvironment CRs.

    Each kindling-deploy step becomes one DSE manifest that can be
    kubectl-applied directly into a Kind cluster with the kindling
    operator running.

    The images are expected to already be built and loaded via
    `docker build` + `kind load docker-image`.
    """
    # Map build context → image tag we'll use locally
    build_ctx_to_image = {}
    for b in builds:
        ctx = b.get("context", "")
        name = b.get("name", "")
        # We'll use a predictable local tag: fuzz-<name>:test
        local_tag = f"{prefix}-{name}:test"
        build_ctx_to_image[ctx] = local_tag
        # Also index by name for fallback matching
        build_ctx_to_image[name] = local_tag

    manifests = []
    for svc in services:
        name = svc["name"]
        port = int(svc.get("port") or 8080)
        health_path = svc.get("health_check_path", "/healthz") or "/healthz"

        # Resolve the local image tag
        ctx = svc.get("context", "")
        image = build_ctx_to_image.get(ctx) or build_ctx_to_image.get(name)
        if not image:
            # Fallback: construct from service name
            image = f"{prefix}-{name}:test"

        # Build env list — strip GitHub Actions template expressions
        env_list = []
        for env_name, env_value in svc.get("env", {}).items():
            # Replace ${{ github.actor }}-<svc> with just <svc>
            # (in a fuzz cluster there's no actor prefix)
            clean_value = re.sub(
                r'\$\{\{\s*github\.actor\s*\}\}-', '', env_value
            )
            # Replace any remaining ${{ ... }} with empty string
            clean_value = re.sub(r'\$\{\{[^}]*\}\}', '', clean_value)
            env_list.append({"name": env_name, "value": clean_value})

        # Build dependencies list
        deps = []
        for dep in svc.get("dependencies", []):
            d = {"type": dep.get("type", "")}
            if dep.get("version"):
                d["version"] = dep["version"]
            deps.append(d)

        # Build the DSE manifest
        dse = {
            "apiVersion": "apps.example.com/v1alpha1",
            "kind": "DevStagingEnvironment",
            "metadata": {
                "name": name,
                "labels": {
                    "app.kubernetes.io/managed-by": "kindling-fuzz",
                    "kindling-fuzz/prefix": prefix,
                },
            },
            "spec": {
                "deployment": {
                    "image": image,
                    "replicas": 1,
                    "port": port,
                    "healthCheck": {
                        "path": health_path,
                        "initialDelaySeconds": 10,
                        "periodSeconds": 5,
                    },
                },
                "service": {
                    "port": port,
                    "type": "ClusterIP",
                },
            },
        }

        if env_list:
            dse["spec"]["deployment"]["env"] = env_list

        if deps:
            dse["spec"]["dependencies"] = deps

        # Add ingress if the service had one
        if svc.get("ingress_host"):
            host = re.sub(r'\$\{\{[^}]*\}\}', 'fuzz', svc["ingress_host"])
            dse["spec"]["ingress"] = {
                "enabled": True,
                "host": host,
                "ingressClassName": "nginx",
            }

        manifests.append(dse)

    return manifests


def emit_dse_yaml(manifests: list[dict]) -> str:
    """Render DSE manifests as a multi-document YAML string."""
    docs = []
    for m in manifests:
        docs.append(yaml.dump(m, default_flow_style=False, sort_keys=False))
    return "---\n".join(docs)


def main():
    import argparse

    parser = argparse.ArgumentParser(
        description="Analyze kindling-generated workflows and optionally emit DSE CRs"
    )
    parser.add_argument("workflow", help="Path to generated workflow YAML")
    parser.add_argument("clone_dir", help="Path to the cloned repo")
    parser.add_argument(
        "--emit-dse", dest="emit_dse", metavar="OUTFILE",
        help="Write DSE CR manifests to this file (- for stdout)",
    )
    parser.add_argument(
        "--prefix", default="fuzz",
        help="Name prefix for DSE resources and image tags (default: fuzz)",
    )
    args = parser.parse_args()

    data = parse_workflow(args.workflow)
    services = extract_services(data)
    builds = extract_builds(data)
    issues = validate_networking(services, builds, args.clone_dir)

    result = {
        "services_count": len(services),
        "services": services,
        "builds": [{"name": b["name"], "context": b["context"]} for b in builds],
        "issues": issues,
    }

    # Always output analysis JSON to stdout
    # (When --emit-dse uses stdout, analysis goes to stderr instead)
    if args.emit_dse and args.emit_dse == "-":
        json.dump(result, sys.stderr, indent=2)
        print(file=sys.stderr)  # trailing newline
    else:
        json.dump(result, sys.stdout, indent=2)
        print()  # trailing newline

    # Emit DSE manifests if requested
    if args.emit_dse:
        manifests = services_to_dse_manifests(
            services, builds, prefix=args.prefix,
        )
        dse_yaml = emit_dse_yaml(manifests)
        if args.emit_dse == "-":
            print(dse_yaml)
        else:
            with open(args.emit_dse, "w") as f:
                f.write(dse_yaml)


if __name__ == "__main__":
    main()
