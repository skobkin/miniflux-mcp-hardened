FROM golang:alpine AS builder

ARG VERSION=dev
ARG REVISION=unknown
ARG BUILD_DATE=unknown

WORKDIR /app

# Copy go mod files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -buildvcs=false \
    -ldflags "-X main.Version=${VERSION} -X main.Revision=${REVISION} -X main.BuildDate=${BUILD_DATE}" \
    -o /out/miniflux-mcp .

FROM scratch

COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=builder --chown=65532:65532 /out/miniflux-mcp /miniflux-mcp

USER 65532:65532

ENV MCP_TRANSPORT=streamable-http

EXPOSE 8080

ENTRYPOINT ["/miniflux-mcp"]
