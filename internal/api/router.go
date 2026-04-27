package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/nithinkuma/drift-checker/internal/config"
	"github.com/nithinkuma/drift-checker/internal/store"
)

// NewRouter creates and returns the application router.
//
// Routes:
//
//	GET  /healthz
//	GET  /                                         embedded UI
//	GET  /api/v1/projects                          list synced projects
//	POST /api/v1/{project}/sync                    fetch from ArgoCD → store
//	GET  /api/v1/{project}/regions                 region table
//	GET  /api/v1/{project}/builds                  build table (all images)
//	GET  /api/v1/{project}/resources               resource table (workloads only)
//	GET  /api/v1/{project}/diff/builds             image tag diffs  (?format=csv, ?appset=, ?all=true)
//	GET  /api/v1/{project}/diff/resources          workload state diffs  (?format=csv, ?appset=, ?all=true)
func NewRouter(cfg config.Config, db *store.Store) http.Handler {
	h := NewHandler(cfg, db)
	r := chi.NewRouter()

	r.Use(Recoverer)
	r.Use(Logger)

	r.Get("/healthz", h.Healthz)
	r.Get("/", serveUI)

	r.Route("/api/v1", func(r chi.Router) {
		r.Get("/projects", h.Projects)

		r.Route("/{project}", func(r chi.Router) {
			r.Post("/sync", h.Sync)
			r.Get("/regions", h.Regions)
			r.Get("/builds", h.Builds)
			r.Get("/resources", h.Resources)
			r.Get("/diff/builds", h.DiffBuilds)
			r.Get("/diff/resources", h.DiffResources)
		})
	})

	return r
}
