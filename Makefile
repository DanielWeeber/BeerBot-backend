.PHONY: build build-multi test run-dev logs docker-run push-version

IMAGE?=danielweeber/beerbot-backend:local
PLATFORMS?=linux/amd64,linux/arm64
GIT_SHA:=$(shell git rev-parse --short HEAD)

build:
	docker build --build-arg GIT_SHA=$(GIT_SHA) -t $(IMAGE) ./bot

build-multi:
	docker buildx build --platform $(PLATFORMS) --build-arg GIT_SHA=$(GIT_SHA) -t $(IMAGE) ./bot --load

test:
	docker compose -f docker-compose.test.yml build --progress=plain

run-dev:
	docker compose -f docker-compose.dev.yml up --build

docker-run:
	docker run --rm -e BOT_TOKEN -e APP_TOKEN -e DB_PATH=/data/beerbot.db -p 9090:9090 -v $$PWD/bot/data:/data $(IMAGE)

logs:
	docker compose -f docker-compose.dev.yml logs -f

push-version:
	@if [ -z "$(TAG)" ]; then echo "Provide TAG= (e.g. TAG=v1.2.3)"; exit 1; fi; \
	docker tag $(IMAGE) danielweeber/beerbot-backend:$(TAG); \
	docker push danielweeber/beerbot-backend:$(TAG)
