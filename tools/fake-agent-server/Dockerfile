FROM golang:1.22 AS builder

WORKDIR /src

COPY go.mod ./
COPY main.go ./
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/fake-agent-server ./main.go

FROM alpine:3.20

RUN apk add --no-cache bash ca-certificates

COPY --from=builder /out/fake-agent-server /usr/local/bin/fake-agent-server
COPY run-eval.sh /marginlab/tests/run-eval

RUN chmod +x /marginlab/tests/run-eval && \
    mkdir -p /marginlab/workspaces/demo /marginlab/state/runs /marginlab/config

EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/fake-agent-server"]
