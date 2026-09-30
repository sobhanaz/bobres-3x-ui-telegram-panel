#!/bin/sh
# Runs once on first database initialisation. Creates one least-privilege role and one
# schema per service, so a compromised service (e.g. the internet-facing payments webhook
# handler) cannot read or modify another service's data.
set -eu

psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" <<SQL
-- nobody gets implicit rights on the shared public schema
REVOKE ALL ON SCHEMA public FROM PUBLIC;
REVOKE ALL ON DATABASE "$POSTGRES_DB" FROM PUBLIC;

CREATE ROLE svc_core        LOGIN PASSWORD '$DB_CORE_PASSWORD'        NOSUPERUSER NOCREATEDB NOCREATEROLE;
CREATE ROLE svc_payments    LOGIN PASSWORD '$DB_PAYMENTS_PASSWORD'    NOSUPERUSER NOCREATEDB NOCREATEROLE;
CREATE ROLE svc_provisioner LOGIN PASSWORD '$DB_PROVISIONER_PASSWORD' NOSUPERUSER NOCREATEDB NOCREATEROLE;

CREATE SCHEMA core        AUTHORIZATION svc_core;
CREATE SCHEMA payments    AUTHORIZATION svc_payments;
CREATE SCHEMA provisioner AUTHORIZATION svc_provisioner;

GRANT CONNECT ON DATABASE "$POSTGRES_DB" TO svc_core, svc_payments, svc_provisioner;

-- each role resolves unqualified names in its own schema only
ALTER ROLE svc_core        SET search_path = core;
ALTER ROLE svc_payments    SET search_path = payments;
ALTER ROLE svc_provisioner SET search_path = provisioner;
SQL
