# ArgoCD Drift Checker — Implementation Plan

## Problem Statement

DevOps teams managing multi-cluster Kubernetes deployments currently detect drift by
running ad-hoc Python scripts per cluster, extracting image SHAs and tags from raw
Kubernetes objects, then manually comparing across environments. This approach has no
structured correlation between apps, no standard grouping, and no historical baseline.

ArgoCD already models the desired state per application and per cluster. This tool
leverages that model to produce structured, diff-aware reports grouped by AppSet —
the natural unit of "same application deployed everywhere".

---

## Goals

| # | Goal |
|---|------|
| G1 | Single API call returns full drift picture for a project |
| G2 | Drift is expressed at AppSet level (not raw Deployment level) |
| G3 | Image comparison includes tag AND digest when available |
| G4 | Regions/clusters are first-class labels, not inferred strings |
| G5 | No write access to ArgoCD ever needed |

## Non-Goals

- Triggering syncs or rollbacks (out of scope, read-only)
- Storing historical drift state (v1 scope only)
- Supporting non-ArgoCD workloads

---

## Phases

### Phase 1 — Core Server & ArgoCD Client (Week 1)

- [ ] Initialize Go module (`drift-checker`)
- [ ] Implement ArgoCD REST client (token auth, TLS-optional skip)
  - `GET /api/v1/applicationsets` — list AppSets filtered by project
  - `GET /api/v1/applications` — list Apps filtered by project
- [ ] Parse ArgoCD response structs (ApplicationSet, Application)
- [ ] Wire up basic HTTP server with `/healthz` endpoint

**Deliverable:** server starts, can authenticate and list raw ArgoCD data.

---

### Phase 2 — Data Model & Grouping (Week 1–2)

- [ ] Define internal domain types: `AppSet`, `AppInstance`, `ImageRef`, `DriftReport`
- [ ] Group Apps under their parent AppSet using the label
  `argocd.argoproj.io/app-set-name`
- [ ] Resolve cluster destination → human-readable region label
  (use ArgoCD cluster `name` field; fall back to server URL hostname)
- [ ] Parse image strings into structured `ImageRef`
  (registry, repository, tag, digest)

**Deliverable:** structured in-memory model ready for analysis.

---

### Phase 3 — Drift Detection Engine (Week 2)

Drift categories detected per AppSet:

| Category | Trigger |
|----------|---------|
| `IMAGE_TAG_DRIFT` | Tag differs across regions for the same image repository |
| `IMAGE_DIGEST_DRIFT` | Digest differs even when tag matches (silent re-tag) |
| `SYNC_DRIFT` | One or more apps in OutOfSync state |
| `MISSING_REGION` | AppSet expects N regions but app absent in some |
| `HEALTH_DRIFT` | Health status not Healthy in at least one region |

- [ ] Implement drift comparator per AppSet
- [ ] Produce per-AppSet `DriftSummary` with affected regions and delta details

**Deliverable:** drift engine returns structured diff for any AppSet.

---

### Phase 4 — HTTP API (Week 2–3)

Endpoints:

```
POST /api/v1/analyze
  Body: { argocd_url, token, project }
  Returns: full AnalysisReport

GET  /api/v1/analyze/{project}/appsets
  Returns: list of AppSets with drift flag

GET  /api/v1/analyze/{project}/appsets/{appset}
  Returns: detailed drift report for one AppSet

GET  /api/v1/analyze/{project}/drift
  Returns: only drifted AppSets (filtered view)
```

Query parameters for filtering:
- `?region=prod-us` — scope to a specific region
- `?drift_type=IMAGE_TAG_DRIFT` — filter by drift category

- [ ] Implement handlers and request validation
- [ ] JSON error responses with structured `{ error, code }` body

**Deliverable:** fully usable REST API.

---

### Phase 5 — Configuration & Deployment (Week 3)

- [ ] Dockerfile (distroless base)
- [ ] `config.yaml` support for defaults (ArgoCD URL, TLS settings)
- [ ] Environment variable overrides (`ARGOCD_URL`, `ARGOCD_TOKEN`)
- [ ] Kubernetes manifests (Deployment + Service) for running inside cluster
- [ ] README with curl examples

**Deliverable:** deployable artifact, runnable in-cluster or locally.

---

## Suggested Additions (not in v1 scope, flagged for discussion)

1. **Caching layer** — ArgoCD API calls are expensive; add a short TTL cache
   (e.g., 30s) to avoid hammering the API on repeated queries.
2. **Webhook / push mode** — Instead of polling on each request, subscribe to
   ArgoCD SSE events and keep an in-memory state, making responses instant.
3. **Multi-project fan-out** — Accept a list of projects and return merged report.
4. **Slack / PagerDuty alerting** — Emit drift events to notification channels.
5. **Historical diff storage** — Persist snapshots to PostgreSQL or SQLite so
   teams can see "when did prod-ir fall behind prod-us?"

---

## Tech Stack

| Component | Choice | Reason |
|-----------|--------|--------|
| Language | Go 1.22+ | Strong concurrency, single binary, excellent HTTP client |
| HTTP framework | `net/http` + `chi` router | Lightweight, no magic |
| HTTP client | `net/http` with retries | Standard, controllable |
| JSON | `encoding/json` | Standard; consider `json-iterator` if perf matters |
| Config | `viper` | Env + file merging with zero boilerplate |
| Testing | `testing` + `testify` | Standard Go idioms |
| Container | Distroless Go | Minimal attack surface |
