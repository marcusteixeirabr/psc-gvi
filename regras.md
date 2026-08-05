# Regras de Negócio — PSC-GVI

Documento de referência para as regras de domínio do sistema.
Mantido aqui para que Marcus possa verificar e corrigir antes de implementar.

> **Como usar:** sempre que uma regra for alterada no código, atualizar este arquivo.
> Seção "Estado da Implementação" de cada regra indica se está ✅ correta, ⚠️ com problema ou ❌ não implementada.

---

## 1. Ciclo de vida de uma Escala (port_call)

```
CRIADA (planned)
    │
    ▼
ATRACADA (active + vessel_status = berthed)
    │
    ├──► CONCLUÍDA (completed) — navio suspendeu, partiu normalmente
    │
    └──► CANCELADA (aborted)  — navio sumiu antes de atracar
```

### Valores de `port_call_status`

| Valor | Significado |
|-------|------------|
| `planned` | Escala criada pelo ZP-21, navio ainda não atracou |
| `active` | Navio atracado (`vessel_status = 'berthed'`) |
| `completed` | Navio partiu (suspendeu) — escala encerrada normalmente |
| `aborted` | Escala cancelada — navio sumiu antes de atracar |
| `closed` | Reservado (uso futuro) |

### Valores de `vessel_status`

| Valor | Significado |
|-------|------------|
| `navigating` | Navio a caminho |
| `drifting` | Derivando, aguardando berço |
| `anchored` | Ancorado na barra |
| `maneuvring` | Em manobra com praticagem |
| `berthed` | Atracado no berço |

---

## 2. O que o ZP-21 mostra

A tabela "Manobras Previstas" do ZP-21 pode ter **dois registros por navio** no mesmo port call:

| Tipo de manobra | Siglas reconhecidas | Significado |
|----------------|-------------------|-------------|
| `entrada` | ETB, ATB, ENTRADA, ATRACADO, CHEGADA | Chegada (prevista ou confirmada) |
| `saida` | ETS, ATS, SAÍDA, DESATRACADO, PARTIDA | Saída (prevista ou confirmada) |

**Estado normal de um navio atracado:**
- ZP-21 mostra **ambos**: `entrada` (ATB confirmado) + `saida` (ETS prevista)

**Importante:** a presença de `saida` (ETS) enquanto o navio está atracado é **normal** — não significa que o navio partiu.

---

## 3. Hierarquia de Imutabilidade

Regra fundamental: o ZP-21 nunca sobrescreve dados já consolidados.

| Prioridade | Campo | Quem pode alterar |
|-----------|-------|------------------|
| 1 (menor) | `actual_arrival` NULL | ZP-21 preenche automaticamente |
| 2 | `actual_arrival` preenchido | Apenas edição manual (editores/admins) |
| 3 (maior) | `actual_departure` preenchido (`completed`) | Apenas edição manual — NADA automático |
| — | Inspeções | Podem ser adicionadas em qualquer escala, mesmo antiga |

---

## 4. Regras de Detecção Automática

### R1 — Criação automática de escala

**Gatilho:** ZP-21 mostra navio sem escala `planned` ou `active` no BD.

**Ação:** cria nova escala com `port_call_status = 'planned'`.

**Estado da implementação:** ✅ Correto (`createPortCallEntry` em `service.go`)

---

### R2 — Auto-atracação: só `saida` visível para escala `planned`

**Situação:** navio tem escala `planned` (sem `actual_arrival`) e ZP-21 mostra **apenas** `saida` (sem `entrada`).

**Interpretação:** o navio atracou entre dois ciclos de scraping — a entrada ocorreu e o ZP-21 já removeu a linha de chegada.

**Ação:**
- Data de atracação = `eta_date` armazenado, ou data corrente se não houver
- `vessel_status` → `berthed`, `port_call_status` → `active`

**Estado da implementação:** ✅ Correto (bloco "Auto-atracação" em `processGroup`)

---

### R3 — Atualização: `só entrada` para escala `active + berthed`

**Situação:** escala está `active + berthed` e ZP-21 mostra **apenas** `entrada` (sem `saida`).

**Interpretação:** o navio ainda está atracado; o registro de chegada (ETA/ATB) permanece visível enquanto a linha de saída ainda não apareceu. A `entrada` visível é da escala corrente, não de uma próxima visita.

**Ação:** apenas **atualiza** os dados da escala existente (ETA, terminal). Não conclui e não cria nova escala.

**Nota:** a detecção de partida ocorre via R6 (`só saída` detectado) ou via R4/R5 (navio some do ZP-21).

**Estado da implementação:** ✅ Correto (2026-04-30) — apenas atualiza ETA/ETD, sem conclusão.

---

### R4 — Permanece Atracado: escala `active + berthed` some do ZP-21 e terminal está vago

**Situação:** escala está `active + berthed`, o navio **sumiu** da tabela "Manobras Previstas", e **não há outra escala `active + berthed` no mesmo terminal**.

