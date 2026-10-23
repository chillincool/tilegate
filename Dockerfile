# syntax=docker/dockerfile:1

# Build on the runner's native platform and cross-compile for the target, so a
# multi-arch build needs no emulation.
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
ARG TARGETOS TARGETARCH
ARG VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/tilegate ./cmd/tilegate
# An empty /data owned by the runtime user, so a fresh volume mounted there
# (for the SQLite favourites database) is writable.
RUN mkdir -p /out/data

# distroless/static: no shell or package manager, runs as uid 65532.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/tilegate /tilegate
COPY --from=build --chown=65532:65532 /out/data /data
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s CMD ["/tilegate", "healthcheck"]
ENTRYPOINT ["/tilegate"]
