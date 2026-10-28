COMPOSE_PROJECT_NAME ?= pv-cloud-millway
PYTHON ?= python3
export COMPOSE_PROJECT_NAME
COMPOSE := docker compose --project-name $(COMPOSE_PROJECT_NAME) --file compose.yaml

.DEFAULT_GOAL := start
.PHONY: start status verify stop

start:
	@$(PYTHON) manage.py start
	@$(MAKE) --no-print-directory verify

status:
	$(COMPOSE) ps

verify:
	$(COMPOSE) exec --no-TTY millway /millway verify -config /etc/millway/millway.json

stop:
	$(COMPOSE) down
