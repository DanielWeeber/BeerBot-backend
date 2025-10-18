.PHONY: build test run-dev logs

build:
	docker build -t bwm-bot:local .

test:
	docker compose -f docker-compose.test.yml run --rm test

run-dev:
	docker compose -f docker-compose.dev.yml up --build

logs:
	docker compose -f docker-compose.dev.yml logs -f
