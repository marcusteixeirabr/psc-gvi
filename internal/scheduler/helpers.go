package scheduler

import "github.com/jackc/pgx/v5/pgtype"

// numericToFloat64 converte pgtype.Numeric para float64, retornando 0 se inválido.
func numericToFloat64(n pgtype.Numeric) float64 {
	if !n.Valid {
		return 0
	}
	f, err := n.Float64Value()
	if err != nil || !f.Valid {
		return 0
	}
	return f.Float64
}
