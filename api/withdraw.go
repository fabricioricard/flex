package handler

import (
	"net/http"

	"usdcflex/internal/core"
)

// Handler atende POST /api/withdraw na Vercel.
func Handler(w http.ResponseWriter, r *http.Request) {
	core.Default().HandleWithdraw(w, r)
}
