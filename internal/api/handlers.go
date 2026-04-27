package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/nithinkuma/drift-checker/internal/analysis"
	"github.com/nithinkuma/drift-checker/internal/argocd"
	"github.com/nithinkuma/drift-checker/internal/config"
	"github.com/nithinkuma/drift-checker/internal/domain"
	"github.com/nithinkuma/drift-checker/internal/store"
)

// Handler holds shared dependencies for all HTTP handlers.
type Handler struct {
	cfg config.Config
	db  *store.Store
}

// NewHandler creates a Handler.
func NewHandler(cfg config.Config, db *store.Store) *Handler {
	return &Handler{cfg: cfg, db: db}
}

// ---- shared types ----

type errorResponse struct {
	Error string `json:"error"`
	Code  string `json:"code"`
}

func respondJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func respondError(w http.ResponseWriter, status int, code, msg string) {
	respondJSON(w, status, errorResponse{Error: msg, Code: code})
}

// ---- handlers ----

// Healthz returns 200 OK.
func (h *Handler) Healthz(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// ArgoProjects lists all AppProject names from ArgoCD directly.
// Used by the UI to populate the project dropdown without requiring the user
// to know project names in advance.
//
//	GET /api/v1/argocd/projects
func (h *Handler) ArgoProjects(w http.ResponseWriter, r *http.Request) {
	argoURL, token, ok := h.resolveCredentials(w)
	if !ok {
		return
	}
	client := newArgoClient(argoURL, token, h.analysisCfg())
	projects, err := client.ListProjects(r.Context())
	if err != nil {
		handleArgoError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"projects": projects})
}

// Projects lists all projects that have been synced.
//
//	GET /api/v1/projects
func (h *Handler) Projects(w http.ResponseWriter, r *http.Request) {
	projects, err := h.db.Projects(r.Context())
	if err != nil {
		respondError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}
	if projects == nil {
		projects = []string{}
	}
	respondJSON(w, http.StatusOK, map[string]any{"projects": projects})
}

// Sync fetches fresh data from ArgoCD for a project and persists it.
//
//	POST /api/v1/{project}/sync
func (h *Handler) Sync(w http.ResponseWriter, r *http.Request) {
	project, argoURL, token, ok := h.projectRequest(w, r)
	if !ok {
		return
	}

	report, err := analysis.Run(r.Context(), argoURL, token, project, h.analysisCfg())
	if err != nil {
		handleArgoError(w, err)
		return
	}

	if err := h.db.Sync(r.Context(), project, report.AppSets); err != nil {
		respondError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}

	respondJSON(w, http.StatusOK, map[string]any{
		"project":   project,
		"synced_at": time.Now().UTC().Format(time.RFC3339),
		"summary":   report.Summary,
	})
}

// Regions returns a flat region table — one row per AppSet per region.
//
//	GET /api/v1/{project}/regions
func (h *Handler) Regions(w http.ResponseWriter, r *http.Request) {
	project, appsets, ok := h.loadProject(w, r)
	if !ok {
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{
		"project": project,
		"rows":    analysis.FlattenRegions(appsets),
	})
}

// Builds returns a flat build table — one row per image per region per AppSet.
//
//	GET /api/v1/{project}/builds
func (h *Handler) Builds(w http.ResponseWriter, r *http.Request) {
	project, appsets, ok := h.loadProject(w, r)
	if !ok {
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{
		"project": project,
		"rows":    analysis.FlattenBuilds(appsets),
	})
}

// Resources returns a flat resource table — one row per workload per region per AppSet.
//
//	GET /api/v1/{project}/resources
func (h *Handler) Resources(w http.ResponseWriter, r *http.Request) {
	project, appsets, ok := h.loadProject(w, r)
	if !ok {
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{
		"project": project,
		"rows":    analysis.FlattenResources(appsets),
	})
}

// DiffBuilds returns the cross-region image tag comparison.
// Only rows where has_diff=true are returned by default.
// ?format=csv   → CSV file download
// ?appset=name  → scope to one AppSet
// ?all=true     → include non-drifted rows as well
//
//	GET /api/v1/{project}/diff/builds
func (h *Handler) DiffBuilds(w http.ResponseWriter, r *http.Request) {
	project, appsets, ok := h.loadProject(w, r)
	if !ok {
		return
	}

	diffs := analysis.BuildDiffs(appsets)
	diffs = filterBuildDiffs(diffs, r.URL.Query().Get("appset"), r.URL.Query().Get("all") != "true")

	if r.URL.Query().Get("format") == "csv" {
		filename := fmt.Sprintf("build-diff-%s.csv", project)
		w.Header().Set("Content-Type", "text/csv")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
		if err := writeBuildDiffCSV(w, diffs); err != nil {
			slog.Error("csv write error", "err", err)
		}
		return
	}

	respondJSON(w, http.StatusOK, map[string]any{
		"project": project,
		"diffs":   diffs,
	})
}

