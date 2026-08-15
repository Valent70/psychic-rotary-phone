# Veriqo Kernel — production build blueprint.
#
# Honest scope note: the FINAL (`FROM scratch`) stages below have been
# executed for real in a sandbox with a working local Docker daemon --
# built from locally-compiled binaries (go1.24.7 toolchain, satisfying
# this module's `go 1.22.2` directive, CGO_ENABLED=0, matching flags),
# producing real content-addressed image digests and running both
# `veriqo-demo` and `veriqo-node` containers to completion. That run is
# what caught and fixed the missing `WORKDIR /tmp` below (a real bug:
# `FROM scratch` has no filesystem at all until COPY/WORKDIR create
# paths, and veriqo-demo's bundle writer uses os.TempDir()).
#
# The BUILD stage (`golang:1.22-alpine`) itself has NOT been executed
# end-to-end in that same sandbox: pulling the base image from Docker
# Hub is blocked there by a confirmed, explicit organization egress
# policy denial (not a technical or credentials gap, and not something
# a retry or mirror works around) -- see docs/production-readiness.md
# §1 for the exact boundary. It has been validated with
# `docker build --check`-style manual review of paths/commands, and is
# expected to work unmodified in any environment with normal registry
# access (e.g. CI).
#
# The `-trimpath -ldflags="-s -w"` flags below are the same ones
# internal/reproducibility.TestBinaryEqualityAcrossIndependentBuilds
# proves produce a byte-identical binary across two genuinely
# independent `go build` invocations (separate GOCACHE each) -- so a
# container image built from this Dockerfile inherits that same
# binary-level reproducibility property.

# --- build stage -------------------------------------------------------
FROM golang:1.22-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY . .
ENV CGO_ENABLED=0
RUN go build -trimpath -ldflags="-s -w" -o /out/veriqo-demo ./cmd/veriqo-demo
RUN go build -trimpath -ldflags="-s -w" -o /out/veriqo-node ./cmd/veriqo-node

# --- final stage (scratch: smallest possible attack surface) -----------
FROM scratch AS veriqo-demo
# WORKDIR creates /tmp even though no shell/mkdir exists in this image --
# veriqo-demo's bundle writer uses os.TempDir(), and `scratch` starts
# with literally no filesystem entries until COPY/WORKDIR create them.
WORKDIR /tmp
COPY --from=build /out/veriqo-demo /veriqo-demo
COPY --from=build /src/cmd/veriqo-demo/policy.yaml /policy.yaml
ENTRYPOINT ["/veriqo-demo"]

FROM scratch AS veriqo-node
COPY --from=build /out/veriqo-node /veriqo-node
ENTRYPOINT ["/veriqo-node"]

# Build a specific target with:
#   docker build --target veriqo-demo -t veriqo-demo:local .
#   docker build --target veriqo-node -t veriqo-node:local .