**Interpretação:** o desaparecimento do ZP-21 pode ser transitório (falha do site, ciclo perdido). Sem evidência de que outro navio ocupou o berço, não há confirmação de que o navio partiu — a escala permanece como atracada.

**Ação:** nenhuma — escala permanece `active + berthed`.

**Terminal NULL:** sem terminal informado, não é possível verificar ocupação. Por conservadorismo, permanece atracado.

**Estado da implementação:** ✅ Correto (2026-04-30) — sem ação, escala permanece atracada.

---

### R5 — Conclusão: escala `active + berthed` some do ZP-21 e terminal está ocupado por outro navio

**Situação:** escala está `active + berthed`, o navio **sumiu** da tabela "Manobras Previstas", e **há outra escala `active + berthed` no mesmo terminal**.

**Interpretação:** um novo navio ocupou o mesmo berço — isso confirma que o navio original partiu. A escala é encerrada.

**Ação:** conclui a escala (`completed`) com data corrente.

**Estado da implementação:** ✅ Correto (2026-04-30) — conclui quando há outro navio confirmado no mesmo terminal (`GetStaleBerthedPortCalls` com EXISTS).

---

### R6 — Cancelamento: escala `planned` desaparece do ZP-21

**Situação:** escala está `planned` (não atracou) E o navio **sumiu** da tabela "Manobras Previstas".

**Interpretação:** escala cancelada ou adiada — o navio não veio.

**Ação:** `port_call_status` → `aborted`.

**Estado da implementação:** ✅ Correto (query `AbortStaleZP21PortCalls`)

---

### R7 — Conclusão: escala `active + berthed` com `só saída` no ZP-21

**Situação:** escala está `active + berthed` e ZP-21 mostra **apenas** `saida` (sem `entrada`).

**Interpretação:** depende da **situação** informada pelo ZP-21 na mesma linha:

| Situação ZP-21 | Interpretação | Ação |
|---------------|--------------|------|
| ≠ "atracado" (ex: "partindo", "em manobra") | Navio está realmente partindo | **Conclui** |
| "atracado" | Apenas ETS prevista — navio ainda no berço (suspensão planejada) | **Não muda nada** — atualiza ETD |
| Ausente / não capturada | Assume partida (comportamento conservador) | **Conclui** |

**Distinção completa:**
| ZP-21 mostra | Ação |
|-------------|------|
| `entrada` + `saida` | Atualiza ETA e ETD — navio atracado com partida prevista (normal) |
| Só `saida`, situação = "atracado" | Atualiza ETD — suspensão prevista, navio ainda no berço |
| Só `saida`, situação ≠ "atracado" | **Conclui** — navio partindo ou já partiu |
| Só `entrada` | Atualiza ETA — navio atracado, saída ainda não prevista |
| Nenhum | Ver R4 e R5 — depende do terminal |

**Campo `Situation` no scraper:** capturado da coluna "Situação" do ZP-21, normalizado (minúsculas, sem acentos). Se a coluna não existir na tabela, `Situation` fica vazio e o comportamento é concluir (fallback seguro).

**Estado da implementação:** ✅ Correto (2026-04-30) — verifica `!strings.Contains(saida.Situation, "atrac")` antes de concluir.

---

### R8 — Cooldown: bloqueio de re-atracação no mesmo terminal em até 2 dias

**Situação:** um navio concluiu uma escala (`completed`) em um terminal X, com `actual_departure`/`departed_date` registrado. Dentro dos **2 dias seguintes** a essa data (contados a partir da data real de desatracação), o ZP-21 passa a mostrar uma nova escala (entrada, ou entrada+saída) para o **mesmo navio** no **mesmo terminal**.

**Interpretação:** um navio não desatraca e re-atraca no mesmo berço em menos de 2 dias — isso é sinal de ruído/incorreção do próprio site da ZP-21 (dado desatualizado, duplicado ou mal capturado), não uma nova escala real.

**Comparação de terminal:** normalizada (minúsculas, sem espaços extras, sem acentuação) — mesmo padrão já usado para normalizar a coluna `Situação`.

**Ação:** a entrada do ZP-21 é **ignorada** para fins de criação automática de escala — não deve disparar R1 (nem qualquer outra criação). Descarte silencioso, sem log dedicado.

**Escopo:** aplica-se apenas à criação **automática** originada do ZP-21 (R1). **Não** se aplica a registro/edição manual por editores ou admin — um editor pode registrar uma nova escala real no mesmo terminal dentro da janela de 2 dias, se de fato aconteceu.

**Estado da implementação:** ❌ Não implementado — especificação registrada em 2026-08-05 (Marcus definiu a regra, decidiu documentar antes de implementar).

---

## 5. Atualização de Dados (sem mudança de status)

**Situação:** escala `planned` ou `active` continua visível no ZP-21 normalmente.

**Ações:**
- Atualiza `eta_date/eta_time` se `actual_arrival` ainda NULL (dados não consolidados)
- Atualiza `etd_date/etd_time` se `actual_departure` ainda NULL
- Registra `last_zp21_seen_at` com timestamp do ciclo

