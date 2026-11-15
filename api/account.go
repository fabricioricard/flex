package handler

import (
	"net/http"

	"usdcflex/core"
)

// Handler atende GET /api/account na Vercel.
func Handler(w http.ResponseWriter, r *http.Request) {
	core.Default().HandleAccount(w, r)
}
