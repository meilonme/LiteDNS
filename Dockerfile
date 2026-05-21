FROM alpine:3.21
WORKDIR /app
RUN mkdir -p /app/configs
COPY bin/litedns /app/litedns
COPY frontend/dist /app/web
COPY configs/config.example.yaml /app/configs/config.example.yaml
EXPOSE 8080
ENTRYPOINT ["/app/litedns"]
