FROM node:22-alpine AS frontend

WORKDIR /web
COPY web/package.json web/package-lock.json ./
RUN npm ci --prefer-offline --no-audit
COPY web/ .
RUN BUILD_PATH=dist npx react-scripts build

# --- Go build ---
FROM golang:1.25-alpine AS builder

RUN apk add --no-cache gcc musl-dev

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
COPY --from=frontend /web/dist /src/web/dist
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