// DiffResources returns the cross-region workload state comparison.
// Only rows where has_diff=true are returned by default.
// ?format=csv   → CSV file download
// ?appset=name  → scope to one AppSet
// ?all=true     → include non-drifted rows as well
//
//	GET /api/v1/{project}/diff/resources
func (h *Handler) DiffResources(w http.ResponseWriter, r *http.Request) {
	project, appsets, ok := h.loadProject(w, r)
	if !ok {
		return
	}

	diffs := analysis.ResourceDiffs(appsets)
	diffs = filterResourceDiffs(diffs, r.URL.Query().Get("appset"), r.URL.Query().Get("all") != "true")

	if r.URL.Query().Get("format") == "csv" {
		filename := fmt.Sprintf("resource-diff-%s.csv", project)
		w.Header().Set("Content-Type", "text/csv")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
		if err := writeResourceDiffCSV(w, diffs); err != nil {
			slog.Error("csv write error", "err", err)
		}
		return
	}

	respondJSON(w, http.StatusOK, map[string]any{
		"project": project,
		"diffs":   diffs,
	})
}

// ---- helpers ----

// loadProject resolves the project from the URL, loads its AppSets from the
// database, and returns them ready for table generation.
func (h *Handler) loadProject(w http.ResponseWriter, r *http.Request) (project string, appsets []domain.AppSet, ok bool) {
	project = chi.URLParam(r, "project")
	if project == "" {
		respondError(w, http.StatusBadRequest, "INVALID_REQUEST", "project is required")
		return
	}

	lastSync, err := h.db.LastSync(r.Context(), project)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}
	if lastSync.IsZero() {
		respondError(w, http.StatusNotFound, "NOT_SYNCED",
			fmt.Sprintf("project %q has not been synced yet — POST /api/v1/%s/sync first", project, project))
		return
	}

	appsets, err = h.db.LoadAppSets(r.Context(), project)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}
	ok = true
	return
}

func (h *Handler) projectRequest(w http.ResponseWriter, r *http.Request) (project, argoURL, token string, ok bool) {
	project = chi.URLParam(r, "project")
	if project == "" {
		respondError(w, http.StatusBadRequest, "INVALID_REQUEST", "project is required")
		return
	}
	argoURL, token, ok = h.resolveCredentials(w)
	return
}

func (h *Handler) resolveCredentials(w http.ResponseWriter) (argoURL, token string, ok bool) {
	argoURL = h.cfg.ArgoURL
	token = h.cfg.ArgoToken
	if argoURL == "" || token == "" {
		respondError(w, http.StatusInternalServerError, "MISCONFIGURED",
			"server is missing ARGOCD_URL or ARGOCD_TOKEN — set them as environment variables")
		return "", "", false
	}
	return argoURL, token, true
}

func (h *Handler) analysisCfg() analysis.Config {
	return analysis.Config{
		TLSSkipVerify: h.cfg.ArgoTLSSkipVerify,
		HTTPTimeout:   h.cfg.ArgoHTTPTimeout,
		MaxApps:       h.cfg.ArgoMaxApps,
	}
}

func newArgoClient(argoURL, token string, cfg analysis.Config) *argocd.Client {
	return argocd.NewClient(argoURL, token, argocd.ClientConfig{
		TLSSkipVerify: cfg.TLSSkipVerify,
		Timeout:       cfg.HTTPTimeout,
		MaxApps:       cfg.MaxApps,
	})
}

func handleArgoError(w http.ResponseWriter, err error) {
	var ae *argocd.ArgoError
	if errors.As(err, &ae) {
		switch ae.Code {
		case "ARGOCD_AUTH_FAILED":
			respondError(w, http.StatusUnauthorized, ae.Code, ae.Message)
		case "ARGOCD_UNREACHABLE":
			respondError(w, http.StatusBadGateway, ae.Code, ae.Message)
		default:
			respondError(w, http.StatusBadGateway, ae.Code, ae.Message)
		}
		return
	}
	respondError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
}

func filterBuildDiffs(diffs []domain.BuildDiff, appset string, onlyDrifted bool) []domain.BuildDiff {
	var out []domain.BuildDiff
	for _, d := range diffs {
		if appset != "" && d.AppSet != appset {
			continue
		}
		if onlyDrifted && !d.HasDiff {
			continue
		}
		out = append(out, d)
	}
	return out
}

func filterResourceDiffs(diffs []domain.ResourceDiff, appset string, onlyDrifted bool) []domain.ResourceDiff {
	var out []domain.ResourceDiff
	for _, d := range diffs {
		if appset != "" && d.AppSet != appset {
			continue
		}
		if onlyDrifted && !d.HasDiff {
			continue
		}
		out = append(out, d)
	}
	return out
}
