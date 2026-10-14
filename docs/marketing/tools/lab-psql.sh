#!/bin/bash
# LAB ONLY. lab-psql.sh 'SQL'  runs SQL inside the lab's postgres, with the container's own superuser.
# (seed-demo.py's docstring shows how its output is applied.)
docker exec -i bobres-lab-bot bash -c 'cd /opt/bobres && docker compose --project-name bobres -f docker-compose.yml --env-file .env exec -T postgres sh -c "psql -U \$POSTGRES_USER -d \$POSTGRES_DB -v ON_ERROR_STOP=1 -P pager=off"' <<<"$1"
