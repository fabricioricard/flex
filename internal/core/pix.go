package core

import (
	"errors"
	"regexp"
	"strings"
)

var (
	reEmail = regexp.MustCompile(`^[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}$`)
	rePhone = regexp.MustCompile(`^\+55\d{10,11}$`)
	reEVP   = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
)

func onlyDigits(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func validCPF(s string) bool {
	if len(s) != 11 {
		return false
	}
	same := true
	for i := 1; i < 11; i++ {
		if s[i] != s[0] {
			same = false
		}
	}
	if same {
		return false
	}
	for t := 9; t < 11; t++ {
		sum := 0
		for i := 0; i < t; i++ {
			sum += int(s[i]-'0') * (t + 1 - i)
		}
		d := (sum * 10) % 11
		if d == 10 {
			d = 0
		}
		if d != int(s[t]-'0') {
			return false
		}
	}
	return true
}

func validCNPJ(s string) bool {
	if len(s) != 14 {
		return false
	}
	w1 := []int{5, 4, 3, 2, 9, 8, 7, 6, 5, 4, 3, 2}
	w2 := []int{6, 5, 4, 3, 2, 9, 8, 7, 6, 5, 4, 3, 2}
	calc := func(w []int) int {
		sum := 0
		for i, x := range w {
			sum += int(s[i]-'0') * x
		}
		r := sum % 11
		if r < 2 {
			return 0
		}
		return 11 - r
	}
	return calc(w1) == int(s[12]-'0') && calc(w2) == int(s[13]-'0')
}

// NormalizePixKey valida e normaliza a chave PIX. Retorna (chave, tipo).
// Telefone deve vir no formato +55DDDNUMERO (padrão do DICT).
func NormalizePixKey(raw string) (string, string, error) {
	k := strings.TrimSpace(raw)
	if k == "" {
		return "", "", errors.New("informe a chave PIX")
	}
	if reEmail.MatchString(k) {
		return strings.ToLower(k), "email", nil
	}
	if reEVP.MatchString(k) {
		return strings.ToLower(k), "aleatoria", nil
	}
	if strings.HasPrefix(k, "+") {
		p := "+" + onlyDigits(k)
		if rePhone.MatchString(p) {
			return p, "telefone", nil
		}
		return "", "", errors.New("telefone inválido (use +55DDDNÚMERO)")
	}
	d := onlyDigits(k)
	switch len(d) {
	case 11:
		if validCPF(d) {
			return d, "cpf", nil
		}
		return "", "", errors.New("CPF inválido (para telefone use +55...)")
	case 14:
		if validCNPJ(d) {
			return d, "cnpj", nil
		}
		return "", "", errors.New("CNPJ inválido")
	}
	return "", "", errors.New("chave PIX inválida")
}
