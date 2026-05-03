#!/bin/bash
set -e

cd /opt/psc-gvi

echo '==> [1/3] Atualizando templates e migrations...'
git pull origin main

echo '==> [2/3] Aplicando migrations...'
set -a
# shellcheck disable=SC1091
source .env
set +a
DB_URL="postgres://${DB_USER}:${DB_PASSWORD}@${DB_HOST}:${DB_PORT}/${DB_NAME}?sslmode=disable"
~/go/bin/goose -dir migrations postgres "$DB_URL" up

echo '==> [3/3] Reiniciando serviço...'
sudo systemctl restart psc-gvi
sudo systemctl is-active psc-gvi

echo 'Deploy concluído!'
