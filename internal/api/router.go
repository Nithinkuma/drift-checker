package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/nithinkuma/drift-checker/internal/config"
)

// NewRouter creates and returns the application router.
func NewRouter(cfg config.Config) http.Handler {
	h := NewHandler(cfg)
	r := chi.NewRouter()

	r.Use(Recoverer)
	r.Use(Logger)

	r.Get("/healthz", h.Healthz)

	r.Route("/api/v1", func(r chi.Router) {
		r.Post("/analyze", h.Analyze)
		r.Get("/analyze/{project}/appsets", h.ListAppSets)
		r.Get("/analyze/{project}/appsets/{appset}", h.GetAppSet)
		r.Get("/analyze/{project}/drift", h.ListDrift)
	})

	return r
}
