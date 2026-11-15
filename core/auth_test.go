package core

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const testProject = "meu-projeto"

type testIdP struct {
	key *rsa.PrivateKey
	srv *httptest.Server
}

func newTestIdP(t *testing.T) *testIdP {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	pemStr := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=3600, must-revalidate")
		_ = json.NewEncoder(w).Encode(map[string]string{"kid1": pemStr})
	}))
	t.Cleanup(srv.Close)
	return &testIdP{key: key, srv: srv}
}

func (i *testIdP) token(t *testing.T, claims map[string]any) string {
	t.Helper()
	enc := func(v any) string {
		b, _ := json.Marshal(v)
		return base64.RawURLEncoding.EncodeToString(b)
	}
	head := enc(map[string]string{"alg": "RS256", "kid": "kid1", "typ": "JWT"})
	body := enc(claims)
	sum := sha256.Sum256([]byte(head + "." + body))
	sig, err := rsa.SignPKCS1v15(rand.Reader, i.key, crypto.SHA256, sum[:])
	if err != nil {
		t.Fatal(err)
	}
	return head + "." + body + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func good(uid string) map[string]any {
	now := time.Now().Unix()
	return map[string]any{
		"aud": testProject, "iss": "https://securetoken.google.com/" + testProject,
		"sub": uid, "iat": now - 10, "auth_time": now - 10, "exp": now + 3600,
	}
}

func (i *testIdP) verifier() *Verifier {
	v := NewVerifier(testProject)
	v.certURL = i.srv.URL
	return v
}

func TestVerify(t *testing.T) {
	idp := newTestIdP(t)
	v := idp.verifier()

	if uid, err := v.Verify(idp.token(t, good("ana"))); err != nil || uid != "ana" {
		t.Fatalf("token válido: uid=%q err=%v", uid, err)
	}

	bad := func(name string, mut func(map[string]any)) {
		c := good("ana")
		mut(c)
		if _, err := v.Verify(idp.token(t, c)); err == nil {
			t.Errorf("%s: deveria falhar", name)
		}
	}
	bad("projeto errado", func(c map[string]any) { c["aud"] = "outro" })
	bad("emissor errado", func(c map[string]any) { c["iss"] = "https://evil.example/" + testProject })
	bad("expirado", func(c map[string]any) { c["exp"] = time.Now().Unix() - 3600 })
	bad("sem sub", func(c map[string]any) { c["sub"] = "" })
	bad("do futuro", func(c map[string]any) { c["iat"] = time.Now().Unix() + 3600 })

	// payload adulterado mantendo a assinatura original
	parts := strings.Split(idp.token(t, good("ana")), ".")
	forged, _ := json.Marshal(good("admin"))
	parts[1] = base64.RawURLEncoding.EncodeToString(forged)
	if _, err := v.Verify(strings.Join(parts, ".")); err == nil {
		t.Error("payload adulterado deveria falhar")
	}
	// chave de outro emissor
	other := newTestIdP(t)
	if _, err := v.Verify(other.token(t, good("ana"))); err == nil {
		t.Error("assinatura de outra chave deveria falhar")
	}
	for _, junk := range []string{"", "a.b", "a.b.c", "x.y.z.w"} {
		if _, err := v.Verify(junk); err == nil {
			t.Errorf("%q deveria falhar", junk)
		}
	}
}

func TestHandlersIsolateUsers(t *testing.T) {
	idp := newTestIdP(t)
	app := &App{
		store:   &Store{b: NewFileBackend(filepath.Join(t.TempDir(), "s.json"))},
		yield:   MockYield{apy: 0.05},
		fx:      MockFX{Base: 5.4, Spread: 0.006},
		pix:     MockPix{},
		sandbox: true,
		auth:    idp.verifier(),
	}
	call := func(h http.HandlerFunc, method, tok, body string, hdr map[string]string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "/x", strings.NewReader(body))
		if tok != "" {
			r.Header.Set("Authorization", "Bearer "+tok)
		}
		for k, v := range hdr {
			r.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		h(w, r)
		return w
	}
	ana, bia := idp.token(t, good("ana")), idp.token(t, good("bia"))

	if w := call(app.HandleAccount, "GET", "", "", nil); w.Code != 401 {
		t.Fatalf("sem token: %d", w.Code)
	}
	if w := call(app.HandleAccount, "GET", "lixo", "", nil); w.Code != 401 {
		t.Fatalf("token lixo: %d", w.Code)
	}
	if w := call(app.HandleSimDeposit, "POST", ana, `{"usdc":100}`, nil); w.Code != 200 {
		t.Fatalf("depósito ana: %d %s", w.Code, w.Body)
	}
	var acc struct{ Balance float64 }
	_ = json.Unmarshal(call(app.HandleAccount, "GET", bia, "", nil).Body.Bytes(), &acc)
	if acc.Balance != 0 {
		t.Fatalf("bia enxergou saldo da ana: %v", acc.Balance)
	}
	// bia não consegue sacar o dinheiro da ana
	w := call(app.HandleWithdraw, "POST", bia, `{"usdc":10,"pix_key":"a@b.com"}`, map[string]string{"Idempotency-Key": "bia-idem-1"})
	if w.Code != 400 {
		t.Fatalf("bia sacou sem saldo: %d %s", w.Code, w.Body)
	}
	w = call(app.HandleWithdraw, "POST", ana, `{"usdc":10,"pix_key":"a@b.com"}`, map[string]string{"Idempotency-Key": "ana-idem-1"})
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"completed"`) {
		t.Fatalf("saque ana: %d %s", w.Code, w.Body)
	}
	// mesma Idempotency-Key em outra conta não vaza o lançamento da ana
	w = call(app.HandleWithdraw, "POST", bia, `{"usdc":10,"pix_key":"a@b.com"}`, map[string]string{"Idempotency-Key": "ana-idem-1"})
	if w.Code != 400 {
		t.Fatalf("idempotência vazou entre contas: %d %s", w.Code, w.Body)
	}
}
