#!/bin/bash
set -e

BACKUP_DIR="/opt/psc-gvi/backups"
DATE=$(date +%Y%m%d_%H%M%S)
BACKUP_FILE="$BACKUP_DIR/pscgvi_$DATE.sql.gz"
RETENTION_DAYS=7

mkdir -p "$BACKUP_DIR"

set -a
# shellcheck disable=SC1091
source /opt/psc-gvi/.env
set +a

echo "[backup] iniciando dump — $DATE"

PGPASSWORD="$DB_PASSWORD" pg_dump \
  -h "$DB_HOST" \
  -U "$DB_USER" \
  "$DB_NAME" \
  | gzip > "$BACKUP_FILE"

SIZE=$(du -h "$BACKUP_FILE" | cut -f1)
echo "[backup] arquivo criado: $BACKUP_FILE ($SIZE)"

# Rotação local: remove backups mais antigos que RETENTION_DAYS
REMOVED=$(find "$BACKUP_DIR" -name "pscgvi_*.sql.gz" -mtime "+$RETENTION_DAYS" -print -delete | wc -l)
[ "$REMOVED" -gt 0 ] && echo "[backup] rotação: $REMOVED arquivo(s) removido(s)"

# Upload GCS — só executa se BACKUP_GCS_BUCKET estiver definida no .env
if [ -n "$BACKUP_GCS_BUCKET" ]; then
  gsutil cp "$BACKUP_FILE" "gs://$BACKUP_GCS_BUCKET/$(basename "$BACKUP_FILE")"
  echo "[backup] upload GCS: gs://$BACKUP_GCS_BUCKET/$(basename "$BACKUP_FILE")"
fi

echo "[backup] concluído"
