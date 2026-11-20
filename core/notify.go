package core

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// Aviso opcional no Telegram a cada mensagem nova do usuário, para a equipe responder na hora.
// Variáveis: TELEGRAM_BOT_TOKEN e TELEGRAM_CHAT_ID. Sem elas, nada é enviado.

type telegram struct {
	token, chat, api string
	hc               *http.Client
}

func newTelegram() *telegram {
	t, c := os.Getenv("TELEGRAM_BOT_TOKEN"), os.Getenv("TELEGRAM_CHAT_ID")
	if t == "" || c == "" {
		return nil
	}
	return &telegram{token: t, chat: c, api: "https://api.telegram.org", hc: &http.Client{Timeout: 3 * time.Second}}
}

func (a *App) notifyTeam(ctx context.Context, email, text string) {
	if a.tg == nil {
		return
	}
	who := email
	if who == "" {
		who = "usuário sem e-mail"
	}
	msg := "Flex · suporte\n" + who + ":\n" + preview(text) + "\n\nResponda em /admin.html"
	form := url.Values{"chat_id": {a.tg.chat}, "text": {msg}, "disable_web_page_preview": {"true"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.tg.api+"/bot"+a.tg.token+"/sendMessage", strings.NewReader(form.Encode()))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if resp, err := a.tg.hc.Do(req); err == nil {
		resp.Body.Close() // falha de aviso nunca derruba o envio da mensagem
	}
}
