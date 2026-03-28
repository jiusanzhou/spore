FROM golang:1.25-alpine AS builder

RUN apk add --no-cache gcc musl-dev

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=1 go build -ldflags="-s -w" -o /spore ./cmd/spore

# --- Runtime ---
FROM alpine:3.20

RUN apk add --no-cache ca-certificates

COPY --from=builder /spore /usr/local/bin/spore

# Default config directory
RUN mkdir -p /root/.spore

EXPOSE 9292

ENTRYPOINT ["spore"]
CMD ["run"]
