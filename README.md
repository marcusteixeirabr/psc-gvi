# psc-gvi

**Port State Control — Sistema interno do Grupo de Vistoria e Inspeção (GVI)**
Delegacia da Capitania dos Portos em Itajaí, SC, Brasil.

## Sobre

Sistema interno para apoio às atividades de inspeção PSC nos portos de Itajaí e Navegantes.
Automatiza a identificação de navios aptos à inspeção, registro de atracações e geração de relatórios mensais.

## Stack

- **Backend:** Go + Gin
- **Frontend:** Go Templates + HTMX
- **Banco de dados:** PostgreSQL
- **Deploy:** GCP Free Tier

## Configuração local

```bash
cp .env.example .env
# edite .env com suas credenciais

go mod tidy
go run ./cmd/server
```

## Variáveis de ambiente

Copie `.env.example` para `.env` e preencha os valores.  
**Nunca comite o arquivo `.env`.**
