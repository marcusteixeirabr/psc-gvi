// Package pagination fornece helpers de paginação server-side reutilizáveis.
package pagination

import (
	"math"
	"net/url"
	"strconv"

	"github.com/gin-gonic/gin"
)

const DefaultPageSize = 50

// Page contém os dados de paginação calculados para uma requisição.
type Page struct {
	Current    int
	TotalPages int
	PageSize   int
	TotalItems int
	Offset     int
	HasPrev    bool
	HasNext    bool
	PrevURL    string
	NextURL    string
}

// FromQuery calcula a paginação a partir do query param ?page= e do total de itens.
// Valores inválidos (< 1 ou > TotalPages) são normalizados silenciosamente.
func FromQuery(c *gin.Context, totalItems, pageSize int) Page {
	current, _ := strconv.Atoi(c.DefaultQuery("page", "1"))

	totalPages := int(math.Ceil(float64(totalItems) / float64(pageSize)))
	if totalPages < 1 {
		totalPages = 1
	}
	if current < 1 {
		current = 1
	}
	if current > totalPages {
		current = totalPages
	}

	offset := (current - 1) * pageSize

	p := Page{
		Current:    current,
		TotalPages: totalPages,
		PageSize:   pageSize,
		TotalItems: totalItems,
		Offset:     offset,
		HasPrev:    current > 1,
		HasNext:    current < totalPages,
	}
	if p.HasPrev {
		p.PrevURL = pageURL(c, current-1)
	}
	if p.HasNext {
		p.NextURL = pageURL(c, current+1)
	}
	return p
}

// Slice aplica a paginação a um slice genérico de inteiros (índices).
// Retorna os índices start e end para fatiamento: items[start:end].
func Slice(total, offset, pageSize int) (start, end int) {
	start = offset
	if start > total {
		start = total
	}
	end = start + pageSize
	if end > total {
		end = total
	}
	return start, end
}

// pageURL constrói a URL da página N preservando todos os query params existentes.
func pageURL(c *gin.Context, pageNum int) string {
	params := url.Values{}
	for k, v := range c.Request.URL.Query() {
		if k != "page" {
			params[k] = v
		}
	}
	params.Set("page", strconv.Itoa(pageNum))
	return "?" + params.Encode()
}
