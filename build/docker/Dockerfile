FROM alpine:3.21
WORKDIR /app
COPY bin/litedns /app/litedns
COPY frontend/dist /app/web
COPY config.yaml /app/config.yaml
EXPOSE 8080
ENTRYPOINT ["/app/litedns"]
