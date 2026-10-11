#!/bin/sh
# Runs once on first database initialisation. Creates one least-privilege role and one
# schema per service, so a compromised service (e.g. the internet-facing payments webhook
# handler) cannot read or modify another service's data.
#
# Values are passed as psql variables (:'name' is quoted as a literal, :"name" as an
# identifier), never spliced into the SQL text, so a password containing a quote cannot
# break or alter the statements.
set -eu

psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" \
  -v db="$POSTGRES_DB" \
  -v core_pw="$DB_CORE_PASSWORD" \
  -v payments_pw="$DB_PAYMENTS_PASSWORD" \
  -v provisioner_pw="$DB_PROVISIONER_PASSWORD" <<'SQL'
-- nobody gets implicit rights on the shared public schema
REVOKE ALL ON SCHEMA public FROM PUBLIC;
REVOKE ALL ON DATABASE :"db" FROM PUBLIC;

CREATE ROLE svc_core        LOGIN PASSWORD :'core_pw'        NOSUPERUSER NOCREATEDB NOCREATEROLE;
CREATE ROLE svc_payments    LOGIN PASSWORD :'payments_pw'    NOSUPERUSER NOCREATEDB NOCREATEROLE;
CREATE ROLE svc_provisioner LOGIN PASSWORD :'provisioner_pw' NOSUPERUSER NOCREATEDB NOCREATEROLE;

CREATE SCHEMA core        AUTHORIZATION svc_core;
CREATE SCHEMA payments    AUTHORIZATION svc_payments;
CREATE SCHEMA provisioner AUTHORIZATION svc_provisioner;

GRANT CONNECT ON DATABASE :"db" TO svc_core, svc_payments, svc_provisioner;

-- each role resolves unqualified names in its own schema only
ALTER ROLE svc_core        SET search_path = core;
ALTER ROLE svc_payments    SET search_path = payments;
ALTER ROLE svc_provisioner SET search_path = provisioner;
SQL

# The backup service's role: read-only on everything (pg_read_all_data, PostgreSQL 14+),
# for pg_dump. Only when the install has its password (installs from before the backup
# service get the role from `bobres install`, which creates it idempotently).
if [ -n "${DB_BACKUP_PASSWORD:-}" ]; then
  psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" \
    -v db="$POSTGRES_DB" \
    -v backup_pw="$DB_BACKUP_PASSWORD" <<'SQL'
CREATE ROLE svc_backup LOGIN PASSWORD :'backup_pw' NOSUPERUSER NOCREATEDB NOCREATEROLE;
GRANT pg_read_all_data TO svc_backup;
GRANT CONNECT ON DATABASE :"db" TO svc_backup;
SQL
fi
