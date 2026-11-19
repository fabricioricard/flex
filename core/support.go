package core

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// Suporte: chat entre o usuário e a equipe. Uma conversa por usuário (uid).
// O servidor é o único que fala com o banco; o navegador nunca acessa o Firestore direto.

const (
	maxSupportText   = 2000
	maxSupportBody   = 8 << 10
	supportPerMinute = 5
	supportPerHour   = 30
)

type Msg struct {
	From string    `json:"from"` // "user" ou "admin"
	Text string    `json:"text"`
	TS   time.Time `json:"ts"`
}

type Thread struct {
	UID      string    `json:"uid"`
	Email    string    `json:"email"`
	Addr     string    `json:"addr,omitempty"`
	LastText string    `json:"lastText"`
	LastFrom string    `json:"lastFrom"`
	LastAt   time.Time `json:"lastAt"`
}

type SupportStore interface {
	// AddMessage grava a mensagem e atualiza o resumo da conversa.
	AddMessage(ctx context.Context, t Thread, m Msg) error
	// Messages devolve as últimas mensagens, da mais antiga para a mais nova.
	Messages(ctx context.Context, uid string, limit int) ([]Msg, error)
	// Threads devolve as conversas mais recentes primeiro.
	Threads(ctx context.Context, limit int) ([]Thread, error)
}

// ---- armazenamento em memória (testes e desenvolvimento local) ----

type MemSupport struct {
	mu      sync.Mutex
	msgs    map[string][]Msg
	threads map[string]Thread
}

func NewMemSupport() *MemSupport {
	return &MemSupport{msgs: map[string][]Msg{}, threads: map[string]Thread{}}
}

func (s *MemSupport) AddMessage(_ context.Context, t Thread, m Msg) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.msgs[t.UID] = append(s.msgs[t.UID], m)
	old := s.threads[t.UID]
	if t.Email == "" {
		t.Email = old.Email
	}
	if t.Addr == "" {
		t.Addr = old.Addr
	}
	s.threads[t.UID] = t
	return nil
}

func (s *MemSupport) Messages(_ context.Context, uid string, limit int) ([]Msg, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	all := s.msgs[uid]
	if len(all) > limit {
		all = all[len(all)-limit:]
	}
	return append([]Msg(nil), all...), nil
}

func (s *MemSupport) Threads(_ context.Context, limit int) ([]Thread, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Thread, 0, len(s.threads))
	for _, t := range s.threads {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastAt.After(out[j].LastAt) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// ---- validação e rotas ----

var addrRe = regexp.MustCompile(`^0x[0-9a-fA-F]{40}$`)

func cleanText(s string) (string, bool) {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\r\n", "\n"))
	if s == "" || utf8.RuneCountInString(s) > maxSupportText {
		return "", false
	}
	return s, true
}

func (a *App) supportStore(w http.ResponseWriter) (SupportStore, bool) {
	if a.support == nil {
		errJSON(w, 503, "suporte ainda não configurado")
		return nil, false
	}
	return a.support, true
}

func (a *App) claims(w http.ResponseWriter, r *http.Request) (Claims, bool) {
	if a.initErr != nil {
		errJSON(w, 503, a.initErr.Error())
		return Claims{}, false
	}
	if a.auth == nil {
		errJSON(w, 503, "login não configurado (FIREBASE_PROJECT_ID)")
		return Claims{}, false
	}
	tok := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	if tok == "" {
		errJSON(w, 401, "faça login para continuar")
		return Claims{}, false
	}
	c, err := a.auth.VerifyClaims(tok)
	if err != nil {
		errJSON(w, 401, "sessão inválida, entre novamente")
		return Claims{}, false
	}
	return c, true
}

func (a *App) isAdmin(c Claims) bool {
	return c.EmailVerified && c.Email != "" && a.admins[c.Email]
}

// GET /api/support  → mensagens da própria conversa
// POST /api/support → {text, addr?} envia uma mensagem
func (a *App) HandleSupport(w http.ResponseWriter, r *http.Request) {
	c, ok := a.claims(w, r)
	if !ok {
		return
	}
	st, ok := a.supportStore(w)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		ms, err := st.Messages(r.Context(), c.UID, 100)
		if err != nil {
			errJSON(w, 502, "não foi possível carregar as mensagens agora")
			return
		}
		writeJSON(w, 200, map[string]any{"messages": nonNil(ms)})
	case http.MethodPost:
		var in struct {
			Text string `json:"text"`
			Addr string `json:"addr"`
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxSupportBody)
		if json.NewDecoder(r.Body).Decode(&in) != nil {
			errJSON(w, 400, "mensagem inválida")
			return
		}
		text, ok := cleanText(in.Text)
		if !ok {
			errJSON(w, 400, "escreva uma mensagem de até 2000 caracteres")
			return
		}
		if in.Addr != "" && !addrRe.MatchString(in.Addr) {
			in.Addr = ""
		}
		recent, err := st.Messages(r.Context(), c.UID, supportPerHour)
		if err != nil {
			errJSON(w, 502, "não foi possível enviar agora, tente de novo")
			return
		}
		now := time.Now().UTC()
		var perMin, perHour int
		for _, m := range recent {
			if m.From != "user" {
				continue
			}
			if now.Sub(m.TS) < time.Hour {
				perHour++
			}
			if now.Sub(m.TS) < time.Minute {
				perMin++
			}
		}
		if perMin >= supportPerMinute || perHour >= supportPerHour {
			errJSON(w, 429, "muitas mensagens em pouco tempo, aguarde um instante")
			return
		}
		m := Msg{From: "user", Text: text, TS: now}
		t := Thread{UID: c.UID, Email: c.Email, Addr: in.Addr, LastText: preview(text), LastFrom: "user", LastAt: now}
		if err := st.AddMessage(r.Context(), t, m); err != nil {
			errJSON(w, 502, "não foi possível enviar agora, tente de novo")
			return
		}
		writeJSON(w, 200, map[string]any{"message": m})
	default:
		w.Header().Set("Allow", "GET, POST")
		errJSON(w, 405, "método não permitido")
	}
}

