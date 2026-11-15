package core

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const googleCertURL = "https://www.googleapis.com/robot/v1/metadata/x509/securetoken@system.gserviceaccount.com"

// Verifier valida ID tokens do Firebase Auth (JWT RS256) sem depender do Admin SDK.
type Verifier struct {
	projectID string
	certURL   string
	hc        *http.Client

	mu        sync.Mutex
	keys      map[string]*rsa.PublicKey
	expires   time.Time
	lastFetch time.Time
}

func NewVerifier(projectID string) *Verifier {
	return &Verifier{
		projectID: projectID,
		certURL:   googleCertURL,
		hc:        &http.Client{Timeout: 8 * time.Second},
	}
}

func (v *Verifier) fetch() error {
	resp, err := v.hc.Get(v.certURL)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("certs: status %d", resp.StatusCode)
	}
	var raw map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return err
	}
	keys := map[string]*rsa.PublicKey{}
	for kid, p := range raw {
		blk, _ := pem.Decode([]byte(p))
		if blk == nil {
			continue
		}
		cert, err := x509.ParseCertificate(blk.Bytes)
		if err != nil {
			continue
		}
		if pub, ok := cert.PublicKey.(*rsa.PublicKey); ok {
			keys[kid] = pub
		}
	}
	if len(keys) == 0 {
		return errors.New("certs: nenhuma chave válida")
	}
	maxAge := time.Hour
	if cc := resp.Header.Get("Cache-Control"); cc != "" {
		if i := strings.Index(cc, "max-age="); i >= 0 {
			rest := cc[i+len("max-age="):]
			j := 0
			for j < len(rest) && rest[j] >= '0' && rest[j] <= '9' {
				j++
			}
			if n, err := strconv.Atoi(rest[:j]); err == nil && n > 0 {
				maxAge = time.Duration(n) * time.Second
			}
		}
	}
	v.keys = keys
	v.lastFetch = time.Now()
	v.expires = v.lastFetch.Add(maxAge)
	return nil
}

func (v *Verifier) key(kid string) (*rsa.PublicKey, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if k, ok := v.keys[kid]; ok && time.Now().Before(v.expires) {
		return k, nil
	}
	// Evita martelar o endpoint do Google com kids inválidos.
	if v.keys != nil && time.Since(v.lastFetch) < 30*time.Second {
		if k, ok := v.keys[kid]; ok {
			return k, nil
		}
		return nil, errors.New("chave desconhecida")
	}
	if err := v.fetch(); err != nil {
		return nil, err
	}
	k, ok := v.keys[kid]
	if !ok {
		return nil, errors.New("chave desconhecida")
	}
	return k, nil
}

func b64(s string) ([]byte, error) { return base64.RawURLEncoding.DecodeString(s) }

// Verify confere assinatura e claims do token e devolve o uid do usuário.
func (v *Verifier) Verify(token string) (string, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "", errors.New("token malformado")
	}
	hb, err := b64(parts[0])
	if err != nil {
		return "", errors.New("token malformado")
	}
	var h struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	if json.Unmarshal(hb, &h) != nil || h.Alg != "RS256" || h.Kid == "" {
		return "", errors.New("cabeçalho inválido")
	}
	pub, err := v.key(h.Kid)
	if err != nil {
		return "", err
	}
	sig, err := b64(parts[2])
	if err != nil {
		return "", errors.New("assinatura inválida")
	}
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if rsa.VerifyPKCS1v15(pub, crypto.SHA256, sum[:], sig) != nil {
		return "", errors.New("assinatura inválida")
	}
	pb, err := b64(parts[1])
	if err != nil {
		return "", errors.New("token malformado")
	}
	var c struct {
		Aud      string `json:"aud"`
		Iss      string `json:"iss"`
		Sub      string `json:"sub"`
		Exp      int64  `json:"exp"`
		Iat      int64  `json:"iat"`
		AuthTime int64  `json:"auth_time"`
	}
	if json.Unmarshal(pb, &c) != nil {
		return "", errors.New("token malformado")
	}
	now := time.Now().Unix()
	const skew = 60
	switch {
	case c.Aud != v.projectID:
		return "", errors.New("audiência inválida")
	case c.Iss != "https://securetoken.google.com/"+v.projectID:
		return "", errors.New("emissor inválido")
	case c.Sub == "" || len(c.Sub) > 128:
		return "", errors.New("usuário inválido")
	case c.Exp <= now-skew:
		return "", errors.New("token expirado")
	case c.Iat > now+skew || c.AuthTime > now+skew:
		return "", errors.New("token do futuro")
	}
	return c.Sub, nil
}
