# syntax=docker/dockerfile:1.7
FROM golang:1.26-alpine AS build
WORKDIR /src
RUN apk add --no-cache git
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
ARG VERSION=dev
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/sbc-api ./cmd/sbc-api

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata && adduser -D -u 10001 sbc
WORKDIR /app
COPY --from=build /out/sbc-api /app/sbc-api
COPY failover.yaml /app/failover.yaml
# Rendered FreeSWITCH config lives on a shared volume (D-04). The directories
# are created in the image so the first-use copy gives the volume the right owner.
RUN mkdir -p /fsconfig/gateways /fsconfig/acl && chown -R sbc:sbc /fsconfig
USER sbc
EXPOSE 8080 8081
ENTRYPOINT ["/app/sbc-api"]
CMD ["serve"]
