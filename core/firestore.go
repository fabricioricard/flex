package core

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Cliente mínimo do Firestore (API REST) com chave de conta de serviço, sem o SDK do Google.
// Coleções: support_threads/{uid} (resumo) e support_threads/{uid}/messages/{id}.

type FirestoreSupport struct {
	email, tokenURI, base string
	key                   *rsa.PrivateKey
	hc                    *http.Client

	mu     sync.Mutex
	token  string
	expiry time.Time
}

type serviceAccount struct {
	ProjectID   string `json:"project_id"`
	ClientEmail string `json:"client_email"`
	PrivateKey  string `json:"private_key"`
	TokenURI    string `json:"token_uri"`
}

// NewFirestoreSupport aceita o JSON da conta de serviço puro ou em base64.
func NewFirestoreSupport(raw string) (*FirestoreSupport, error) {
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(raw, "{") {
		b, err := base64.StdEncoding.DecodeString(raw)
		if err != nil {
			return nil, errors.New("FIREBASE_SERVICE_ACCOUNT inválida")
		}
		raw = string(b)
	}
	var sa serviceAccount
	if err := json.Unmarshal([]byte(raw), &sa); err != nil || sa.ProjectID == "" || sa.ClientEmail == "" {
		return nil, errors.New("FIREBASE_SERVICE_ACCOUNT inválida")
	}
	blk, _ := pem.Decode([]byte(sa.PrivateKey))
	if blk == nil {
		return nil, errors.New("chave privada inválida")
	}
	k, err := x509.ParsePKCS8PrivateKey(blk.Bytes)
	if err != nil {
		return nil, errors.New("chave privada inválida")
	}
	rk, ok := k.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("chave privada inválida")
	}
	if sa.TokenURI == "" {
		sa.TokenURI = "https://oauth2.googleapis.com/token"
	}
	return &FirestoreSupport{
		email:    sa.ClientEmail,
		tokenURI: sa.TokenURI,
		base:     "https://firestore.googleapis.com/v1/projects/" + sa.ProjectID + "/databases/(default)/documents",
		key:      rk,
		hc:       &http.Client{Timeout: 10 * time.Second},
	}, nil
}

func b64u(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func (f *FirestoreSupport) accessToken(ctx context.Context) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.token != "" && time.Now().Before(f.expiry.Add(-2*time.Minute)) {
		return f.token, nil
	}
	now := time.Now().Unix()
	hdr, _ := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT"})
	cl, _ := json.Marshal(map[string]any{
		"iss": f.email, "scope": "https://www.googleapis.com/auth/datastore",
		"aud": f.tokenURI, "iat": now, "exp": now + 3600,
	})
	in := b64u(hdr) + "." + b64u(cl)
	sum := sha256.Sum256([]byte(in))
	sig, err := rsa.SignPKCS1v15(nil, f.key, crypto.SHA256, sum[:])
	if err != nil {
		return "", err
	}
	form := url.Values{"grant_type": {"urn:ietf:params:oauth:grant-type:jwt-bearer"}, "assertion": {in + "." + b64u(sig)}}
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, f.tokenURI, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := f.hc.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	var out struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if resp.StatusCode != 200 || json.Unmarshal(body, &out) != nil || out.AccessToken == "" {
		return "", fmt.Errorf("token: status %d", resp.StatusCode)
	}
	f.token = out.AccessToken
	f.expiry = time.Now().Add(time.Duration(out.ExpiresIn) * time.Second)
	return f.token, nil
}

func (f *FirestoreSupport) do(ctx context.Context, method, path string, q url.Values, body any) ([]byte, error) {
	tok, err := f.accessToken(ctx)
	if err != nil {
		return nil, err
	}
	u := f.base + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequestWithContext(ctx, method, u, rd)
	req.Header.Set("Authorization", "Bearer "+tok)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := f.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("firestore: status %d", resp.StatusCode)
	}
	return b, nil
}

type fsDoc struct {
	Fields map[string]struct {
		StringValue    string `json:"stringValue"`
		TimestampValue string `json:"timestampValue"`
	} `json:"fields"`
}

func sv(s string) map[string]string { return map[string]string{"stringValue": s} }
func tv(t time.Time) map[string]string {
	return map[string]string{"timestampValue": t.UTC().Format(time.RFC3339Nano)}
}

func (f *FirestoreSupport) AddMessage(ctx context.Context, t Thread, m Msg) error {
	if _, err := f.do(ctx, http.MethodPost, "/support_threads/"+url.PathEscape(t.UID)+"/messages", nil, map[string]any{
		"fields": map[string]any{"from": sv(m.From), "text": sv(m.Text), "ts": tv(m.TS)},
	}); err != nil {
		return err
	}
	fields := map[string]any{"lastText": sv(t.LastText), "lastFrom": sv(t.LastFrom), "lastAt": tv(t.LastAt)}
	mask := url.Values{"updateMask.fieldPaths": {"lastText", "lastFrom", "lastAt"}}
	if t.Email != "" {
		fields["email"] = sv(t.Email)
		mask.Add("updateMask.fieldPaths", "email")
	}
	if t.Addr != "" {
		fields["addr"] = sv(t.Addr)
		mask.Add("updateMask.fieldPaths", "addr")
	}
	_, err := f.do(ctx, http.MethodPatch, "/support_threads/"+url.PathEscape(t.UID), mask, map[string]any{"fields": fields})
	return err
}

func (f *FirestoreSupport) Messages(ctx context.Context, uid string, limit int) ([]Msg, error) {
	b, err := f.do(ctx, http.MethodGet, "/support_threads/"+url.PathEscape(uid)+"/messages",
		url.Values{"orderBy": {"ts desc"}, "pageSize": {fmt.Sprint(limit)}}, nil)
	if err != nil {
		return nil, err
	}
	var out struct {
		Documents []fsDoc `json:"documents"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	ms := make([]Msg, 0, len(out.Documents))
	for i := len(out.Documents) - 1; i >= 0; i-- { // inverte: mais antiga primeiro
		d := out.Documents[i].Fields
		ts, _ := time.Parse(time.RFC3339Nano, d["ts"].TimestampValue)
		ms = append(ms, Msg{From: d["from"].StringValue, Text: d["text"].StringValue, TS: ts})
	}
	return ms, nil
}

func (f *FirestoreSupport) Threads(ctx context.Context, limit int) ([]Thread, error) {
	b, err := f.do(ctx, http.MethodGet, "/support_threads",
		url.Values{"orderBy": {"lastAt desc"}, "pageSize": {fmt.Sprint(limit)}}, nil)
	if err != nil {
		return nil, err
	}
	var out struct {
		Documents []struct {
			Name string `json:"name"`
			fsDoc
		} `json:"documents"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	ts := make([]Thread, 0, len(out.Documents))
	for _, d := range out.Documents {
		at, _ := time.Parse(time.RFC3339Nano, d.Fields["lastAt"].TimestampValue)
		ts = append(ts, Thread{
			UID: d.Name[strings.LastIndex(d.Name, "/")+1:], Email: d.Fields["email"].StringValue, Addr: d.Fields["addr"].StringValue,
			LastText: d.Fields["lastText"].StringValue, LastFrom: d.Fields["lastFrom"].StringValue, LastAt: at,
		})
	}
	return ts, nil
}
