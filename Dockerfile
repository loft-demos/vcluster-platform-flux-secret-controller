# syntax=docker/dockerfile:1.7

FROM --platform=$BUILDPLATFORM golang:1.23-alpine AS build
WORKDIR /app

# Defaults so local (non-buildx) builds also work
ARG TARGETOS=linux
ARG TARGETARCH=amd64
ARG GOPRIVATE
ENV GOPRIVATE=${GOPRIVATE}
# Reliable proxy avoids odd 403/429 when hitting VCS directly
ENV GOPROXY=https://proxy.golang.org,direct

# 1) Prime module cache
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

# 2) Copy ALL sources
COPY . .

# 2.5) Ensure go.sum matches current imports
RUN --mount=type=cache,target=/go/pkg/mod go mod tidy

RUN go version && go env

# 3) Print layout, then build (cache build artifacts)
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build <<'EOF'
set -eux
echo "TARGETOS=${TARGETOS} TARGETARCH=${TARGETARCH}"
echo "Root files:"; ls -la
echo "cmd tree:"; [ -d cmd ] && find cmd -maxdepth 2 -type f -name "*.go" -print || true
echo "go list:"; go list ./... || true

BUILD_PATH=./cmd/manager

echo "Building: ${BUILD_PATH}"

CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} \
  go build -v -trimpath -ldflags="-s -w" \
    -o /out/vcluster-platform-flux-secret-controller "${BUILD_PATH}"
EOF

FROM gcr.io/distroless/static:nonroot AS runtime
WORKDIR /
COPY --from=build /out/vcluster-platform-flux-secret-controller /vcluster-platform-flux-secret-controller
USER nonroot:nonroot
ENTRYPOINT ["/vcluster-platform-flux-secret-controller"]
