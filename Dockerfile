# syntax=docker/dockerfile:1

# Builder: compile a static binary. modernc.org/sqlite is pure Go, so CGO is off.
FROM golang:1.27.1-alpine@sha256:8a5910f31396cd4d89662f56c68b3ae31d374308270a1c3bd96672ee5ed43414 AS build

WORKDIR /src
ENV CGO_ENABLED=0

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go build -trimpath -ldflags="-s -w" -o /out/relay ./cmd/relay

# The SQLite file lives here. Distroless has no shell to create it, so the
# directory is made in the builder and owned by the runtime user.
RUN mkdir -p /out/data

# Runtime: distroless static, no shell, no package manager. The nonroot user
# is uid/gid 65532.
FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab

LABEL org.opencontainers.image.title="viku-apn-relay" \
      org.opencontainers.image.description="Relays Vikunja webhook deliveries to iOS devices through APNs" \
      org.opencontainers.image.source="https://github.com/Seergs/viku-apn-relay" \
      org.opencontainers.image.licenses="NOASSERTION"

COPY --from=build --chown=65532:65532 /out/data /data
COPY --from=build /out/relay /usr/local/bin/relay

ENV ADDR=:8080 \
    DATABASE_PATH=/data/relay.db

WORKDIR /data
USER 65532:65532
EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/relay"]
