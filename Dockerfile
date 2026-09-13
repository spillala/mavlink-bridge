# syntax=docker/dockerfile:1

FROM golang:1.25-bookworm AS builder
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY cmd ./cmd
COPY internal ./internal

ARG APP_VERSION=dev
ARG GIT_SHA=local

RUN CGO_ENABLED=0 go test ./... -count=1

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o /out/bridge ./cmd/bridge
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o /out/replay ./cmd/replay

FROM gcr.io/distroless/static-debian12

WORKDIR /app
COPY --from=builder /out/bridge /app/bridge
COPY --from=builder /out/replay /app/replay

USER nonroot:nonroot
ENTRYPOINT ["/app/bridge"]
