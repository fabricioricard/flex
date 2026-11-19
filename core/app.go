package core

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"sync"
)

// O Flex não guarda dinheiro nem saldo no servidor: a carteira e o cofre ficam na blockchain,
// e o app lê tudo de lá. Este backend só verifica o login (Firebase) e fica pronto para
// receber o parceiro de Pix, que exige chaves guardadas no servidor.

type App struct {
	auth    *Verifier
	initErr error
	support SupportStore
	admins  map[string]bool
}

// New monta o app a partir das variáveis de ambiente.
func New() *App {
	a := &App{admins: adminSet()}
	if pid := os.Getenv("FIREBASE_PROJECT_ID"); pid != "" {
		a.auth = NewVerifier(pid)
	} else if os.Getenv("VERCEL") != "" {
		a.initErr = errors.New("login não configurado (FIREBASE_PROJECT_ID)")
	}
	// Suporte: Firestore na Vercel (chave de serviço); em memória só no desenvolvimento local.
	if raw := os.Getenv("FIREBASE_SERVICE_ACCOUNT"); raw != "" {
		if fs, err := NewFirestoreSupport(raw); err == nil {
			a.support = fs
		}
	} else if os.Getenv("VERCEL") == "" {
		a.support = NewMemSupport()
	}
	return a
}

var (
	once sync.Once
	app  *App
)

// Default devolve o App compartilhado (reaproveitado entre invocações quentes).
func Default() *App {
	once.Do(func() { app = New() })
	return app
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func errJSON(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

// userID extrai o uid do token Firebase. Sem verificador (só em dev local) recusa,
// para não existir um usuário "demo" que passe sem login.
func (a *App) userID(w http.ResponseWriter, r *http.Request) (string, bool) {
	if a.initErr != nil {
		errJSON(w, 503, a.initErr.Error())
		return "", false
	}
	if a.auth == nil {
		errJSON(w, 503, "login não configurado (FIREBASE_PROJECT_ID)")
		return "", false
	}
	tok := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	if tok == "" {
		errJSON(w, 401, "faça login para continuar")
		return "", false
	}
	uid, err := a.auth.Verify(tok)
	if err != nil {
		errJSON(w, 401, "sessão inválida, entre novamente")
		return "", false
	}
	return uid, true
}

// GET /api/health
func (a *App) HandleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// GET /api/me: confirma que o login do Firebase é válido (usado pelo parceiro de Pix, no futuro).
func (a *App) HandleMe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		errJSON(w, 405, "método não permitido")
		return
	}
	uid, ok := a.userID(w, r)
	if !ok {
		return
	}
	writeJSON(w, 200, map[string]string{"uid": uid})
}
