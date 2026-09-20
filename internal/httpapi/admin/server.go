// Package admin is the Admin REST API of Section 8, consumed by the web UI
// and by API key clients. Every state change is written to audit_log.
package admin

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/imfanee/supersbc/internal/auth"
	"github.com/imfanee/supersbc/internal/billing"
	"github.com/imfanee/supersbc/internal/callcontrol"
	"github.com/imfanee/supersbc/internal/config"
	"github.com/imfanee/supersbc/internal/esl"
	"github.com/imfanee/supersbc/internal/fsconfig"
	"github.com/imfanee/supersbc/internal/gateways"
	"github.com/imfanee/supersbc/internal/invoice"
	"github.com/imfanee/supersbc/internal/mail"
	"github.com/imfanee/supersbc/internal/model"
	"github.com/imfanee/supersbc/internal/quality"
	"github.com/imfanee/supersbc/internal/sipcapture"
	"github.com/imfanee/supersbc/internal/store"
	"github.com/imfanee/supersbc/internal/tables"
	"github.com/imfanee/supersbc/internal/trace"
)

// Deps are the collaborators of the admin API.
type Deps struct {
	Cfg      *config.Config
	Log      *slog.Logger
	Store    *store.Store
	Redis    *redis.Client
	Pipe     *callcontrol.Pipeline
	Bill     *billing.Engine
	Tables   *tables.Tables
	ESL      *esl.Supervisor
	Gateways *gateways.Poller
	Renderer *fsconfig.Renderer
	Version  string
	Ready    func(context.Context) any
	Reports  *ReportsHandler
	Trace    *trace.Store
	Invoices *invoice.Service
	Mailer   *mail.Mailer
	Capture  *sipcapture.Store
	Quality  *quality.Tracker
}

// Handler is the admin API.
type Handler struct {
	Deps
	signer   *auth.Signer
	validate *validator.Validate
}

// New creates the handler.
func New(d Deps) *Handler {
	return &Handler{Deps: d, signer: auth.NewSigner(d.Cfg.Auth.JWTSecret, d.Cfg.Auth.AccessTokenTTL), validate: validator.New()}
}

// ---- principal ----

type ctxKey int

const principalKey ctxKey = 1

// Principal is the authenticated caller.
type Principal struct {
	UserID    uuid.UUID
	Email     string
	Role      string
	ViaAPIKey bool
}

func principal(ctx context.Context) *Principal {
	p, _ := ctx.Value(principalKey).(*Principal)
	return p
}

// Mount registers everything under /api/v1 plus /api/docs.
func (h *Handler) Mount(r chi.Router) {
	r.Route("/api/v1", func(r chi.Router) {
		r.Use(h.securityHeaders)
		r.Post("/auth/login", h.login)
		r.Post("/auth/refresh", h.refresh)
		r.Post("/auth/logout", h.logout)
		r.Post("/auth/reset", h.resetPassword)
		r.Post("/auth/forgot", h.forgotPassword)
		r.Get("/auth/options", h.authOptions)
		r.Get("/system/version", h.version)

		r.Group(func(r chi.Router) {
			r.Use(h.authenticate, h.csrf)
			r.Get("/auth/me", h.me)
			r.Post("/auth/password", h.changePassword)
			r.Post("/auth/totp/setup", h.totpSetup)
			r.Post("/auth/totp/confirm", h.totpConfirm)
			r.Post("/auth/totp/disable", h.totpDisable)
			r.Get("/auth/api-keys", h.listAPIKeys)
			r.Post("/auth/api-keys", h.createAPIKey)
			r.Delete("/auth/api-keys/{id}", h.revokeAPIKey)

			h.mountCustomers(r)
			h.mountCarriers(r)
			h.mountRates(r)
			h.mountRoutes(r)
			h.mountCDRs(r)
			h.mountSystem(r)
			h.mountUsers(r)
			h.mountTrace(r)
			h.mountBans(r)
			h.mountInvoices(r)
			if h.Reports != nil {
				h.Reports.h = h
				h.Reports.Mount(r)
			}
		})
	})
	r.Get("/api/docs", h.docsPage)
	r.Get("/api/docs/openapi.json", h.openapiJSON)
}

// ---- middleware ----

func (h *Handler) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

// authenticate accepts the access cookie or an API key bearer token.
func (h *Handler) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p *Principal
		if ah := r.Header.Get("Authorization"); strings.HasPrefix(ah, "Bearer sbc_") {
			key := strings.TrimPrefix(ah, "Bearer ")
			u, err := h.Store.UserByAPIKeyHash(r.Context(), auth.HashToken(key))
			if err != nil {
				fail(w, http.StatusUnauthorized, "invalid api key")
				return
			}
			p = &Principal{UserID: u.ID, Email: u.Email, Role: u.Role, ViaAPIKey: true}
		} else if c, err := r.Cookie(accessCookie); err == nil {
			claims, err := h.signer.Verify(c.Value)
			if err != nil {
				fail(w, http.StatusUnauthorized, "session expired")
				return
			}
			p = &Principal{UserID: claims.UserID, Email: claims.Email, Role: claims.Role}
		} else {
			fail(w, http.StatusUnauthorized, "authentication required")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), principalKey, p)))
	})
}

