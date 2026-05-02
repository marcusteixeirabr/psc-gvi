package vessel

import "strings"

// VesselFilter contém os critérios de filtragem da lista de navios.
type VesselFilter struct {
	Prioridade string // P1 | P2 | P3 | N/D | ""
	Risco      string // high | standard | low | not_found | ""
	Bandeira   string // texto livre (busca parcial)
}

// Active retorna true se algum filtro está ativo.
func (f VesselFilter) Active() bool {
	return f.Prioridade != "" || f.Risco != "" || f.Bandeira != ""
}

// Apply filtra o slice de VesselView com base nos critérios definidos.
// Filtros combinam em AND. Campo vazio = sem restrição.
func (f VesselFilter) Apply(vessels []VesselView) []VesselView {
	if !f.Active() {
		return vessels
	}
	out := make([]VesselView, 0, len(vessels))
	for _, v := range vessels {
		if f.Prioridade != "" && string(v.Priority) != f.Prioridade {
			continue
		}
		if f.Risco != "" && v.RiskLevel != f.Risco {
			continue
		}
		if f.Bandeira != "" && !strings.Contains(strings.ToLower(v.Flag), strings.ToLower(f.Bandeira)) {
			continue
		}
		out = append(out, v)
	}
	return out
}
