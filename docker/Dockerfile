# filex default image (~600-700 MB)
# - Pure Go binary (CGO=0, modernc.org/sqlite)
# - + ffmpeg (video thumbs)
# - + ghostscript + poppler-utils (PDF → JPEG, page 1)
# - + libreoffice (headless soffice for .doc/.xls/.ppt/.odt/.ods/.odp → PDF → JPEG)
# - + ttf-liberation + ttf-dejavu + font-noto so libreoffice/Ghostscript don't tofu non-ASCII glyphs
#
# Image size grew from ~40 MB (no thumb deps) to ~600-700 MB; the bulk is libreoffice.
# Trade-off accepted: every advertised thumbnail format actually works at runtime.
# A truly-slim variant (frontend + Go binary only) is no longer kept — the previous
# Dockerfile.full has been merged into this single canonical recipe.
#
# Build (from repo root):
#   docker build -t ghcr.io/brf-tech/filex:latest -f docker/Dockerfile .
#
# Run:
#   docker run -p 5212:5212 -v $(pwd)/data:/data ghcr.io/brf-tech/filex:latest

# Multi-arch: build stages are pinned to $BUILDPLATFORM so the Node/Go work
# always runs natively on the build host; the Go stage cross-compiles via
# TARGETOS/TARGETARCH (CGO=0 makes this free). Only the final runtime stage
# (apk installs) runs per-target-arch, under QEMU when cross-building.
#   docker buildx build --platform linux/amd64,linux/arm64 -f docker/Dockerfile .

# ---------------------------------------------------------------------------
# Stage 1: Build npm packages (filex-core, webcomponent, react) + admin UI
# ---------------------------------------------------------------------------
FROM --platform=$BUILDPLATFORM node:20-alpine AS frontend-build
RUN corepack enable && corepack prepare pnpm@9.12.0 --activate
WORKDIR /src

# Copy lockfiles + workspace manifests first so Docker layer cache survives
# source code edits when only deps haven't changed.
COPY package.json pnpm-workspace.yaml ./
COPY pnpm-lock.yaml* ./
COPY packages/core/package.json ./packages/core/
COPY packages/webcomponent/package.json ./packages/webcomponent/
COPY packages/react/package.json ./packages/react/
COPY web/package.json ./web/

# Install all workspace deps (core/webcomponent/react/web). Use --no-frozen-lockfile
# in dev images that don't ship a lockfile yet; CI ships a lockfile so it's frozen.
# vue-tsc OOMs the default 1.7 GB Node heap once we pull in
# @brftech/filex-core (workspace dep with code-split viewers). Bump
# the budget to 4 GB for every Node process during the build.
ENV NODE_OPTIONS=--max-old-space-size=4096

RUN pnpm install --frozen-lockfile || pnpm install --no-frozen-lockfile

# Copy sources after install for max cache hit.
COPY packages/ ./packages/
COPY web/ ./web/

# Build npm packages first (web depends on them transitively for type imports
# and the webcomponent dist gets embedded into the Go binary).
RUN pnpm -r --filter='./packages/*' build
RUN pnpm --filter='./web' build

# ---------------------------------------------------------------------------
# Stage 2: Stage embed assets in /tmp for the Go build to consume
# ---------------------------------------------------------------------------
FROM frontend-build AS embed-prep
RUN mkdir -p /tmp/embed/admin /tmp/embed/web && \
    cp -r /src/web/dist/. /tmp/embed/admin/ && \
    cp -r /src/packages/webcomponent/dist/. /tmp/embed/web/

# ---------------------------------------------------------------------------
# Stage 3: Build the Go binary with embedded assets
# ---------------------------------------------------------------------------
# Bumped to 1.25 for golang.org/x/image v0.39+ (BMP/TIFF/WebP decoders we
# import in internal/thumb/image.go register Go 1.25 toolchain features).
FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS backend-build
RUN apk --no-cache add git ca-certificates
WORKDIR /src

