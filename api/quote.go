package handler

import (
	"net/http"

	"usdcflex/core"
)

// Handler atende GET /api/quote na Vercel.
func Handler(w http.ResponseWriter, r *http.Request) {
	core.Default().HandleQuote(w, r)
}
