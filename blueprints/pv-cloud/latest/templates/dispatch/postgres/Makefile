COMPOSE_PROJECT_NAME ?= pv-cloud-postgres
export COMPOSE_PROJECT_NAME
COMPOSE := docker compose --project-name $(COMPOSE_PROJECT_NAME) --file compose.yaml

.DEFAULT_GOAL := start
.PHONY: start status verify stop

start:
	@python3 manage.py start

status:
	$(COMPOSE) ps

verify:
	@python3 manage.py verify

stop:
	$(COMPOSE) down
