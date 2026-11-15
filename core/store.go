package core

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"os"
	"sync"
	"time"
)

const secondsPerYear = 365 * 24 * 3600.0
const maxEntries = 200

var (
	ErrInsufficient = errors.New("saldo insuficiente")
	ErrBusy         = errors.New("conta ocupada")
)

type Entry struct {
	ID     string    `json:"id"`
	Type   string    `json:"type"` // deposit | withdraw_pix
	USDC   float64   `json:"usdc"`
	BRL    float64   `json:"brl,omitempty"`
	Rate   float64   `json:"rate,omitempty"`
	PixKey string    `json:"pix_key,omitempty"`
	E2E    string    `json:"e2e_id,omitempty"`
	Status string    `json:"status"` // pending | completed | failed
	Error  string    `json:"error,omitempty"`
	At     time.Time `json:"at"`
}

type Account struct {
	Balance   float64           `json:"balance"`   // USDC em Updated
	Principal float64           `json:"principal"` // USDC líquido aportado
	Updated   time.Time         `json:"updated"`
	Entries   []Entry           `json:"entries"`
	Idem      map[string]string `json:"idem"` // Idempotency-Key -> entry ID
}

// accrue capitaliza o saldo continuamente até `now`.
func (a *Account) accrue(now time.Time, apy float64) {
	dt := now.Sub(a.Updated).Seconds()
	if dt > 0 && a.Balance > 0 {
		a.Balance *= math.Exp(math.Log1p(apy) * dt / secondsPerYear)
	}
	a.Updated = now
}

func (a *Account) entry(id string) *Entry {
	for i := range a.Entries {
		if a.Entries[i].ID == id {
			return &a.Entries[i]
		}
	}
	return nil
}

// trim limita o histórico guardado e limpa chaves de idempotência órfãs.
func (a *Account) trim() {
	if len(a.Entries) <= maxEntries {
		return
	}
	a.Entries = a.Entries[len(a.Entries)-maxEntries:]
	keep := map[string]bool{}
	for _, e := range a.Entries {
		keep[e.ID] = true
	}
	for k, id := range a.Idem {
		if !keep[id] {
			delete(a.Idem, k)
		}
	}
}

// Backend é o armazenamento: arquivo (local) ou Redis (Vercel).
type Backend interface {
	Lock(user string) (unlock func(), err error)
	Load(user string) (*Account, error) // nil se não existir
	Save(user string, a *Account) error
}

type Store struct{ b Backend }

// Update executa fn com a conta travada (lock) e salva se fn retornar nil.
func (s *Store) Update(user string, fn func(a *Account) error) error {
	unlock, err := s.b.Lock(user)
	if err != nil {
		return err
	}
	defer unlock()
	a, err := s.b.Load(user)
	if err != nil {
		return err
	}
	if a == nil {
		a = &Account{Updated: time.Now()}
	}
	if a.Idem == nil {
		a.Idem = map[string]string{}
	}
	if err := fn(a); err != nil {
		return err
	}
	a.trim()
	return s.b.Save(user, a)
}

// ---- FileBackend (desenvolvimento local) ----

type FileBackend struct {
	mu       sync.Mutex
	path     string
	accounts map[string]*Account
}

func NewFileBackend(path string) *FileBackend {
	f := &FileBackend{path: path, accounts: map[string]*Account{}}
	if b, err := os.ReadFile(path); err == nil {
		var d struct {
			Accounts map[string]*Account `json:"accounts"`
		}
		if json.Unmarshal(b, &d) == nil && d.Accounts != nil {
			f.accounts = d.Accounts
		}
	}
	return f
}

func (f *FileBackend) Lock(string) (func(), error) {
	f.mu.Lock()
	return f.mu.Unlock, nil
}

func (f *FileBackend) Load(user string) (*Account, error) {
	a, ok := f.accounts[user]
	if !ok {
		return nil, nil
	}
	b, _ := json.Marshal(a) // cópia profunda
	var c Account
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, err
	}
	return &c, nil
}

func (f *FileBackend) Save(user string, a *Account) error {
	f.accounts[user] = a
	b, err := json.MarshalIndent(map[string]any{"accounts": f.accounts}, "", "  ")
	if err != nil {
		return err
	}
	tmp := f.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, f.path)
}

// ---- RedisBackend (Upstash REST: funciona na Vercel) ----

type RedisBackend struct {
	url, token string
	hc         *http.Client
}

func NewRedisBackend(url, token string) *RedisBackend {
	return &RedisBackend{url: url, token: token, hc: &http.Client{Timeout: 8 * time.Second}}
}

func (r *RedisBackend) cmd(args ...any) (json.RawMessage, error) {
	body, _ := json.Marshal(args)
	req, _ := http.NewRequest("POST", r.url, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+r.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := r.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out struct {
		Result json.RawMessage `json:"result"`
		Error  string          `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	if out.Error != "" {
		return nil, fmt.Errorf("redis: %s", out.Error)
	}
	return out.Result, nil
}

const luaUnlock = `if redis.call('get',KEYS[1])==ARGV[1] then return redis.call('del',KEYS[1]) else return 0 end`

func (r *RedisBackend) Lock(user string) (func(), error) {
	key := "lock:" + user
	tok := newID("l")
	deadline := time.Now().Add(8 * time.Second)
	for {
		res, err := r.cmd("SET", key, tok, "NX", "PX", 15000)
		if err != nil {
			return nil, err
		}
		if string(res) != "null" {
			break
		}
		if time.Now().After(deadline) {
			return nil, ErrBusy
		}
		time.Sleep(40 * time.Millisecond)
	}
	return func() { _, _ = r.cmd("EVAL", luaUnlock, 1, key, tok) }, nil
}

func (r *RedisBackend) Load(user string) (*Account, error) {
	res, err := r.cmd("GET", "acct:"+user)
	if err != nil {
		return nil, err
	}
	if string(res) == "null" {
		return nil, nil
	}
	var s string
	if err := json.Unmarshal(res, &s); err != nil {
		return nil, err
	}
	var a Account
	if err := json.Unmarshal([]byte(s), &a); err != nil {
		return nil, err
	}
	return &a, nil
}

func (r *RedisBackend) Save(user string, a *Account) error {
	b, err := json.Marshal(a)
	if err != nil {
		return err
	}
	_, err = r.cmd("SET", "acct:"+user, string(b))
	return err
}
