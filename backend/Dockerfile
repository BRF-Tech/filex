# filex slim image (~40 MB) — pure Go, no thumbnail external tools
FROM golang:1.25-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build \
    -trimpath \
    -ldflags='-s -w -X github.com/brf-tech/filex/backend/internal/version.Version=$(VERSION) -X github.com/brf-tech/filex/backend/internal/version.Commit=$(COMMIT)' \
    -o /out/filex \
    ./cmd/filex

FROM alpine:3.20
RUN apk --no-cache add ca-certificates tzdata
COPY --from=builder /out/filex /usr/local/bin/filex
EXPOSE 5212
VOLUME /data
ENV FILEX_DATA_DIR=/data
ENTRYPOINT ["/usr/local/bin/filex"]
CMD ["serve"]
