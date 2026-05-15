FROM golang:1.26-alpine AS builder
WORKDIR /src
COPY server/ .
RUN go mod download
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -trimpath -ldflags="-s -w" -o /stealthpte-server ./cmd/server

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=builder /stealthpte-server /app/stealthpte-server
VOLUME ["/data"]
EXPOSE 8080
ENTRYPOINT ["/app/stealthpte-server"]
