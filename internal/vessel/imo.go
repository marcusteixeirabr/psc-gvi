package vessel

import (
	"fmt"
	"strings"
)

// ValidateIMO valida um número IMO de navio conforme o algoritmo de dígito de controle da OMI.
//
// Regra: dado ABCDEFG (7 dígitos), G deve ser a unidade de (A×7)+(B×6)+(C×5)+(D×4)+(E×3)+(F×2).
// Referência: https://en.wikipedia.org/wiki/IMO_number
func ValidateIMO(raw string) error {
	imo := strings.TrimSpace(raw)
	if len(imo) != 7 {
		return fmt.Errorf("IMO deve ter exatamente 7 dígitos (recebido %d)", len(imo))
	}
	for _, ch := range imo {
		if ch < '0' || ch > '9' {
			return fmt.Errorf("IMO deve conter apenas dígitos numéricos")
		}
	}
	weights := [6]int{7, 6, 5, 4, 3, 2}
	sum := 0
	for i, w := range weights {
		sum += int(imo[i]-'0') * w
	}
	check := int(imo[6] - '0')
	if sum%10 != check {
		return fmt.Errorf("IMO inválido: dígito de controle deve ser %d (soma=%d)", sum%10, sum)
	}
	return nil
}
