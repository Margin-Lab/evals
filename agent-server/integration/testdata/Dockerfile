FROM golang:1.23 AS builder

WORKDIR /src/agent-server

COPY agent-server/go.mod agent-server/go.sum ./
COPY runner/runner-core/go.mod /src/runner/runner-core/go.mod
RUN go mod download

COPY agent-server /src/agent-server
COPY runner/runner-core /src/runner/runner-core
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/agent-server ./cmd/agent-server

FROM debian:12-slim

WORKDIR /app

RUN apt-get update && \
    apt-get install -y --no-install-recommends bash ca-certificates && \
    rm -rf /var/lib/apt/lists/*

COPY --from=builder /out/agent-server /usr/local/bin/agent-server

EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/agent-server"]
