// Servidor local para desenvolvimento: go run ./cmd/local
// Na Vercel, quem atende são as funções em /api e os arquivos em /public.
package main

import (
	"log"
	"mime"
	"net/http"
	"os"

	"usdcflex/core"
)

func main() {
	app := core.New()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/account", app.HandleAccount)
	mux.HandleFunc("/api/quote", app.HandleQuote)
	mux.HandleFunc("/api/withdraw", app.HandleWithdraw)
	mux.HandleFunc("/api/deposit", app.HandleSimDeposit)
	_ = mime.AddExtensionType(".webmanifest", "application/manifest+json")
	files := http.FileServer(http.Dir("public"))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/sw.js" || r.URL.Path == "/" {
			w.Header().Set("Cache-Control", "no-cache")
		}
		files.ServeHTTP(w, r)
	})
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	log.Printf("USDC Flex local em http://localhost:%s", port)
	log.Fatal(http.ListenAndServe(":"+port, mux))
}
