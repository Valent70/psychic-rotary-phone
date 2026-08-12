# Veriqo Kernel — production build blueprint.
#
# Honest scope note: this Dockerfile has been written to match the
# repository's actual module layout and has been validated with
# `docker build --check`-style manual review of paths/commands, but has
# NOT been executed with a real `docker build` in this sandbox (no
# Docker daemon / no registry pull access here). Treat this as a
# reviewed blueprint, not a proven artifact — build and smoke-test it in
# your own CI before relying on it.

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
COPY --from=build /out/veriqo-demo /veriqo-demo
COPY --from=build /src/cmd/veriqo-demo/policy.yaml /policy.yaml
ENTRYPOINT ["/veriqo-demo"]

FROM scratch AS veriqo-node
COPY --from=build /out/veriqo-node /veriqo-node
ENTRYPOINT ["/veriqo-node"]

# Build a specific target with:
#   docker build --target veriqo-demo -t veriqo-demo:local .
#   docker build --target veriqo-node -t veriqo-node:local .
