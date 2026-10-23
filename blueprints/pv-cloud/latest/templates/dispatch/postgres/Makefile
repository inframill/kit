COMPOSE := docker compose --project-name pv-cloud-postgres --file compose.yaml

.DEFAULT_GOAL := install
.PHONY: install password login status stop

install: password
	@$(MAKE) --no-print-directory login
	$(COMPOSE) config --quiet
	$(COMPOSE) up --detach --wait --wait-timeout 120

password:
	@umask 077; mkdir -p secrets; \
		if [ ! -s secrets/postgres_password ]; then \
			python3 -c 'import secrets; print(secrets.token_hex(32))' > secrets/postgres_password; \
		fi
	@chmod 700 secrets
	@chmod 600 secrets/postgres_password

login:
	@python3 registry_login.py

status:
	$(COMPOSE) ps

stop:
	$(COMPOSE) down
