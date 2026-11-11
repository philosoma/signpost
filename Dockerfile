# syntax=docker/dockerfile:1.7
# The build stage runs on the build machine and cross-compiles, so an arm64
# image needs no emulation. The binary is static and has no third-party modules.
FROM --platform=$BUILDPLATFORM golang:1.26.9-alpine@sha256:3082400e369fa24d5fc60bca20edab3f6d604e0c5a690ec66b295eff4dd87ade AS build
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
WORKDIR /src
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w -X main.version=$VERSION" -o /out/signpost ./cmd/signpost

# Includes CA roots (Let's Encrypt surfaces are probed over TLS) and runs as uid 65532.
FROM cgr.dev/chainguard/static@sha256:fc42af33e643f20d08baf03cddcc1c02ba57052dfb02edb0d79f3fc47bcea8d9
COPY --from=build /out/signpost /app/signpost
EXPOSE 8080
ENTRYPOINT ["/app/signpost"]
