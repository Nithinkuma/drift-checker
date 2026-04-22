package api

import (
	"encoding/json"
	"errors"
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

// ---- request / response helpers ----

type analyzeRequest struct {
	ArgocdURL string `json:"argocd_url"`
	Token     string `json:"token"`
	Project   string `json:"project"`
}

type errorResponse struct {
	Error   string `json:"error"`
	Code    string `json:"code"`
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

// Healthz returns 200 OK — used by liveness probes.
func (h *Handler) Healthz(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// Analyze runs a full drift analysis for a project and returns the report.
//
//	POST /api/v1/analyze
func (h *Handler) Analyze(w http.ResponseWriter, r *http.Request) {
	var req analyzeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid JSON body")
		return
	}
	if req.ArgocdURL == "" || req.Token == "" || req.Project == "" {
		respondError(w, http.StatusBadRequest, "INVALID_REQUEST", "argocd_url, token, and project are required")
		return
	}

	report, err := analysis.Run(r.Context(), req.ArgocdURL, req.Token, req.Project, analysis.Config{
		TLSSkipVerify: h.cfg.ArgoTLSSkipVerify,
		HTTPTimeout:   h.cfg.ArgoHTTPTimeout,
		MaxApps:       h.cfg.ArgoMaxApps,
	})
	if err != nil {
		handleArgoError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, report)
}

// ListAppSets returns all AppSets for a project with a top-level drift flag.
//
//	GET /api/v1/analyze/{project}/appsets
func (h *Handler) ListAppSets(w http.ResponseWriter, r *http.Request) {
	req, ok := h.appSetRequest(w, r)
	if !ok {
		return
	}

	report, err := analysis.Run(r.Context(), req.ArgocdURL, req.Token, req.Project, analysis.Config{
		TLSSkipVerify: h.cfg.ArgoTLSSkipVerify,
		HTTPTimeout:   h.cfg.ArgoHTTPTimeout,
		MaxApps:       h.cfg.ArgoMaxApps,
	})
	if err != nil {
		handleArgoError(w, err)
		return
	}

	type appSetSummary struct {
		Name          string   `json:"name"`
		DriftDetected bool     `json:"drift_detected"`
		DriftTypes    []string `json:"drift_types,omitempty"`
		Regions       []string `json:"regions"`
	}
	summaries := make([]appSetSummary, 0, len(report.AppSets))
	for _, as := range report.AppSets {
		regions := uniqueRegions(as.Apps)
		summaries = append(summaries, appSetSummary{
			Name:          as.Name,
			DriftDetected: as.DriftDetected,
			DriftTypes:    as.DriftTypes,
			Regions:       regions,
		})
	}
	respondJSON(w, http.StatusOK, map[string]any{
		"project": report.Project,
		"appsets": summaries,
	})
}

// GetAppSet returns the full drift detail for one AppSet.
//
//	GET /api/v1/analyze/{project}/appsets/{appset}
func (h *Handler) GetAppSet(w http.ResponseWriter, r *http.Request) {
	appSetName := chi.URLParam(r, "appset")
	req, ok := h.appSetRequest(w, r)
	if !ok {
		return
	}

	report, err := analysis.Run(r.Context(), req.ArgocdURL, req.Token, req.Project, analysis.Config{
		TLSSkipVerify: h.cfg.ArgoTLSSkipVerify,
		HTTPTimeout:   h.cfg.ArgoHTTPTimeout,
		MaxApps:       h.cfg.ArgoMaxApps,
	})
	if err != nil {
		handleArgoError(w, err)
		return
	}

	for _, as := range report.AppSets {
		if as.Name == appSetName {
			respondJSON(w, http.StatusOK, as)
			return
		}
	}
	respondError(w, http.StatusNotFound, "NOT_FOUND", "appset not found: "+appSetName)
}

// ListDrift returns only the drifted AppSets, optionally filtered by drift type.
//
//	GET /api/v1/analyze/{project}/drift?drift_type=IMAGE_TAG_DRIFT
func (h *Handler) ListDrift(w http.ResponseWriter, r *http.Request) {
	req, ok := h.appSetRequest(w, r)
	if !ok {
		return
	}
	filterType := r.URL.Query().Get("drift_type")

	report, err := analysis.Run(r.Context(), req.ArgocdURL, req.Token, req.Project, analysis.Config{
		TLSSkipVerify: h.cfg.ArgoTLSSkipVerify,
		HTTPTimeout:   h.cfg.ArgoHTTPTimeout,
		MaxApps:       h.cfg.ArgoMaxApps,
	})
	if err != nil {
		handleArgoError(w, err)
		return
	}

	var drifted []domain.AppSet
	for _, as := range report.AppSets {
		if !as.DriftDetected {
			continue
		}
		if filterType != "" && !analysis.ContainsDriftType(as.DriftTypes, filterType) {
			continue
		}
		drifted = append(drifted, as)
	}
	respondJSON(w, http.StatusOK, map[string]any{
		"project": report.Project,
		"appsets": drifted,
	})
}

// ---- helpers ----

// appSetRequest extracts the project from the URL param and the ArgoCD
// credentials from query params or Authorization header.
func (h *Handler) appSetRequest(w http.ResponseWriter, r *http.Request) (analyzeRequest, bool) {
	project := chi.URLParam(r, "project")
	if project == "" {
		respondError(w, http.StatusBadRequest, "INVALID_REQUEST", "project is required")
		return analyzeRequest{}, false
	}

	// Credentials: prefer query params, fall back to headers.
	argoURL := r.URL.Query().Get("argocd_url")
	token := r.URL.Query().Get("token")

	if argoURL == "" {
		argoURL = r.Header.Get("X-ArgoCD-URL")
	}
	if token == "" {
		if auth := r.Header.Get("Authorization"); len(auth) > 7 {
			token = auth[7:] // strip "Bearer "
		}
	}

	if argoURL == "" || token == "" {
		respondError(w, http.StatusBadRequest, "INVALID_REQUEST",
			"provide argocd_url and token as query params or X-ArgoCD-URL / Authorization headers")
		return analyzeRequest{}, false
	}
	return analyzeRequest{ArgocdURL: argoURL, Token: token, Project: project}, true
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

func uniqueRegions(apps []domain.AppInstance) []string {
	seen := make(map[string]struct{}, len(apps))
	var out []string
	for _, a := range apps {
		if _, ok := seen[a.Region]; !ok {
			seen[a.Region] = struct{}{}
			out = append(out, a.Region)
		}
	}
	return out
}

