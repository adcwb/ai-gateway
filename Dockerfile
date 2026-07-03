# ---- frontend build ----
FROM node:22-alpine AS webbuilder
WORKDIR /web
COPY frontend/package.json frontend/package-lock.json* ./
RUN npm install
COPY frontend/ ./
RUN npm run build

# ---- backend build ----
FROM golang:1.23-alpine AS builder
WORKDIR /app
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
# refresh embedded console with the real build
COPY --from=webbuilder /web/dist ./internal/console/dist
RUN CGO_ENABLED=0 GOOS=linux go build -o server ./cmd/server

# ---- runtime ----
FROM alpine:3.20
RUN apk --no-cache add ca-certificates tzdata
WORKDIR /app
COPY --from=builder /app/server .
COPY --from=builder /app/configs ./configs
EXPOSE 8080 9090
ENTRYPOINT ["./server", "-conf", "configs/config.yaml"]