**Estado da implementação:** ✅ Correto

---

## 6. Resumo das Combinações (escala `active + berthed`)

| ZP-21 mostra | Ação correta | Estado |
|-------------|-------------|--------|
| `entrada` + `saida` | Atualiza ETA e ETD — **não conclui** | ✅ Correto |
| Só `entrada` | Atualiza ETA — **não conclui** | ✅ Correto |
| Só `saida`, situação ≠ "atracado" | **Conclui** (R7) | ✅ Correto |
| Só `saida`, situação = "atracado" | Atualiza ETD — suspensão prevista (R7) | ✅ Correto |
| Nenhum + terminal livre ou NULL | **Permanece atracado** (R4) | ✅ Correto |
| Nenhum + terminal ocupado | **Conclui** (R5) | ✅ Correto |

---

## 7. Resumo das Regras por Combinação ZP-21 × Status (visão completa)

| ZP-21 mostra | Status atual | Ação |
|-------------|-------------|------|
| entrada+saída ou só entrada | Nenhuma escala ativa | Criar como `planned` (R1) |
| entrada+saída ou só entrada | `planned` ou `active` | Atualizar ETA/ETD (R1) |
| Só saída | `planned` | Auto-atracação → `active+berthed` (R2) |
| Só saída, situação ≠ "atracado" | `active+berthed` | Conclui (R7) |
| Só saída, situação = "atracado" | `active+berthed` | Atualiza ETD — suspensão prevista (R7) |
| Nenhum | `planned` | Cancela → `aborted` (R6) |
| Nenhum | `active+berthed`, terminal livre ou NULL | Permanece atracado (R4) |
| Nenhum | `active+berthed`, outro navio no terminal | Conclui (R5) |

---

## 8. Pendências

| # | Assunto | Status |
|---|---------|--------|
| P1 | Corrigir R3, R4/R5 e R7 no código (`service.go` e `port_call.sql`) | ✅ Implementado (2026-04-30) |
| P2 | Navios com nome acentuado já no BD precisam de merge manual após fix de acentos | ⚠️ Aberto |
| P3 | Renomear `P0` → `P3` em `internal/vessel/service.go` e em todo o código | ❌ Não feito |
| P4 | Implementar R8 — cooldown de 2 dias para re-atracação no mesmo terminal | ❌ Não implementado |
| P5 | Implementar minLength/maxLength na busca VesselFinder (LOA ±5%) | ❌ Não implementado |

---

## 9. Busca de IMO — VesselFinder

**Fonte real:** vesselfinder.com (`internal/scraper/vesselfinder.go`). "Equasis" era terminologia desatualizada de planejamento — não existe scraper de equasis.org neste projeto (corrigido em 2026-08-05).

### Situação atual (✅ implementada)

- Busca por nome: `{VESSEL_FINDER_URL}/vessels?name={nome}`
- Quando a busca retorna mais de um resultado, desambigua comparando LOA/Beam extraídos da tabela de resultados (`disambiguateByDimensions`):
  - Match direto se LOA e Beam estiverem ambos dentro de ±5% (`WithinTolerance`)
  - Fallback: melhor diferença proporcional combinada, aceito se ≤30%
  - Sem dimensões locais (LOA/Beam do navio) → erro de ambiguidade, não resolvido automaticamente

### Nova regra — filtro de LOA na URL (❌ não implementada)

**Situação:** navio novo tem `vessels.length_m` preenchido (veio do ZP-21) no momento em que o scraper de IMO é acionado.

**Ação:** incluir `minLength` e `maxLength` diretamente na URL de busca, filtrando no próprio VesselFinder antes mesmo de buscar por nome:

```
{VESSEL_FINDER_URL}/vessels?name={nome}&minLength={min}&maxLength={max}
```

- **Tolerância:** ±5% sobre o LOA — mesma tolerância já usada no código para desambiguação por dimensões, reaproveitada por consistência.
- **Arredondamento:** `min`/`max` arredondados para inteiro — o campo do VesselFinder (`input#l-min`/`input#l-max`, name `minLength`/`maxLength`) só aceita dígitos (`maxlength="4"`, sem casas decimais).
- **Exemplo:** LOA = 152 → `minLength=144&maxLength=160`.

**Why:** para nomes de navio comuns, a busca só por nome pode retornar múltiplas páginas de resultado no VesselFinder, exigindo desambiguação manual. Filtrar por comprimento diretamente na URL reduz isso na origem.

**Sem LOA:** se `vessels.length_m` estiver vazio, mantém o comportamento atual (busca só por nome).

### Candidato futuro — Beam na URL (não decidido)

Se mesmo com o filtro de LOA ainda houver ambiguidade recorrente, considerar incluir também o Beam (`vessels.beam_m`, campo "boca") na URL de busca. Marcus quer testar isoladamente com LOA primeiro antes de decidir se isso é necessário.

**Estado da implementação:** ❌ Não implementado — especificação registrada em 2026-08-05.
