// Package vessel contém a lógica de negócio e os handlers HTTP de navios.
package vessel

import (
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/marcusteixeirabr/psc-gvi/internal/db/sqlc"
)

// Priority representa a prioridade de inspeção calculada pelo CIALA.
type Priority string

const (
	P0      Priority = "P0" // dentro do prazo — sem prioridade
	P1      Priority = "P1" // vencido — prioridade máxima
	P2      Priority = "P2" // próximo do vencimento — atenção
	PNone   Priority = "N/A" // risk_level ou data desconhecidos (aguarda CIALA)
)

// VesselView é o modelo de apresentação de um navio para os templates HTML.
// Converte os tipos pgtype (usados pelo banco) em strings limpas para a UI.
// Os templates Go trabalham com este tipo, nunca com sqlc.Vessel diretamente.
type VesselView struct {
	ID          int64
	IMO         string
	Name        string
	Flag        string
	YearBuilt   string
	VesselType  string
	LengthM     string
	BeamM       string
	RiskLevel   string // "high" | "standard" | "low" | "" (desconhecido)
	LastInspDate string
	Priority    Priority
	PriorityLabel string
	Active      bool
}

// ToView converte um sqlc.Vessel (tipos pgtype) em VesselView (strings simples).
// É chamado após qualquer query que retorna um Vessel.
func ToView(v sqlc.Vessel) VesselView {
	vv := VesselView{
		ID:     v.ID,
		IMO:    v.Imo,
		Name:   v.Name,
		Active: v.Active,
	}

	// Campos nullable: ponteiros Go (*string, *int32) ou pgtype.
	// Verificamos se há valor antes de formatar.
	if v.Flag != nil {
		vv.Flag = *v.Flag
	}
	if v.YearBuilt != nil {
		vv.YearBuilt = fmt.Sprintf("%d", *v.YearBuilt)
	}
	if v.VesselType != nil {
		vv.VesselType = *v.VesselType
	}

	// pgtype.Numeric → string. Float64Value() converte para ponto flutuante.
	// Suficientemente preciso para comprimento/boca de navio em metros.
	if v.LengthM.Valid {
		if f, err := v.LengthM.Float64Value(); err == nil && f.Valid {
			vv.LengthM = fmt.Sprintf("%.1f", f.Float64)
		}
	}
	if v.BeamM.Valid {
		if f, err := v.BeamM.Float64Value(); err == nil && f.Valid {
			vv.BeamM = fmt.Sprintf("%.1f", f.Float64)
		}
	}

	// risk_level e last_inspection_date vêm do CIALA — podem ser nulos.
	if v.RiskLevel != nil {
		vv.RiskLevel = *v.RiskLevel
	}
	if v.LastInspectionDate.Valid {
		vv.LastInspDate = v.LastInspectionDate.Time.Format("02/01/2006")
	}

	// Calcula prioridade de inspeção pela regra CIALA (equivalente ao Java CialaPolicy).
	vv.Priority = calculatePriority(v.RiskLevel, v.LastInspectionDate)
	vv.PriorityLabel = priorityLabel(vv.Priority)

	return vv
}

// ToViews converte um slice de navios — atalho para uso nos handlers de lista.
func ToViews(vessels []sqlc.Vessel) []VesselView {
	views := make([]VesselView, len(vessels))
	for i, v := range vessels {
		views[i] = ToView(v)
	}
	return views
}

// calculatePriority implementa a política de prioridade de inspeção do CIALA.
// Replicado do Java CialaPolicy — regra regulatória do Acordo de Viña del Mar.
//
// Lógica:
//   - Nível desconhecido ou nunca inspecionado → P1 (prioridade máxima)
//   - Depende do risk_level e de quantos meses passaram desde a última inspeção
func calculatePriority(riskLevel *string, lastInsp pgtype.Date) Priority {
	if riskLevel == nil || !lastInsp.Valid {
		// Sem dados do CIALA → prioridade máxima por segurança.
		return P1
	}

	months := monthsSince(lastInsp.Time)

	switch *riskLevel {
	case "high":
		// Alto risco: inspecionar a cada 2 meses.
		// P0: 0–2m | P2: 3–4m | P1: >=5m
		if months <= 2 {
			return P0
		} else if months <= 4 {
			return P2
		}
		return P1

	case "standard":
		// Risco padrão: inspecionar a cada 5 meses.
		// P0: 0–5m | P2: 6–8m | P1: >=9m
		if months <= 5 {
			return P0
		} else if months <= 8 {
			return P2
		}
		return P1

	case "low":
		// Baixo risco: inspecionar a cada 9 meses.
		// P0: 0–9m | P2: 10–18m | P1: >=19m
		if months <= 9 {
			return P0
		} else if months <= 18 {
			return P2
		}
		return P1
	}

	return PNone
}

// monthsSince calcula quantos meses completos se passaram desde uma data.
func monthsSince(t time.Time) int {
	now := time.Now()
	years := now.Year() - t.Year()
	months := int(now.Month()) - int(t.Month())
	return years*12 + months
}

// priorityLabel retorna o texto para exibição na UI.
func priorityLabel(p Priority) string {
	switch p {
	case P0:
		return "Sem prioridade"
	case P1:
		return "Prioridade 1 — Inspecionar"
	case P2:
		return "Prioridade 2 — Atenção"
	case PNone:
		return "Aguardando CIALA"
	}
	return ""
}

// parseNumeric converte uma string de formulário em pgtype.Numeric.
// Retorna um Numeric inválido (NULL no banco) se a string for vazia.
func parseNumeric(s string) pgtype.Numeric {
	var n pgtype.Numeric
	if s != "" {
		_ = n.Scan(s)
	}
	return n
}

// parseOptionalString converte uma string de formulário em *string.
// Retorna nil (NULL no banco) se a string for vazia.
func parseOptionalString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// parseOptionalInt32 converte uma string de formulário em *int32.
// Retorna nil se a string for vazia ou não for um inteiro válido.
func parseOptionalInt32(s string) *int32 {
	if s == "" {
		return nil
	}
	var v int32
	_, err := fmt.Sscanf(s, "%d", &v)
	if err != nil {
		return nil
	}
	return &v
}
