# psc-gvi

**Port State Control — Sistema interno do Grupo de Vistoria e Inspeção (GVI)**
Delegacia da Capitania dos Portos em Itajaí, SC, Brasil.

## Sobre

Sistema interno para apoio às atividades de inspeção PSC nos portos de Itajaí e Navegantes.
Automatiza a identificação de navios aptos à inspeção, registro de atracações e geração de relatórios mensais.

Parte da rotina diária consiste em: buscar navios previstos no ZP-21 → identificar o IMO via VesselFinder →
consultar histórico e grau de risco no CIALA → exibir no dashboard os navios na "janela de inspeção" (P1 ou P2).

## Stack

- **Backend:** Go + Gin
- **Frontend:** Go Templates + HTMX
- **Banco de dados:** PostgreSQL 16
- **Migrations:** Goose
- **Queries:** sqlc (SQL puro, sem ORM)
- **Deploy:** GCP Free Tier (e2-micro)

---

## Arquitetura

### Diagrama de Casos de Uso

```mermaid
flowchart LR
    ADM(["Admin"])
    EDT(["Editor"])
    RDR(["Leitor"])

    subgraph Navios
        UC1["Cadastrar Navio"]
        UC2["Editar / Categorizar"]
        UC3["Desativar Navio"]
    end

    subgraph "Port Calls"
        UC4["Disparar Scraping ZP-21"]
        UC5["Visualizar Port Calls"]
    end

    subgraph Inspeções
        UC6["Registrar Inspeção"]
        UC7["Dashboard — Janela de Inspeção"]
        UC8["Relatório Mensal"]
    end

    subgraph Administração
        UC9["Gerenciar Usuários"]
    end

    ADM --> UC1 & UC2 & UC3 & UC4 & UC5 & UC6 & UC7 & UC8 & UC9
    EDT --> UC1 & UC2 & UC4 & UC5 & UC6 & UC7 & UC8
    RDR --> UC5 & UC7 & UC8
```

---

### Diagrama de Classes

```mermaid
classDiagram
    class Vessel {
        +int64 ID
        +string IMO
        +string Name
        +string Flag
        +int YearBuilt
        +string VesselType
        +float LengthM
        +float BeamM
        +string RiskLevel
        +date LastInspectionDate
        +Category Category
        +calculatePriority() Priority
    }

    class PortCall {
        +int64 ID
        +string Terminal
        +string Berth
        +VesselStatus VesselStatus
        +PortCallStatus PortCallStatus
        +date ETADate
        +time ETATime
        +date ETDDate
        +time ETDTime
        +datetime ActualArrival
        +datetime ActualDeparture
        +string RiskLevelSnapshot
        +Priority PrioritySnapshot
        +bool ZP21Sourced
    }

    class Inspection {
        +int64 ID
        +date InspectionDate
        +string Result
        +string Notes
    }

    class User {
        +int64 ID
        +string Username
        +string DisplayName
        +Role Role
        +bool Active
    }

    class Category {
        <<enumeration>>
        estrangeiro
        nacional
        afretado
        apoio
        desativado
    }

    class Priority {
        <<enumeration>>
        P1
        P2
        P3
        NA
    }

    class VesselStatus {
        <<enumeration>>
        navigating
        drifting
        anchored
        maneuvring
        berthed
    }

    class PortCallStatus {
        <<enumeration>>
        planned
        active
        closed
        aborted
    }

    class Role {
        <<enumeration>>
        admin
        editor
        reader
    }

    Vessel "1" --> "0..*" PortCall : tem
    PortCall "1" --> "0..1" Inspection : pode ter
    Vessel --> Category : classificado como
    Vessel --> Priority : calcula
    PortCall --> VesselStatus : status físico
    PortCall --> PortCallStatus : status administrativo
    User --> Role : tem
```

---

### Diagrama Entidade-Relacionamento

```mermaid
erDiagram
    USERS {
        bigint id PK
        varchar username
        varchar display_name
        varchar password_hash
        varchar role
        boolean active
        timestamptz created_at
    }

    VESSELS {
        bigint id PK
        varchar imo "nullable — preenchido pelo VesselFinder"
        varchar name
        varchar flag "nullable"
        int year_built "nullable"
        varchar vessel_type "nullable"
        decimal length_m "nullable"
        decimal beam_m "nullable"
        varchar risk_level "nullable — preenchido pelo CIALA"
        date last_inspection_date "nullable"
        varchar category "estrangeiro|nacional|afretado|apoio|desativado"
        timestamptz created_at
        timestamptz updated_at
    }

    PORT_CALLS {
        bigint id PK
        bigint vessel_id FK
        varchar terminal "nullable"
        varchar berth "nullable"
        varchar vessel_status
        varchar port_call_status
        date eta_date "nullable"
        time eta_time "nullable"
        date etd_date "nullable"
        time etd_time "nullable"
        timestamptz actual_arrival "nullable"
        timestamptz actual_departure "nullable"
        varchar risk_level_snapshot "nullable"
        varchar priority_snapshot "nullable"
        boolean zp21_sourced
        timestamptz created_at
        timestamptz updated_at
    }

    INSPECTIONS {
        bigint id PK
        bigint port_call_id FK
        date inspection_date
        varchar result
        text notes "nullable"
        timestamptz created_at
        timestamptz updated_at
    }

    VESSELS ||--o{ PORT_CALLS : "tem"
    PORT_CALLS ||--o| INSPECTIONS : "pode ter"
```

---

## Configuração local

```bash
# 1. Copie e preencha as variáveis de ambiente
cp .env.example .env

# 2. Suba os containers (app + banco)
docker compose up -d

# 3. Abra o terminal no devcontainer (VS Code) e aplique as migrations
goose -dir /workspace/migrations postgres "host=db port=5432 dbname=pscgvi user=pscgvi password=devpassword sslmode=disable" up

# 4. Inicie o servidor
go run ./cmd/server
```

Acesse: `http://localhost:8080/login` — credenciais iniciais: `admin` / `admin123`

## Variáveis de ambiente

Copie `.env.example` para `.env` e preencha os valores.
**Nunca comite o arquivo `.env`.**

| Variável | Descrição |
|----------|-----------|
| `ZP21_URL` | URL da página de manobras do ZP-21 (pode mudar sem aviso) |
| `DB_*` | Conexão com o PostgreSQL |
| `SESSION_SECRET` | Chave de assinatura dos cookies de sessão (mín. 32 chars) |
| `CIALA_USERNAME` | Login do portal CIALA |
| `CIALA_PASSWORD` | Senha do portal CIALA |
| `PORT` | Porta HTTP do servidor (padrão: 8080) |
| `ENV` | `development` ou `production` |

---

## Documentação

Os diagramas acima refletem o estado atual do domínio e serão atualizados a cada fase concluída.
Por estarem em formato Mermaid (texto no repositório), evoluem junto com o código sem ferramentas externas.

A documentação completa do projeto — guia de usuário, guia de operação e runbook de deploy —
está prevista para a **Fase 7**, antes do deploy em produção no GCP.

O histórico de decisões técnicas e o progresso de cada feature estão registrados em `docs/stories/`.
