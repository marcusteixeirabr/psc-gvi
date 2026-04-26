# Alpine é ~200MB vs ~1.5GB do Debian bookworm
FROM golang:1.26-alpine

# apk é o gerenciador de pacotes do Alpine (equivalente ao apt-get do Debian)
RUN apk add --no-cache \
    git \
    curl \
    wget \
    make \
    bash \
    postgresql-client

# Compila e instala as ferramentas Go, depois limpa o cache de módulos.
# O && go clean -modcache no MESMO RUN é essencial: o cache só deixa de
# existir na camada Docker se for removido no mesmo comando em que foi criado.
# Sem isso, todas as dependências transitivas (goose baixa drivers para
# ClickHouse, MySQL, SQL Server...) ficam embedadas na imagem — são ~3 GB.
RUN go install github.com/air-verse/air@v1.65.1 \
    && go install github.com/pressly/goose/v3/cmd/goose@v3.27.1 \
    && go install github.com/sqlc-dev/sqlc/cmd/sqlc@v1.31.1 \
    && go clean -modcache

WORKDIR /workspace
