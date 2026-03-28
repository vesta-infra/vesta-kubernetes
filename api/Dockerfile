FROM --platform=$BUILDPLATFORM golang:1.22-alpine AS builder
ARG TARGETARCH
RUN apk add --no-cache git
WORKDIR /workspace
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=$TARGETARCH go build -a -o vesta-api ./cmd/main.go

FROM gcr.io/distroless/static:nonroot
WORKDIR /
COPY --from=builder /workspace/vesta-api .
USER 65532:65532
EXPOSE 8090
ENTRYPOINT ["/vesta-api"]
