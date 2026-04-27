package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/nithinkuma/drift-checker/internal/config"
)

// NewRouter creates and returns the application router.
//
// Routes:
//
//	GET  /healthz
//	GET  /api/v1/{project}/regions                  region table
//	GET  /api/v1/{project}/builds                   build table (all images)
//	GET  /api/v1/{project}/resources                resource table (workloads only)
//	GET  /api/v1/{project}/diff/builds              image tag diffs  (?format=csv, ?appset=, ?all=true)
//	GET  /api/v1/{project}/diff/resources           workload state diffs  (?format=csv, ?appset=, ?all=true)
func NewRouter(cfg config.Config) http.Handler {
	h := NewHandler(cfg)
	r := chi.NewRouter()

	r.Use(Recoverer)
	r.Use(Logger)

	r.Get("/healthz", h.Healthz)

	r.Route("/api/v1/{project}", func(r chi.Router) {
		r.Get("/regions", h.Regions)
		r.Get("/builds", h.Builds)
		r.Get("/resources", h.Resources)
		r.Get("/diff/builds", h.DiffBuilds)
		r.Get("/diff/resources", h.DiffResources)
	})

	return r
}
