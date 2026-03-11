FROM golang:1.23-alpine AS builder
WORKDIR /app
COPY go.mod ./
COPY . .
RUN go build -o /out/litedns ./cmd/litedns

FROM alpine:3.21
WORKDIR /app
COPY --from=builder /out/litedns /app/litedns
COPY config.yaml /app/config.yaml
EXPOSE 8080
ENTRYPOINT ["/app/litedns"]
