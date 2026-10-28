COMPOSE_PROJECT_NAME ?= pv-cloud-postgres
PYTHON ?= python3
export COMPOSE_PROJECT_NAME
COMPOSE := docker compose --project-name $(COMPOSE_PROJECT_NAME) --file compose.yaml

.DEFAULT_GOAL := start
.PHONY: start status verify stop

start:
	@$(PYTHON) manage.py start

status:
	$(COMPOSE) ps

verify:
	@$(PYTHON) manage.py verify

stop:
	$(COMPOSE) down
