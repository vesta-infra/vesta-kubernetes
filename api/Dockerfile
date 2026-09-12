FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS builder
ARG TARGETARCH
# Stamped into the binary so the running API can report what it is. The release workflow
# passes the git tag; a plain `docker build` leaves the package defaults ("dev"), which
# the update check treats as "not a release" and stays quiet about.
ARG APP_VERSION=dev
ARG APP_COMMIT=none
ARG APP_DATE=unknown
RUN apk add --no-cache git
WORKDIR /workspace
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=$TARGETARCH go build -a \
      -ldflags "-s -w \
        -X kubernetes.getvesta.sh/api/internal/version.Version=${APP_VERSION} \
        -X kubernetes.getvesta.sh/api/internal/version.Commit=${APP_COMMIT} \
        -X kubernetes.getvesta.sh/api/internal/version.Date=${APP_DATE}" \
      -o vesta-api ./cmd/main.go

FROM gcr.io/distroless/static:nonroot
WORKDIR /
COPY --from=builder /workspace/vesta-api .
USER 65532:65532
EXPOSE 8090
ENTRYPOINT ["/vesta-api"]
