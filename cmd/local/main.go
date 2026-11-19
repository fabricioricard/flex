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
	mux.HandleFunc("/api/health", app.HandleHealth)
	mux.HandleFunc("/api/me", app.HandleMe)
	mux.HandleFunc("/api/support", app.HandleSupport)
	mux.HandleFunc("/api/admin-support", app.HandleAdminSupport)
	mux.HandleFunc("/api/whoami", app.HandleWhoAmI)
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
	log.Printf("Flex local em http://localhost:%s", port)
	log.Fatal(http.ListenAndServe(":"+port, mux))
}
