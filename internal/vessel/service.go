// Package vessel contém a lógica de negócio e os handlers HTTP de navios.
package vessel

import (
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/marcusteixeirabr/psc-gvi/internal/db/sqlc"
)

// Priority representa a prioridade de inspeção calculada pelo CIALA.
type Priority string

const (
	P1    Priority = "P1"  // vencido — prioridade máxima
	P2    Priority = "P2"  // próximo do vencimento — atenção
	P3    Priority = "P3"  // dentro do prazo — sem urgência (fora da janela)
	PNone Priority = "N/D" // sem dados do CIALA (aguarda consulta)
)

// VesselView é o modelo de apresentação de um navio para os templates HTML.
type VesselView struct {
	ID             int64
	IMO            string
	Name           string
	Flag           string
	YearBuilt      string
	VesselType     string
	LengthM        string
	BeamM          string
	RiskLevel      string // "high" | "standard" | "low" | "not_found" | ""
	LastInspDate   string // data formatada, "Sem inspeção prévia" ou ""
	NeverInspected bool   // CIALA confirmou: nunca inspecionado no AVM
	Deficiencies   string // texto de deficiências da última inspeção (CIALA)
	Afretado       bool   // afretado por empresa brasileira → comporta como nacional
	Acompanhado    bool   // false = não-acompanhado (estado, apoio, desativado...)
	Priority       Priority
	PriorityLabel  string
}

// Classification retorna a classificação derivada automaticamente.
func (v VesselView) Classification() string {
	if !v.Acompanhado {
		return "Não acompanhado"
	}
	if v.Afretado {
		return "Afretado"
	}
	if isBrazilianFlag(v.Flag) {
		return "Nacional"
	}
	return "Estrangeiro"
}

// IsBrazilian retorna true se a bandeira for brasileira — chamável nos templates.
func (v VesselView) IsBrazilian() bool {
	return isBrazilianFlag(v.Flag)
}

// isBrazilianFlag retorna true para bandeiras brasileiras (case-insensitive).
func isBrazilianFlag(flag string) bool {
	f := strings.ToLower(strings.TrimSpace(flag))
	return f == "brazil" || f == "brasil"
}


// ToView converte um sqlc.Vessel (tipos pgtype) em VesselView (strings simples).
// É chamado após qualquer query que retorna um Vessel.
func ToView(v sqlc.Vessel) VesselView {
	vv := VesselView{
		ID:   v.ID,
		Name: v.Name,
	}

	if v.Imo != nil {
		vv.IMO = *v.Imo
	}
	vv.Afretado    = v.Afretado
	vv.Acompanhado = v.Acompanhado

	// Campos nullable: ponteiros Go (*string, *int32) ou pgtype.
	if v.Flag != nil {
		vv.Flag = *v.Flag
	}
	if v.YearBuilt != nil {
		vv.YearBuilt = fmt.Sprintf("%d", *v.YearBuilt)
	}
	if v.VesselType != nil {
		vv.VesselType = *v.VesselType
	}

	// pgtype.Numeric → string.
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
	} else if v.RiskLevel != nil && *v.RiskLevel != "not_found" {
		vv.NeverInspected = true
		vv.LastInspDate = "Sem inspeção prévia"
	}

	if v.LastInspectionDeficiencies != nil {
		vv.Deficiencies = *v.LastInspectionDeficiencies
	}

	// Calcula prioridade de inspeção pela regra CIALA.
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
// P1 = inspeção vencida | P2 = próximo do vencimento | P3 = fora da janela | N/D = sem dados
func calculatePriority(riskLevel *string, lastInsp pgtype.Date) Priority {
	if riskLevel == nil {
		return PNone // Aguardando consulta ao CIALA.
	}
	if *riskLevel == "not_found" {
		return PNone // Não registrado no CIALA — sem dados suficientes.
	}
	if !lastInsp.Valid {
		return P1 // "Sin Inspección previa en AVM" — nunca inspecionado = vencido.
	}

	months := monthsSince(lastInsp.Time)

	switch *riskLevel {
	case "high":
		// Alto risco: inspecionar a cada 2 meses.
		if months <= 2 {
			return P3
		} else if months <= 4 {
			return P2
		}
		return P1

	case "standard":
		// Risco padrão: inspecionar a cada 5 meses.
		if months <= 5 {
			return P3
		} else if months <= 8 {
			return P2
		}
		return P1

	case "low":
		// Baixo risco: inspecionar a cada 9 meses.
		if months <= 9 {
			return P3
		} else if months <= 18 {
			return P2
		}
		return P1
	}

	return PNone
}

// monthsSince calcula quantos meses se passaram desde uma data.
// Conta meses calendário e adiciona 1 se o dia do mês atual já ultrapassou
// o dia da inspeção (para evitar subestimar quando a data parcial já passou).
func monthsSince(t time.Time) int {
	now := time.Now()
	years := now.Year() - t.Year()
	months := int(now.Month()) - int(t.Month())
	total := years*12 + months
	if now.Day() > t.Day() {
		total++
	}
	return total
}

// priorityLabel retorna o texto para exibição na UI.
func priorityLabel(p Priority) string {
	switch p {
	case P1:
		return "Prioridade 1 — Inspecionar"
	case P2:
		return "Prioridade 2 — Atenção"
	case P3:
		return "Sem prioridade — Fora da janela"
	case PNone:
		return "N/D — Aguardando CIALA"
	}
	return ""
}

// CalcPriority é a versão exportada de calculatePriority, para uso em outros pacotes.
func CalcPriority(riskLevel *string, lastInsp pgtype.Date) Priority {
	return calculatePriority(riskLevel, lastInsp)
}

// PriorityLabelFor é a versão exportada de priorityLabel.
func PriorityLabelFor(p Priority) string {
	return priorityLabel(p)
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

