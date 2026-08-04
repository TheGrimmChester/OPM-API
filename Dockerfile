# OPM-API — Open Project Manager control plane
FROM golang:1.22-alpine AS builder
RUN apk --no-cache add git ca-certificates
WORKDIR /app
ENV GOPROXY=https://proxy.golang.org,direct
ENV GOSUMDB=sum.golang.org
COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./
RUN CGO_ENABLED=0 GOOS=linux go build -o opm-api .

FROM docker:27-cli AS dockercli

FROM debian:bookworm-slim AS opm-api
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates git \
 && rm -rf /var/lib/apt/lists/*
COPY --from=dockercli /usr/local/bin/docker /usr/local/bin/docker
WORKDIR /root/
COPY --from=builder /app/opm-api .
ENV LISTEN_ADDR=:8096 \
    OPM_RUNNER_TAG=smoke
EXPOSE 8096
CMD ["./opm-api"]

FROM opm-api AS opm-orchestrator
ENV ORCHESTRATOR_LISTEN_ADDR=:8099
CMD ["./opm-api", "orchestrator"]

# Ephemeral task-automation runner — one container per job (not always-on).
FROM debian:bookworm-slim AS opm-runner-task
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates \
 && rm -rf /var/lib/apt/lists/*
COPY scripts/opm-runner-entrypoint.sh /usr/local/bin/opm-runner
RUN chmod 755 /usr/local/bin/opm-runner
USER 65532:65532
WORKDIR /home/opm
ENTRYPOINT ["/usr/local/bin/opm-runner"]
