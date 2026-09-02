# Build-agent sidecar image: kubectl (base) + helm + crane + the kindling
# CLI. Used by CIRunnerPool pods with spec.enableSnapshotDeploy: true so
# their .snapshot-deploy signal handler can run `kindling snapshot --deploy`
# end-to-end -- crane copy from the local in-cluster registry, helm install
# against a separate staging cluster. See snapshot-deploy-runner-sidecar.md
# for the full design.
#
# Pools that don't set enableSnapshotDeploy keep using the plain
# bitnami/kubectl:latest image (internal/controller/cirunnerpool_controller.go,
# defaultBuildAgentImage) -- this image is additive, never a replacement.
#
# Build from the repo root (not this directory), since it needs both cli/
# and pkg/ci/ (cli/go.mod references pkg/ci via a relative replace directive):
#
#   docker build -f hack/build-agent/Dockerfile -t ghcr.io/kindling-sh/build-agent:latest .

FROM alpine/helm:3 AS helm
FROM gcr.io/go-containerregistry/crane:latest AS crane

FROM golang:1.25 AS kindling-build
WORKDIR /workspace

# Cache module downloads before copying full sources.
COPY pkg/ci/ pkg/ci/
COPY cli/go.mod cli/go.sum cli/
RUN cd cli && go mod download

COPY cli/ cli/
RUN cd cli && CGO_ENABLED=0 GOOS=linux go build -o /kindling .

FROM bitnami/kubectl:latest

LABEL org.opencontainers.image.source="https://github.com/kindling-sh/kindling"
LABEL org.opencontainers.image.description="kindling CI runner build-agent sidecar (kubectl + helm + crane + kindling CLI)"
LABEL org.opencontainers.image.licenses="Apache-2.0"

COPY --from=helm /usr/bin/helm /usr/local/bin/helm
COPY --from=crane /ko-app/crane /usr/local/bin/crane
COPY --from=kindling-build /kindling /usr/local/bin/kindling
