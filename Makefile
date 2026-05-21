.PHONY: run build docker-build docker-up docker-down

run:
	./scripts/dev.sh

build:
	./scripts/build.sh

docker-build: build
	docker build -t litedns:latest .

docker-up:
	docker compose -f docker-compose.yaml up -d

docker-down:
	docker compose -f docker-compose.yaml down
