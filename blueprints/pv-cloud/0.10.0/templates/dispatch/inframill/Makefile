COMPOSE_PROJECT_NAME ?= pv-cloud-inframill
PYTHON ?= python3
export COMPOSE_PROJECT_NAME
COMPOSE := docker compose --project-name $(COMPOSE_PROJECT_NAME) --file compose.yaml

.DEFAULT_GOAL := start
.PHONY: start status verify stop

start:
	@$(PYTHON) validate.py
	@$(PYTHON) registry_login.py
	@mkdir -p ssh blueprints
	@chmod 700 ssh
	$(COMPOSE) config --quiet
	$(COMPOSE) up --detach --force-recreate --wait --wait-timeout 120

status:
	$(COMPOSE) ps

verify:
	$(COMPOSE) exec --no-TTY inframill python3 /etc/inframill/healthcheck.py

stop:
	$(COMPOSE) down