// csrf enforces the double-submit token for cookie sessions on state changes.
func (h *Handler) csrf(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := principal(r.Context())
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions && p != nil && !p.ViaAPIKey {
			c, err := r.Cookie(csrfCookie)
			hdr := r.Header.Get("X-CSRF-Token")
			if err != nil || hdr == "" || subtle.ConstantTimeCompare([]byte(c.Value), []byte(hdr)) != 1 {
				fail(w, http.StatusForbidden, "csrf token missing or invalid")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// requireRole allows the listed roles (admin always allowed).
func requireRole(roles ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p := principal(r.Context())
			if p == nil {
				fail(w, http.StatusUnauthorized, "authentication required")
				return
			}
			if p.Role != "admin" {
				ok := false
				for _, role := range roles {
					if role == p.Role {
						ok = true
					}
				}
				if !ok {
					fail(w, http.StatusForbidden, "insufficient role")
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// writers: viewer is read-only; operator may change routing and configuration
// but not money; admin may do everything.
var (
	operators = requireRole("operator")
	admins    = requireRole()
)

// ---- helpers ----

type errorBody struct {
	Error   string            `json:"error"`
	Details map[string]string `json:"details,omitempty"`
}

func fail(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, errorBody{Error: msg})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// failErr maps store errors to HTTP codes.
func failErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		fail(w, http.StatusNotFound, "not found")
	case errors.Is(err, store.ErrConflict):
		fail(w, http.StatusConflict, err.Error())
	case errors.Is(err, store.ErrInsufficientFunds):
		fail(w, http.StatusUnprocessableEntity, "insufficient funds")
	default:
		fail(w, http.StatusInternalServerError, err.Error())
	}
}

// decode parses and validates a JSON body.
func (h *Handler) decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<20)).Decode(v); err != nil {
		fail(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return false
	}
	if err := h.validate.Struct(v); err != nil {
		var ve validator.ValidationErrors
		details := map[string]string{}
		if errors.As(err, &ve) {
			for _, fe := range ve {
				details[strings.ToLower(fe.Field())] = fe.Tag()
			}
		}
		writeJSON(w, http.StatusBadRequest, errorBody{Error: "validation failed", Details: details})
		return false
	}
	return true
}

func pathUUID(w http.ResponseWriter, r *http.Request, name string) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, name))
	if err != nil {
		fail(w, http.StatusBadRequest, "invalid "+name)
		return uuid.Nil, false
	}
	return id, true
}

func pageParams(r *http.Request) store.Page {
	q := r.URL.Query()
	p := store.Page{Sort: q.Get("sort")}
	p.Page, _ = strconv.Atoi(q.Get("page"))
	p.PerPage, _ = strconv.Atoi(q.Get("per_page"))
	if strings.HasPrefix(p.Sort, "-") {
		p.Sort = p.Sort[1:]
		p.Desc = true
	}
	if q.Get("order") == "desc" {
		p.Desc = true
	}
	return p.Normalize()
}

func remoteIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		return strings.TrimSpace(strings.Split(xff, ",")[0])
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// audit records an admin action.
func (h *Handler) audit(r *http.Request, action, entity, id string, before, after any) {
	p := principal(r.Context())
	var uid *uuid.UUID
	email := ""
	if p != nil {
		u := p.UserID
		uid = &u
		email = p.Email
	}
	h.Store.Audit(context.WithoutCancel(r.Context()), uid, email, action, entity, id, before, after, remoteIP(r))
}

func (h *Handler) publish(ctx context.Context, channel, payload string) {
	if h.Redis != nil {
		_ = h.Redis.Publish(ctx, channel, payload).Err()
	}
}

func queryTime(r *http.Request, key string) *time.Time {
	v := r.URL.Query().Get(key)
	if v == "" {
		return nil
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02"} {
		if t, err := time.Parse(layout, v); err == nil {
			return &t
		}
	}
	return nil
}

func queryUUID(r *http.Request, key string) *uuid.UUID {
	if id, err := uuid.Parse(r.URL.Query().Get(key)); err == nil {
		return &id
	}
	return nil
}

func queryInt(r *http.Request, key string) *int {
	if n, err := strconv.Atoi(r.URL.Query().Get(key)); err == nil {
		return &n
	}
	return nil
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func uuidPtr(s string) (*uuid.UUID, error) {
	if s == "" {
		return nil, nil
	}
	id, err := uuid.Parse(s)
	if err != nil {
		return nil, fmt.Errorf("invalid uuid %q", s)
	}
	return &id, nil
}

// ensureUser returns the principal's user row.
func (h *Handler) ensureUser(w http.ResponseWriter, r *http.Request) (*model.User, bool) {
	p := principal(r.Context())
	u, err := h.Store.UserByID(r.Context(), p.UserID)
	if err != nil {
		fail(w, http.StatusUnauthorized, "user not found")
		return nil, false
	}
	return u, true
}

type uuidT = uuid.UUID

func parseUUID(s string) (uuid.UUID, error) { return uuid.Parse(s) }

var uuidNil = uuid.Nil
