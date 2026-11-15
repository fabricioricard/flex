package core

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"math"
	"time"
)

// ---- Interfaces: troque as implementações mock por integrações reais ----

// YieldProvider: onde o USDC fica rendendo (ex.: Aave/Morpho/Compound via vault).
type YieldProvider interface {
	APY() (float64, error)                            // ex.: 0.052 = 5,2% a.a.
	Withdraw(ctx context.Context, usdc float64) error // resgata liquidez para pagar o saque
}

// FXProvider: cotação USDC -> BRL já com spread.
type FXProvider interface {
	Quote(usdc float64) (rate float64, brl float64, err error)
}

type PixPayout struct {
	Key       string
	KeyType   string
	AmountBRL float64
	Reference string // idempotência no provedor
}

// PixProvider: liquidação PIX (ex.: Transfero, BRLA, Bitso, Cobre, etc.).
type PixProvider interface {
	Send(ctx context.Context, p PixPayout) (e2eID string, err error)
}

// ---- Mocks (modo sandbox) ----

type MockYield struct{ apy float64 }

func (m MockYield) APY() (float64, error) { return m.apy, nil }
func (m MockYield) Withdraw(ctx context.Context, usdc float64) error {
	time.Sleep(150 * time.Millisecond)
	return nil
}

type MockFX struct {
	Base   float64 // BRL por USDC
	Spread float64 // ex.: 0.006 = 0,6%
}

func (m MockFX) Quote(usdc float64) (float64, float64, error) {
	if usdc <= 0 {
		return 0, 0, errors.New("valor inválido")
	}
	// leve oscilação para parecer mercado
	wobble := 1 + 0.002*math.Sin(float64(time.Now().Unix())/60)
	mid := m.Base * wobble
	rate := mid * (1 - m.Spread)
	return round(rate, 4), round(usdc*rate, 2), nil
}

type MockPix struct{}

func (MockPix) Send(ctx context.Context, p PixPayout) (string, error) {
	time.Sleep(400 * time.Millisecond)
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return "E00000000" + time.Now().Format("20060102") + hex.EncodeToString(b)[:14], nil
}

func round(v float64, d int) float64 {
	p := math.Pow10(d)
	return math.Round(v*p) / p
}
