// Pacote user: gerenciamento de usuários (listagem) para admins.
package user

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/marcusteixeirabr/psc-gvi/internal/auth"
)

// UserRow é a view struct usada no template de listagem.
type UserRow struct {
	ID          int64
	Username    string
	DisplayName string
	Role        string
	RoleLabel   string
	Active      bool
	CreatedAt   string
}

// Handler agrupa as dependências do gerenciamento de usuários.
type Handler struct {
	pool *pgxpool.Pool
}

// NewHandler cria o handler com o pool de conexões.
func NewHandler(pool *pgxpool.Pool) *Handler {
	return &Handler{pool: pool}
}

// List — GET /admin/users
// Lista todos os usuários do sistema.
func (h *Handler) List(c *gin.Context) {
	rows, err := h.pool.Query(c.Request.Context(),
		`SELECT id, username, display_name, role, active, created_at
		 FROM users ORDER BY id`,
	)
	if err != nil {
		c.HTML(http.StatusInternalServerError, "admin_users.html", gin.H{
			"User":  auth.GetUser(c),
			"Error": "Erro ao carregar usuários: " + err.Error(),
			"Users": []UserRow{},
		})
		return
	}
	defer rows.Close()

	var users []UserRow
	for rows.Next() {
		var u UserRow
		var createdAt time.Time
		if err := rows.Scan(&u.ID, &u.Username, &u.DisplayName, &u.Role, &u.Active, &createdAt); err != nil {
			continue
		}
		u.CreatedAt = createdAt.Format("02/01/2006")
		u.RoleLabel = roleLabel(u.Role)
		users = append(users, u)
	}

	c.HTML(http.StatusOK, "admin_users.html", gin.H{
		"User":  auth.GetUser(c),
		"Users": users,
		"Flash": c.Query("flash"),
	})
}

func roleLabel(r string) string {
	switch r {
	case "admin":
		return "Administrador"
	case "editor":
		return "Editor"
	case "reader":
		return "Leitor"
	}
	return r
}
