FROM golang:1.24-alpine AS deps

RUN apk add --no-cache git ca-certificates file

WORKDIR /build

COPY go.mod go.sum ./

RUN --mount=type=cache,target=/root/.cache/go \
    go mod download -x

FROM deps AS builder

ARG VERSION=dev
ARG COMMIT_SHA=unknown
ARG BUILD_TIME=unknown

COPY . .

RUN --mount=type=cache,target=/root/.cache/go \
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build \
        -ldflags="-s -w \
            -X main.version=${VERSION} \
            -X main.commitSHA=${COMMIT_SHA} \
            -X main.buildTime=${BUILD_TIME}" \
        -trimpath \
        -o /build/bin/server \
        ./cmd/server

RUN file /build/bin/server | grep -q "statically linked" \
    || (echo "ERROR: binary is not statically linked" && exit 1)

FROM gcr.io/distroless/static-debian12:nonroot AS runtime

COPY --from=builder --chown=nonroot:nonroot /build/bin/server /server

COPY --from=builder --chown=nonroot:nonroot /build/migrations /migrations

USER nonroot:nonroot

EXPOSE 8080

ENV GOMEMLIMIT=400MiB \
    GOGC=100 \
    DATABASE_MIGRATIONS_PATH=file:///migrations

ENTRYPOINT ["/server"]