COMPOSE := docker compose --project-name pv-cloud-inframill --file compose.yaml

.DEFAULT_GOAL := install
.PHONY: install validate login status stop

install: validate
	@$(MAKE) --no-print-directory login
	@mkdir -p ssh blueprints
	@chmod 700 ssh
	$(COMPOSE) config --quiet
	$(COMPOSE) up --detach --force-recreate --wait --wait-timeout 120

validate:
	@python3 validate.py

login:
	@python3 registry_login.py

status:
	$(COMPOSE) ps

stop:
	$(COMPOSE) down
