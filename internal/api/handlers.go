package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/nithinkuma/drift-checker/internal/analysis"
	"github.com/nithinkuma/drift-checker/internal/argocd"
	"github.com/nithinkuma/drift-checker/internal/config"
	"github.com/nithinkuma/drift-checker/internal/domain"
)

// Handler holds shared dependencies for all HTTP handlers.
type Handler struct {
	cfg config.Config
}

// NewHandler creates a Handler.
func NewHandler(cfg config.Config) *Handler {
	return &Handler{cfg: cfg}
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

// Regions returns a flat region table — one row per AppSet per region.
//
//	GET /api/v1/{project}/regions
func (h *Handler) Regions(w http.ResponseWriter, r *http.Request) {
	project, argoURL, token, ok := h.projectRequest(w, r)
	if !ok {
		return
	}
	appsets, err := h.fetchAppSets(r, argoURL, token, project)
	if err != nil {
		handleArgoError(w, err)
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
	project, argoURL, token, ok := h.projectRequest(w, r)
	if !ok {
		return
	}
	appsets, err := h.fetchAppSets(r, argoURL, token, project)
	if err != nil {
		handleArgoError(w, err)
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
	project, argoURL, token, ok := h.projectRequest(w, r)
	if !ok {
		return
	}
	appsets, err := h.fetchAppSets(r, argoURL, token, project)
	if err != nil {
		handleArgoError(w, err)
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
	project, argoURL, token, ok := h.projectRequest(w, r)
	if !ok {
		return
	}
	appsets, err := h.fetchAppSets(r, argoURL, token, project)
	if err != nil {
		handleArgoError(w, err)
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
	project, argoURL, token, ok := h.projectRequest(w, r)
	if !ok {
		return
	}
	appsets, err := h.fetchAppSets(r, argoURL, token, project)
	if err != nil {
		handleArgoError(w, err)
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

// fetchAppSets runs the full ArgoCD fetch + group + detect pipeline and
// returns the analyzed AppSet slice ready for table generation.
func (h *Handler) fetchAppSets(r *http.Request, argoURL, token, project string) ([]domain.AppSet, error) {
	report, err := analysis.Run(r.Context(), argoURL, token, project, h.analysisCfg())
	if err != nil {
		return nil, err
	}
	return report.AppSets, nil
}

func (h *Handler) projectRequest(w http.ResponseWriter, r *http.Request) (project, argoURL, token string, ok bool) {
	project = chi.URLParam(r, "project")
	if project == "" {
		respondError(w, http.StatusBadRequest, "INVALID_REQUEST", "project is required")
		return
	}
	argoURL, token, ok = h.resolveCredentials(w, "", "")
	return
}

func (h *Handler) resolveCredentials(w http.ResponseWriter, reqURL, reqToken string) (argoURL, token string, ok bool) {
	argoURL = reqURL
	if argoURL == "" {
		argoURL = h.cfg.ArgoURL
	}
	token = reqToken
	if token == "" {
		token = h.cfg.ArgoToken
	}
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
