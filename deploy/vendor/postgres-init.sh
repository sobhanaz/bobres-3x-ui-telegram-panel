#!/bin/sh
# Runs once, when the vendor database is first created. Makes the license server's own role
# and database: the role owns that one database and is not a superuser, so a flaw in the server
# cannot touch anything else in the Postgres instance. The password is a psql variable, never
# spliced into the SQL text.
set -eu

psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname postgres \
  -v app_pw="$DB_LICENSESRV_PASSWORD" <<'SQL'
CREATE ROLE licensesrv LOGIN PASSWORD :'app_pw' NOSUPERUSER NOCREATEDB NOCREATEROLE;
CREATE DATABASE bobres_license OWNER licensesrv;
REVOKE ALL ON DATABASE bobres_license FROM PUBLIC;
SQL
