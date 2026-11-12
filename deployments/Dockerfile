FROM golang:1.22-alpine AS builder
WORKDIR /app
COPY . .
RUN go mod download
RUN CGO_ENABLED=0 GOOS=linux go build -o /accounting-service ./cmd/accounting-service

FROM alpine:3.19
WORKDIR /app
COPY --from=builder /accounting-service /app/accounting-service
EXPOSE 8080 50051
ENTRYPOINT ["/app/accounting-service"]
