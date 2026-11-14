package core

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"
)

const (
	minWithdrawUSDC = 1.0
	minBRL          = 5.0
	demoUser        = "demo" // TODO: trocar por autenticação real (Firebase Auth / JWT)
)

type App struct {
	store   *Store
	yield   YieldProvider
	fx      FXProvider
	pix     PixProvider
	sandbox bool
	initErr error
}

type httpErr struct {
	code int
	msg  string
}

func (e *httpErr) Error() string { return e.msg }

func bad(msg string) error { return &httpErr{400, msg} }

func newID(prefix string) string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return prefix + "_" + hex.EncodeToString(b)
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func firstEnv(keys ...string) string {
	for _, k := range keys {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	return ""
}

// New monta o app a partir das variáveis de ambiente.
func New() *App {
	apy := 0.052
	if v, err := strconv.ParseFloat(os.Getenv("MOCK_APY"), 64); err == nil {
		apy = v
	}
	a := &App{
		yield:   MockYield{apy: apy},
		fx:      MockFX{Base: 5.40, Spread: 0.006},
		pix:     MockPix{},
		sandbox: os.Getenv("MODE") != "production",
	}
	url := firstEnv("UPSTASH_REDIS_REST_URL", "KV_REST_API_URL")
	token := firstEnv("UPSTASH_REDIS_REST_TOKEN", "KV_REST_API_TOKEN")
	switch {
	case url != "" && token != "":
		a.store = &Store{b: NewRedisBackend(url, token)}
	case os.Getenv("VERCEL") != "":
		a.initErr = errors.New("armazenamento não configurado (UPSTASH_REDIS_REST_URL / _TOKEN)")
	default:
		a.store = &Store{b: NewFileBackend(envOr("STATE_PATH", "state.json"))}
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

func (a *App) fail(w http.ResponseWriter, err error) {
	var he *httpErr
	switch {
	case errors.As(err, &he):
		errJSON(w, he.code, he.msg)
	case errors.Is(err, ErrBusy):
		errJSON(w, 503, "conta ocupada, tente novamente")
	default:
		log.Printf("erro interno: %v", err)
		errJSON(w, 500, "erro interno")
	}
}

// guard valida método HTTP e se o armazenamento está pronto.
func (a *App) guard(w http.ResponseWriter, r *http.Request, method string) bool {
	if r.Method != method {
		w.Header().Set("Allow", method)
		errJSON(w, 405, "método não permitido")
		return false
	}
	if a.initErr != nil {
		errJSON(w, 503, a.initErr.Error())
		return false
	}
	return true
}

func (a *App) apy() float64 {
	v, err := a.yield.APY()
	if err != nil {
		return 0
	}
	return v
}

// GET /api/account
func (a *App) HandleAccount(w http.ResponseWriter, r *http.Request) {
	if !a.guard(w, r, http.MethodGet) {
		return
	}
	apy := a.apy()
	var resp map[string]any
	err := a.store.Update(demoUser, func(acc *Account) error {
		acc.accrue(time.Now(), apy)
		entries := acc.Entries
		if len(entries) > 30 {
			entries = entries[len(entries)-30:]
		}
		rev := make([]Entry, len(entries))
		for i, e := range entries {
			rev[len(entries)-1-i] = e
		}
		resp = map[string]any{
			"balance":   acc.Balance,
			"principal": acc.Principal,
			"earnings":  acc.Balance - acc.Principal,
			"apy":       apy,
			"entries":   rev,
			"deposit": map[string]string{
				"network": "Base",
				"address": "0xSANDBOX000000000000000000000000000000",
			},
			"sandbox": a.sandbox,
		}
		return nil
	})
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, 200, resp)
}

// POST /api/deposit  (somente sandbox: simula um depósito)
func (a *App) HandleSimDeposit(w http.ResponseWriter, r *http.Request) {
	if !a.guard(w, r, http.MethodPost) {
		return
	}
	if !a.sandbox {
		errJSON(w, 404, "indisponível")
		return
	}
	var in struct {
		USDC float64 `json:"usdc"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in) != nil || in.USDC <= 0 || in.USDC > 1_000_000 {
		errJSON(w, 400, "valor inválido")
		return
	}
	apy := a.apy()
	err := a.store.Update(demoUser, func(acc *Account) error {
		now := time.Now()
		acc.accrue(now, apy)
		acc.Balance += in.USDC
		acc.Principal += in.USDC
		acc.Entries = append(acc.Entries, Entry{ID: newID("dep"), Type: "deposit", USDC: in.USDC, Status: "completed", At: now})
		return nil
	})
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]string{"status": "ok"})
}

// GET /api/quote?usdc=10
func (a *App) HandleQuote(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		errJSON(w, 405, "método não permitido")
		return
	}
	usdc, _ := strconv.ParseFloat(r.URL.Query().Get("usdc"), 64)
	rate, brl, err := a.fx.Quote(usdc)
	if err != nil {
		errJSON(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"rate": rate, "brl": brl})
}

// POST /api/withdraw  (header Idempotency-Key obrigatório)
func (a *App) HandleWithdraw(w http.ResponseWriter, r *http.Request) {
	if !a.guard(w, r, http.MethodPost) {
		return
	}
	idem := r.Header.Get("Idempotency-Key")
	if len(idem) < 8 || len(idem) > 100 {
		errJSON(w, 400, "Idempotency-Key obrigatório")
		return
	}
	var in struct {
		USDC   float64 `json:"usdc"`
		PixKey string  `json:"pix_key"`
		All    bool    `json:"all"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in) != nil {
		errJSON(w, 400, "corpo inválido")
		return
	}
	key, keyType, err := NormalizePixKey(in.PixKey)
	if err != nil {
		errJSON(w, 400, err.Error())
		return
	}
	apy := a.apy()

	// Fase 1: reserva o saldo (débito) com a conta travada.
	var (
		replay                           *Entry
		id                               string
		amount, brl, rate, principalPart float64
	)
	err = a.store.Update(demoUser, func(acc *Account) error {
		if eid, ok := acc.Idem[idem]; ok { // requisição repetida
			if e := acc.entry(eid); e != nil {
				c := *e
				replay = &c
				return nil
			}
		}
		now := time.Now()
		acc.accrue(now, apy)
		amount = in.USDC
		if in.All {
			amount = acc.Balance
		}
		amount = round(amount, 6)
		if amount > acc.Balance {
			if amount-acc.Balance > 1e-9 {
				return bad(ErrInsufficient.Error())
			}
			amount = acc.Balance
		}
		if amount < minWithdrawUSDC {
			return bad(fmt.Sprintf("saque mínimo de %.0f USDC", minWithdrawUSDC))
		}
		var qerr error
		rate, brl, qerr = a.fx.Quote(amount)
		if qerr != nil || brl < minBRL {
			return bad(fmt.Sprintf("valor mínimo de R$ %.2f", minBRL))
		}
		if acc.Balance > 0 {
			principalPart = acc.Principal * (amount / acc.Balance)
		}
		acc.Balance -= amount
		if acc.Balance < 0 {
			acc.Balance = 0
		}
		acc.Principal -= principalPart
		if acc.Principal < 0 {
			acc.Principal = 0
		}
		id = newID("wd")
		acc.Entries = append(acc.Entries, Entry{
			ID: id, Type: "withdraw_pix", USDC: amount, BRL: brl, Rate: rate,
			PixKey: maskKey(key, keyType), Status: "pending", At: now,
		})
		acc.Idem[idem] = id
		return nil
	})
	if err != nil {
		a.fail(w, err)
		return
	}
	if replay != nil {
		writeJSON(w, 200, replay)
		return
	}

	// Fase 2: resgata liquidez e liquida o PIX (fora do lock).
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var e2e string
	var sendErr error
	if sendErr = a.yield.Withdraw(ctx, amount); sendErr == nil {
		e2e, sendErr = a.pix.Send(ctx, PixPayout{Key: key, KeyType: keyType, AmountBRL: brl, Reference: id})
	}

	// Fase 3: confirma ou estorna.
	var final Entry
	err = a.store.Update(demoUser, func(acc *Account) error {
		e := acc.entry(id)
		if e == nil {
			return errors.New("lançamento não encontrado")
		}
		if sendErr != nil {
			acc.accrue(time.Now(), apy)
			acc.Balance += amount
			acc.Principal += principalPart
			e.Status, e.Error = "failed", "falha ao enviar PIX; saldo estornado"
			log.Printf("withdraw %s falhou: %v", id, sendErr)
		} else {
			e.Status, e.E2E = "completed", e2e
		}
		final = *e
		return nil
	})
	if err != nil {
		// Débito já feito e PIX possivelmente enviado: não estornar às cegas.
		log.Printf("ALERTA: withdraw %s precisa de conciliação: %v", id, err)
		a.fail(w, err)
		return
	}
	writeJSON(w, 200, final)
}

func maskKey(k, t string) string {
	switch t {
	case "cpf":
		return "***." + k[3:6] + ".***-**"
	case "cnpj":
		return "**.***." + k[5:8] + "/****-**"
	case "email":
		for i, c := range k {
			if c == '@' {
				if i > 2 {
					return k[:2] + "***" + k[i:]
				}
				return "***" + k[i:]
			}
		}
	case "telefone":
		return k[:5] + "*****" + k[len(k)-2:]
	}
	if len(k) > 8 {
		return k[:8] + "…"
	}
	return k
}
