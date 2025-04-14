FROM node:18-alpine AS dashboard-builder
WORKDIR /app/dashboard
COPY dashboard/package.json dashboard/package-lock.json ./
RUN npm ci
COPY dashboard ./
RUN npm run build

FROM golang:1.23-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY keys ./keys
COPY config ./config
COPY updates ./updates
RUN GOOS=linux GOARCH=amd64 go build -o main ./cmd/api

FROM alpine:latest
RUN apk add --no-cache bash
COPY --from=builder /app/main /app/main
COPY --from=dashboard-builder /app/dashboard/dist /app/dashboard/dist
EXPOSE 3000
CMD ["/app/main"]
