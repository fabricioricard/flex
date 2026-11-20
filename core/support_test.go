package core

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func supportApp(t *testing.T) (*App, *testIdP) {
	idp := newTestIdP(t)
	return &App{auth: idp.verifier(), support: NewMemSupport(), admins: map[string]bool{"equipe@flex.app": true}}, idp
}

func withEmail(uid, email string, verified bool) map[string]any {
	c := good(uid)
	c["email"] = email
	c["email_verified"] = verified
	return c
}

func do(h http.HandlerFunc, method, body, tok, query string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "/x"+query, strings.NewReader(body))
	if tok != "" {
		r.Header.Set("Authorization", "Bearer "+tok)
	}
	w := httptest.NewRecorder()
	h(w, r)
	return w
}

func msgsOf(t *testing.T, w *httptest.ResponseRecorder) []Msg {
	t.Helper()
	var out struct{ Messages []Msg }
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	return out.Messages
}

func TestSupportFlow(t *testing.T) {
	app, idp := supportApp(t)
	ana := idp.token(t, withEmail("ana", "ana@x.com", true))
	bia := idp.token(t, withEmail("bia", "bia@x.com", true))

	if w := do(app.HandleSupport, "GET", "", "", ""); w.Code != 401 {
		t.Fatalf("sem login: %d", w.Code)
	}
	if ms := msgsOf(t, do(app.HandleSupport, "GET", "", ana, "")); len(ms) != 0 {
		t.Fatal("conversa nova deveria estar vazia")
	}
	for _, bad := range []string{`{"text":""}`, `{"text":"   "}`, `{"text":"` + strings.Repeat("a", 2001) + `"}`, `lixo`} {
		if w := do(app.HandleSupport, "POST", bad, ana, ""); w.Code != 400 {
			t.Fatalf("%.20q deveria dar 400, deu %d", bad, w.Code)
		}
	}
	if w := do(app.HandleSupport, "POST", `{"text":" Oi, preciso de ajuda ","addr":"0xnada"}`, ana, ""); w.Code != 200 {
		t.Fatalf("envio: %d %s", w.Code, w.Body)
	}
	do(app.HandleSupport, "POST", `{"text":"outra","addr":"0x1234567890abcdef1234567890ABCDEF12345678"}`, ana, "")
	ms := msgsOf(t, do(app.HandleSupport, "GET", "", ana, ""))
	if len(ms) != 2 || ms[0].Text != "Oi, preciso de ajuda" || ms[0].From != "user" || ms[1].Text != "outra" {
		t.Fatalf("mensagens: %+v", ms)
	}
	// isolamento: Bia não vê a conversa da Ana
	if ms := msgsOf(t, do(app.HandleSupport, "GET", "", bia, "")); len(ms) != 0 {
		t.Fatal("vazou conversa entre usuários")
	}
	// endereço inválido é descartado, válido é guardado
	th, _ := app.support.Threads(context.Background(), 10)
	if len(th) != 1 || th[0].Addr != "0x1234567890abcdef1234567890ABCDEF12345678" || th[0].Email != "ana@x.com" {
		t.Fatalf("thread: %+v", th)
	}
}

func TestSupportRateLimit(t *testing.T) {
	app, idp := supportApp(t)
	tok := idp.token(t, withEmail("ana", "ana@x.com", true))
	for i := 0; i < supportPerMinute; i++ {
		if w := do(app.HandleSupport, "POST", `{"text":"oi"}`, tok, ""); w.Code != 200 {
			t.Fatalf("msg %d: %d", i, w.Code)
		}
	}
	if w := do(app.HandleSupport, "POST", `{"text":"oi"}`, tok, ""); w.Code != 429 {
		t.Fatalf("esperava 429, veio %d", w.Code)
	}
}

