.PHONY: run build docker-build docker-up docker-down

run:
	./scripts/dev.sh

build:
	./scripts/build.sh

docker-build: build
	docker build -f build/docker/Dockerfile -t litedns:latest .

docker-up:
	docker compose -f build/docker/docker-compose.yaml up -d

docker-down:
	docker compose -f build/docker/docker-compose.yaml down
