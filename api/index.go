package handler

import (
	"net/http"
	"path"
	"strings"

	"usdcflex/core"
)

// Handler é a única função da API na Vercel. O vercel.json reescreve
// /api/<rota> para /api/index?route=<rota>.
func Handler(w http.ResponseWriter, r *http.Request) {
	app := core.Default()
	route := strings.Trim(r.URL.Query().Get("route"), "/")
	if route == "" {
		route = path.Base(r.URL.Path)
	}
	switch route {
	case "account":
		app.HandleAccount(w, r)
	case "quote":
		app.HandleQuote(w, r)
	case "withdraw":
		app.HandleWithdraw(w, r)
	case "deposit":
		app.HandleSimDeposit(w, r)
	default:
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"rota não encontrada"}`))
	}
}
