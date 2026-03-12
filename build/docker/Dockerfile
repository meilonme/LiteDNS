FROM alpine:3.21
WORKDIR /app
COPY bin/litedns /app/litedns
COPY frontend/dist /app/web
COPY configs/config.example.yaml /app/config.example.yaml
EXPOSE 8080
ENTRYPOINT ["/app/litedns"]
