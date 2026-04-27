package webui

import (
	"embed"
	"io"
	"net/http"
	"strings"
)

//go:embed index.html
var content embed.FS

func Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}

		f, err := content.Open("index.html")
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		defer f.Close()

		data, err := io.ReadAll(f)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")

		contentType := r.Header.Get("Accept")
		if strings.Contains(contentType, "text/html") || contentType == "" || strings.HasPrefix(contentType, "*/*") {
			w.Write(data)
			return
		}
		w.Write(data)
	})
}
