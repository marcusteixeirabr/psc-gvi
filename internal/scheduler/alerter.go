package scheduler

import (
	"context"
	"fmt"
	"log"
	"net/smtp"
	"sync"
	"time"

	dbsqlc "github.com/marcusteixeirabr/psc-gvi/internal/db/sqlc"
)

const (
	alertThresholdErrors  = 3
	alertThresholdSilent  = 3
	alertCooldown         = 6 * time.Hour
)

// Alerter verifica condições de alerta e envia e-mails via SMTP.
// Cooldown em memória evita spam durante falhas prolongadas.
type Alerter struct {
	smtpHost  string
	smtpPort  string
	smtpUser  string
	smtpPass  string
	alertTo   string
	mu        sync.Mutex
	lastSent  map[string]time.Time // chave: "scraper:tipo"
}

// SMTPConfig agrupa a configuração SMTP.
type SMTPConfig struct {
	Host, Port, User, Password, AlertEmail string
}

// NewAlerter cria o Alerter. Retorna nil se SMTP não estiver configurado (graceful skip).
func NewAlerter(cfg SMTPConfig) *Alerter {
	if cfg.Host == "" || cfg.User == "" || cfg.Password == "" || cfg.AlertEmail == "" {
		return nil
	}
	return &Alerter{
		smtpHost: cfg.Host,
		smtpPort: cfg.Port,
		smtpUser: cfg.User,
		smtpPass: cfg.Password,
		alertTo:  cfg.AlertEmail,
		lastSent: make(map[string]time.Time),
	}
}

// SendTest envia um e-mail de teste para verificar a configuração SMTP.
func (a *Alerter) SendTest() error {
	return a.send(
		"[PSC GVI] Teste de e-mail — configuração OK",
		fmt.Sprintf("PSC GVI — Teste de Alerta\n\nSe você recebeu este e-mail, a configuração SMTP está funcionando corretamente.\n\nHorário: %s\n",
			time.Now().Format("02/01/2006 15:04:05")),
	)
}

// CheckAndAlert verifica as condições de alerta e envia e-mail se necessário.
func (a *Alerter) CheckAndAlert(ctx context.Context, q *dbsqlc.Queries) {
	a.checkConsecutiveErrors(ctx, q, "zp21")
	a.checkConsecutiveErrors(ctx, q, "auto_imo")
	a.checkConsecutiveErrors(ctx, q, "auto_ciala")
	a.checkZP21Silent(ctx, q)
}

func (a *Alerter) checkConsecutiveErrors(ctx context.Context, q *dbsqlc.Queries, scraperName string) {
	count, err := q.CountConsecutiveErrors(ctx, scraperName)
	if err != nil || count < alertThresholdErrors {
		return
	}
	key := scraperName + ":consecutive_errors"
	if !a.canSend(key) {
		return
	}
	subject := fmt.Sprintf("[PSC GVI] Alerta: scraper %s falhou %d vezes consecutivas", scraperName, count)
	body := fmt.Sprintf(
		"PSC GVI — Alerta Automático\n\n"+
			"Scraper: %s\n"+
			"Situação: %d execuções consecutivas com erro\n"+
			"Horário: %s\n\n"+
			"Acesse /admin/health para verificar os detalhes.\n",
		scraperName, count, time.Now().Format("02/01/2006 15:04:05"),
	)
	if err := a.send(subject, body); err != nil {
		log.Printf("[alerter] erro ao enviar alerta para %s: %v", scraperName, err)
		return
	}
	a.markSent(key)
	log.Printf("[alerter] alerta enviado: %s falhou %d vezes", scraperName, count)
}

func (a *Alerter) checkZP21Silent(ctx context.Context, q *dbsqlc.Queries) {
	count, err := q.CountZP21SilentCycles(ctx)
	if err != nil || count < alertThresholdSilent {
		return
	}
	key := "zp21:silent"
	if !a.canSend(key) {
		return
	}
	subject := "[PSC GVI] Alerta: ZP-21 retornou 0 navios nas últimas 3 execuções"
	body := fmt.Sprintf(
		"PSC GVI — Alerta Automático\n\n"+
			"Situação: as últimas %d execuções do scraper ZP-21 retornaram 0 navios.\n"+
			"Isso pode indicar que o site mudou de estrutura ou está fora do ar.\n"+
			"Horário: %s\n\n"+
			"Acesse /admin/health e verifique o ZP-21 manualmente.\n",
		count, time.Now().Format("02/01/2006 15:04:05"),
	)
	if err := a.send(subject, body); err != nil {
		log.Printf("[alerter] erro ao enviar alerta ZP-21 silencioso: %v", err)
		return
	}
	a.markSent(key)
	log.Printf("[alerter] alerta enviado: ZP-21 silencioso (%d execuções sem navios)", count)
}

func (a *Alerter) canSend(key string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	last, ok := a.lastSent[key]
	return !ok || time.Since(last) >= alertCooldown
}

func (a *Alerter) markSent(key string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.lastSent[key] = time.Now()
}

// send envia o e-mail via SMTP com STARTTLS (porta 587).
// Compatível com Yahoo, Gmail e outros provedores que usam STARTTLS.
func (a *Alerter) send(subject, body string) error {
	addr := a.smtpHost + ":" + a.smtpPort
	msg := []byte(fmt.Sprintf(
		"From: %s\r\nTo: %s\r\nSubject: %s\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n%s",
		a.smtpUser, a.alertTo, subject, body,
	))
	auth := smtp.PlainAuth("", a.smtpUser, a.smtpPass, a.smtpHost)
	return smtp.SendMail(addr, auth, a.smtpUser, []string{a.alertTo}, msg)
}
