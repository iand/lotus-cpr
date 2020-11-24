# Builder
FROM golang:1.15-buster as builder
RUN apt-get update -y && apt-get install -y git openssh-client gcc ca-certificates

WORKDIR /build

# Download all dependencies.
COPY go.mod go.mod
COPY go.sum go.sum
RUN go mod download

# Do the build
COPY *.go /build/
RUN CGO_ENABLED=1 go build -o lotus-cpr github.com/iand/lotus-cpr

# Runner
FROM debian:buster-slim
RUN apt-get update && apt-get upgrade -y && apt-get install -y ca-certificates procps

COPY --from=builder /build/lotus-cpr /lotus-cpr
ENTRYPOINT ["/lotus-cpr"]
CMD ["--help"]

