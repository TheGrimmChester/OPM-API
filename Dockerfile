FROM golang:1.22-alpine AS builder
WORKDIR /app
COPY go.mod ./
COPY *.go ./
RUN CGO_ENABLED=0 GOOS=linux go build -o opm-api .
FROM debian:bookworm-slim AS opm-api
COPY --from=builder /app/opm-api /root/opm-api
ENV LISTEN_ADDR=:8096
EXPOSE 8096
CMD ["/root/opm-api"]