func TestAdminSupport(t *testing.T) {
	app, idp := supportApp(t)
	ana := idp.token(t, withEmail("ana", "ana@x.com", true))
	adm := idp.token(t, withEmail("u-adm", "Equipe@Flex.app", true))
	admUnverified := idp.token(t, withEmail("u-adm", "equipe@flex.app", false))

	do(app.HandleSupport, "POST", `{"text":"Meu saldo sumiu?"}`, ana, "")

	if w := do(app.HandleAdminSupport, "GET", "", ana, ""); w.Code != 403 {
		t.Fatalf("usuário comum: %d", w.Code)
	}
	if w := do(app.HandleAdminSupport, "GET", "", admUnverified, ""); w.Code != 403 {
		t.Fatalf("e-mail não verificado: %d", w.Code)
	}
	if w := do(app.HandleAdminSupport, "GET", "", "", ""); w.Code != 401 {
		t.Fatalf("sem login: %d", w.Code)
	}
	w := do(app.HandleAdminSupport, "GET", "", adm, "")
	var list struct{ Threads []Thread }
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &list) != nil || len(list.Threads) != 1 || list.Threads[0].UID != "ana" || list.Threads[0].LastFrom != "user" {
		t.Fatalf("lista: %d %s", w.Code, w.Body)
	}
	if ms := msgsOf(t, do(app.HandleAdminSupport, "GET", "", adm, "?uid=ana")); len(ms) != 1 {
		t.Fatalf("admin lendo: %+v", ms)
	}
	for _, bad := range []string{`{"uid":"","text":"x"}`, `{"uid":"a/b","text":"x"}`, `{"uid":"ana","text":""}`} {
		if w := do(app.HandleAdminSupport, "POST", bad, adm, ""); w.Code != 400 {
			t.Fatalf("%s: %d", bad, w.Code)
		}
	}
	time.Sleep(2 * time.Millisecond)
	if w := do(app.HandleAdminSupport, "POST", `{"uid":"ana","text":"Oi! Vou verificar."}`, adm, ""); w.Code != 200 {
		t.Fatalf("resposta: %d %s", w.Code, w.Body)
	}
	ms := msgsOf(t, do(app.HandleSupport, "GET", "", ana, ""))
	if len(ms) != 2 || ms[1].From != "admin" || ms[1].Text != "Oi! Vou verificar." {
		t.Fatalf("usuário deveria ver a resposta: %+v", ms)
	}
	// resposta da equipe não apaga o e-mail da conversa
	th, _ := app.support.Threads(context.Background(), 10)
	if th[0].Email != "ana@x.com" || th[0].LastFrom != "admin" {
		t.Fatalf("thread: %+v", th[0])
	}
	// whoami
	var who struct{ Admin bool }
	_ = json.Unmarshal(do(app.HandleWhoAmI, "GET", "", adm, "").Body.Bytes(), &who)
	if !who.Admin {
		t.Fatal("whoami admin")
	}
	_ = json.Unmarshal(do(app.HandleWhoAmI, "GET", "", ana, "").Body.Bytes(), &who)
	if who.Admin {
		t.Fatal("whoami comum")
	}
}

func TestSupportNotConfigured(t *testing.T) {
	idp := newTestIdP(t)
	app := &App{auth: idp.verifier()}
	if w := do(app.HandleSupport, "GET", "", idp.token(t, good("ana")), ""); w.Code != 503 {
		t.Fatalf("sem store: %d", w.Code)
	}
}

// ---- Firestore REST contra um servidor falso ----

