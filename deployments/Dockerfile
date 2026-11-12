FROM golang:1.26.3-alpine AS builder
WORKDIR /app
RUN apk add --no-cache ca-certificates
COPY . .
RUN go mod download
RUN CGO_ENABLED=0 GOOS=linux go build -o /accounting-service ./cmd/accounting-service

FROM alpine:3.19
WORKDIR /app
RUN apk add --no-cache ca-certificates
COPY --from=builder /accounting-service /app/accounting-service
EXPOSE 8080 50051
ENTRYPOINT ["/app/accounting-service"]
