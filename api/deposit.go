package handler

import (
	"net/http"

	"usdcflex/internal/core"
)

// Handler atende POST /api/deposit (sandbox) na Vercel.
func Handler(w http.ResponseWriter, r *http.Request) {
	core.Default().HandleSimDeposit(w, r)
}
