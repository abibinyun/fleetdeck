FROM golang:1.24-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /fleetdeck cmd/fleetdeck/main.go

FROM alpine:3.21
RUN apk add --no-cache ca-certificates docker-cli openssh-client curl bash
COPY --from=builder /fleetdeck /usr/local/bin/fleetdeck

EXPOSE 8089
ENTRYPOINT ["/usr/local/bin/fleetdeck"]
CMD ["-config", "/etc/fleetdeck/fleet.yaml", "-data", "/data"]