# Cache deps layer. We accept a missing go.sum (first-time build before
# `go mod tidy` has been run locally) — `go mod download` will create one.
COPY backend/go.mod ./
COPY backend/go.sum* ./
RUN go mod download || go mod tidy

# Copy backend sources + drop in embed assets from the frontend stage.
COPY backend/ ./
COPY --from=embed-prep /tmp/embed/admin ./embed/admin
COPY --from=embed-prep /tmp/embed/web   ./embed/web

# `go mod tidy` here (not just in the deps layer) because the COPY above
# overwrites any go.sum produced earlier when the source tree didn't ship
# one. This guarantees a complete go.sum before `go build`.
RUN go mod tidy

ARG VERSION=dev
ARG COMMIT=unknown
ARG DATE=unknown
# TARGETOS/TARGETARCH are set by buildx per requested platform; empty on a
# plain `docker build`, where Go then falls back to the host defaults.
ARG TARGETOS
ARG TARGETARCH
# ldflag symbol path must match the package var declarations in
# internal/version/version.go — `-X main.version=...` is silently a no-op
# because cmd/filex/main.go has no `version` symbol; the linker just skips
# unknown -X targets, so the banner/footer falls back to "0.1.0-dev (unknown,
# unknown)" even on tagged release builds. Use the fully-qualified package
# path instead.
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build \
    -trimpath \
    -ldflags="-s -w \
        -X github.com/brf-tech/filex/backend/internal/version.Version=${VERSION} \
        -X github.com/brf-tech/filex/backend/internal/version.Commit=${COMMIT} \
        -X github.com/brf-tech/filex/backend/internal/version.Date=${DATE}" \
    -o /out/filex \
    ./cmd/filex

# ---------------------------------------------------------------------------
# Stage 4: Runtime with full thumbnail toolchain
# ---------------------------------------------------------------------------
FROM alpine:3.20

# Single apk layer — keeps the Docker layer cache friendly when nothing in
# the dep list changes between builds.
#   ca-certificates / tzdata / su-exec — base runtime niceties.
#   ffmpeg                              — video thumbnails (1st keyframe).
#   ghostscript + poppler-utils         — PDF → JPEG (page 1). gs preferred,
#                                         pdftoppm as fallback (matches
#                                         backend/internal/thumb/pdf.go).
#   libreoffice                         — headless `soffice --headless --convert-to pdf`
#                                         for office docs (matches thumb/office.go).
#                                         Pulls in writer/calc/impress/draw.
#   openjdk17-jre-headless              — LibreOffice's xlsx/docx pipeline calls
#                                         out to javaldx for chart/formula
#                                         rendering. Without a JRE present
#                                         soffice exits 1 and the pipeline rows
#                                         the file as state=failed. Ship the
#                                         smallest headless JRE.
#   font-{noto,noto-cjk} + ttf-*        — without these libreoffice and
#                                         Ghostscript render Unicode as boxes.
RUN apk --no-cache add \
        ca-certificates \
        tzdata \
        su-exec \
        ffmpeg \
        imagemagick \
        ghostscript \
        poppler-utils \
        libreoffice \
        openjdk17-jre-headless \
        rsvg-convert \
        ttf-liberation \
        ttf-dejavu \
        font-noto \
        font-noto-cjk \
    && addgroup -S filex && adduser -S -G filex filex

COPY --from=backend-build /out/filex /usr/local/bin/filex

EXPOSE 5212
VOLUME ["/data"]

ENV FILEX_DATA_DIR=/data \
    FILEX_LISTEN=0.0.0.0:5212 \
    FILEX_THUMBS_ENABLED=true

# Healthcheck — assumes /healthz is mounted unauthenticated by the server.
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD wget -q --spider http://127.0.0.1:5212/healthz || exit 1

ENTRYPOINT ["/usr/local/bin/filex"]
CMD ["serve"]