func TestFirestoreClient(t *testing.T) {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	der, _ := x509.MarshalPKCS8PrivateKey(key)
	pemKey := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))

	var calls []string
	tokens := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			tokens++
			r.ParseForm()
			parts := strings.Split(r.Form.Get("assertion"), ".")
			if len(parts) != 3 || r.Form.Get("grant_type") != "urn:ietf:params:oauth:grant-type:jwt-bearer" {
				w.WriteHeader(400)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "tok123", "expires_in": 3600})
			return
		}
		if r.Header.Get("Authorization") != "Bearer tok123" {
			w.WriteHeader(401)
			return
		}
		calls = append(calls, r.Method+" "+r.URL.Path+"?"+r.URL.RawQuery)
		switch {
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/messages"):
			// ordem decrescente por ts, como pedido
			_, _ = w.Write([]byte(`{"documents":[
			 {"fields":{"from":{"stringValue":"admin"},"text":{"stringValue":"segunda"},"ts":{"timestampValue":"2026-11-19T10:00:02Z"}}},
			 {"fields":{"from":{"stringValue":"user"},"text":{"stringValue":"primeira"},"ts":{"timestampValue":"2026-11-19T10:00:01Z"}}}]}`))
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/support_threads/ana"):
			_, _ = w.Write([]byte(`{"fields":{"adminReadAt":{"timestampValue":"2026-11-19T11:00:00Z"}}}`))
		case r.Method == "GET":
			_, _ = w.Write([]byte(`{"documents":[{"name":"projects/p/databases/(default)/documents/support_threads/ana","fields":{"email":{"stringValue":"ana@x.com"},"lastText":{"stringValue":"oi"},"lastFrom":{"stringValue":"user"},"lastAt":{"timestampValue":"2026-11-19T10:00:00Z"}}}]}`))
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	defer srv.Close()

	f, err := NewFirestoreSupport(`{"project_id":"p","client_email":"sa@p.iam","private_key":` + mustJSON(pemKey) + `,"token_uri":"` + srv.URL + `/token"}`)
	if err != nil {
		t.Fatal(err)
	}
	f.base = srv.URL + "/v1/projects/p/databases/(default)/documents"

	now := time.Now()
	if err := f.AddMessage(context.Background(), Thread{UID: "ana", Email: "ana@x.com", LastText: "oi", LastFrom: "user", LastAt: now}, Msg{From: "user", Text: "oi", TS: now}); err != nil {
		t.Fatal(err)
	}
	ms, err := f.Messages(context.Background(), "ana", 50)
	if err != nil || len(ms) != 2 || ms[0].Text != "primeira" || ms[1].Text != "segunda" || ms[1].From != "admin" {
		t.Fatalf("mensagens: %v %+v", err, ms)
	}
	ts, err := f.Threads(context.Background(), 10)
	if err != nil || len(ts) != 1 || ts[0].UID != "ana" || ts[0].Email != "ana@x.com" {
		t.Fatalf("threads: %v %+v", err, ts)
	}
	rd, err := f.ReadAt(context.Background(), "ana")
	if err != nil || !rd.Equal(time.Date(2026, 11, 19, 11, 0, 0, 0, time.UTC)) {
		t.Fatalf("readAt: %v %v", err, rd)
	}
	if err := f.MarkRead(context.Background(), "ana", now); err != nil {
		t.Fatal(err)
	}
	if tokens != 1 {
		t.Fatalf("token deveria ser reaproveitado, pedidos: %d", tokens)
	}
	joined := strings.Join(calls, "\n")
	for _, want := range []string{"POST /v1/projects/p/databases/(default)/documents/support_threads/ana/messages", "PATCH /v1/projects/p/databases/(default)/documents/support_threads/ana?", "orderBy=ts+desc", "updateMask.fieldPaths=email", "updateMask.fieldPaths=adminReadAt"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("faltou %q em:\n%s", want, joined)
		}
	}
	// base64 e JSON inválido
	if _, err := NewFirestoreSupport("lixo"); err == nil {
		t.Fatal("deveria recusar")
	}
}

func mustJSON(s string) string { b, _ := json.Marshal(s); return string(b) }

func TestReadReceiptAndNotify(t *testing.T) {
	app, idp := supportApp(t)
	var got []string
	tg := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		got = append(got, r.URL.Path+"|"+r.Form.Get("chat_id")+"|"+r.Form.Get("text"))
	}))
	defer tg.Close()
	app.tg = &telegram{token: "T0K", chat: "42", api: tg.URL, hc: tg.Client()}
	ana := idp.token(t, withEmail("ana", "ana@x.com", true))
	adm := idp.token(t, withEmail("u-adm", "equipe@flex.app", true))

	do(app.HandleSupport, "POST", `{"text":"preciso de ajuda"}`, ana, "")
	if len(got) != 1 || !strings.Contains(got[0], "/botT0K/sendMessage|42|") || !strings.Contains(got[0], "ana@x.com") || !strings.Contains(got[0], "preciso de ajuda") {
		t.Fatalf("telegram: %v", got)
	}
	read := func() time.Time {
		var o struct{ ReadAt time.Time }
		_ = json.Unmarshal(do(app.HandleSupport, "GET", "", ana, "").Body.Bytes(), &o)
		return o.ReadAt
	}
	if !read().IsZero() {
		t.Fatal("antes de a equipe abrir não pode estar lido")
	}
	do(app.HandleAdminSupport, "GET", "", adm, "?uid=ana")
	if read().IsZero() {
		t.Fatal("depois de a equipe abrir deveria estar lido")
	}
	// sem Telegram configurado, enviar continua funcionando
	app.tg = nil
	if w := do(app.HandleSupport, "POST", `{"text":"oi"}`, ana, ""); w.Code != 200 {
		t.Fatal(w.Code)
	}
}
