FROM golang:1.23-alpine AS builder

WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o slk-mcp .

FROM alpine:3.21

RUN addgroup -g 1000 mcp && adduser -u 1000 -G mcp -s /bin/sh -D mcp

COPY --from=builder /build/slk-mcp /usr/local/bin/slk-mcp

USER mcp
EXPOSE 8000

HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
    CMD wget -q --spider http://localhost:8000/sse || exit 1

ENTRYPOINT ["slk-mcp"]
CMD ["-transport", "sse", "-port", "8000"]
