# Imagem base oficial do Go — versão 1.24, baseada no Debian
# "bookworm" é o nome da versão do Debian (estável)
FROM golang:1.24-bookworm

# Instala ferramentas do sistema operacional que vamos precisar
RUN apt-get update && apt-get install -y \
    git \
    curl \
    wget \
    make \
    postgresql-client \
    && rm -rf /var/lib/apt/lists/*

# Instala ferramentas específicas do Go:
#
# air     → reinicia o servidor automaticamente quando você salva um arquivo
#           (live reload — equivalente ao nodemon do Node.js)
#
# goose   → gerencia migrações do banco de dados
#           (cria/atualiza tabelas de forma versionada)
#
# sqlc    → você escreve SQL puro, ele gera o código Go correspondente
#           (sem ORM mágico — você vê exatamente o que acontece no banco)
RUN go install github.com/air-verse/air@latest \
    && go install github.com/pressly/goose/v3/cmd/goose@latest \
    && go install github.com/sqlc-dev/sqlc/cmd/sqlc@latest

# Define o diretório de trabalho dentro do container
# É aqui que seus arquivos do projeto vão aparecer
WORKDIR /workspace