// GET /api/admin-support            → lista de conversas (só equipe)
// GET /api/admin-support?uid=…      → mensagens de uma conversa
// POST /api/admin-support {uid,text} → resposta da equipe
func (a *App) HandleAdminSupport(w http.ResponseWriter, r *http.Request) {
	c, ok := a.claims(w, r)
	if !ok {
		return
	}
	if !a.isAdmin(c) {
		errJSON(w, 403, "acesso restrito à equipe")
		return
	}
	st, ok := a.supportStore(w)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		if uid := r.URL.Query().Get("uid"); uid != "" {
			if len(uid) > 128 || strings.ContainsAny(uid, "/") {
				errJSON(w, 400, "uid inválido")
				return
			}
			ms, err := st.Messages(r.Context(), uid, 200)
			if err != nil {
				errJSON(w, 502, "falha ao carregar")
				return
			}
			writeJSON(w, 200, map[string]any{"messages": nonNil(ms)})
			return
		}
		ts, err := st.Threads(r.Context(), 100)
		if err != nil {
			errJSON(w, 502, "falha ao carregar")
			return
		}
		if ts == nil {
			ts = []Thread{}
		}
		writeJSON(w, 200, map[string]any{"threads": ts})
	case http.MethodPost:
		var in struct {
			UID  string `json:"uid"`
			Text string `json:"text"`
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxSupportBody)
		if json.NewDecoder(r.Body).Decode(&in) != nil || in.UID == "" || len(in.UID) > 128 || strings.ContainsAny(in.UID, "/") {
			errJSON(w, 400, "pedido inválido")
			return
		}
		text, ok := cleanText(in.Text)
		if !ok {
			errJSON(w, 400, "escreva uma resposta de até 2000 caracteres")
			return
		}
		now := time.Now().UTC()
		m := Msg{From: "admin", Text: text, TS: now}
		t := Thread{UID: in.UID, LastText: preview(text), LastFrom: "admin", LastAt: now}
		if err := st.AddMessage(r.Context(), t, m); err != nil {
			errJSON(w, 502, "falha ao enviar")
			return
		}
		writeJSON(w, 200, map[string]any{"message": m})
	default:
		w.Header().Set("Allow", "GET, POST")
		errJSON(w, 405, "método não permitido")
	}
}

// GET /api/support-admin-check → {admin:bool} para o painel saber se mostra a tela.
func (a *App) HandleWhoAmI(w http.ResponseWriter, r *http.Request) {
	c, ok := a.claims(w, r)
	if !ok {
		return
	}
	writeJSON(w, 200, map[string]any{"admin": a.isAdmin(c), "email": c.Email})
}

func nonNil(m []Msg) []Msg {
	if m == nil {
		return []Msg{}
	}
	return m
}

func preview(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) > 120 {
		r := []rune(s)
		s = string(r[:120]) + "…"
	}
	return s
}

// adminSet lê ADMIN_EMAILS (separados por vírgula).
func adminSet() map[string]bool {
	m := map[string]bool{}
	for _, e := range strings.Split(os.Getenv("ADMIN_EMAILS"), ",") {
		if e = strings.ToLower(strings.TrimSpace(e)); e != "" {
			m[e] = true
		}
	}
	return m
}
