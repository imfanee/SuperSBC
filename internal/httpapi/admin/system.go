package admin

import (
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/opensbc/opensbc/internal/auth"
	"github.com/opensbc/opensbc/internal/failover"
)

func (h *Handler) mountSystem(r chi.Router) {
	r.Get("/system/status", h.systemStatus)
	r.Get("/system/failover-rules", h.failoverRules)
	r.Get("/system/settings", h.getSettings)
	r.With(admins).Put("/system/settings", h.putSettings)
	r.Get("/system/gateways", h.gatewayStates)
	r.With(admins).Post("/system/profiles/{name}/restart", h.restartProfile)
	r.Get("/system/notifications", h.notifications)
	r.Get("/audit-log", h.auditLog)
}

func (h *Handler) mountUsers(r chi.Router) {
	r.With(admins).Get("/users", h.listUsers)
	r.With(admins).Post("/users", h.createUser)
	r.With(admins).Put("/users/{id}", h.updateUser)
	r.With(admins).Delete("/users/{id}", h.deleteUser)
	r.With(admins).Post("/users/{id}/password", h.resetUserPassword)
	r.With(admins).Post("/users/{id}/reset-token", h.createResetToken)
}

// createResetToken godoc
// @Summary Generate a one-time password reset link for a user (valid one hour)
// @Tags users
// @Produce json
// @Param id path string true "user id"
// @Success 201 {object} map[string]string
// @Router /users/{id}/reset-token [post]
func (h *Handler) createResetToken(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	if _, err := h.Store.UserByID(r.Context(), id); err != nil {
		failErr(w, err)
		return
	}
	token, err := auth.RandomToken(32)
	if err != nil {
		failErr(w, err)
		return
	}
	if err := h.Store.CreatePasswordReset(r.Context(), id, auth.HashToken(token), time.Now().Add(time.Hour), principal(r.Context()).Email); err != nil {
		failErr(w, err)
		return
	}
	h.audit(r, "user.reset_token", "user", id.String(), nil, nil)
	writeJSON(w, http.StatusCreated, map[string]string{"token": token, "path": "/reset-password?token=" + token, "expires_in": "1h"})
}

type resetInput struct {
	Token    string `json:"token" validate:"required"`
	Password string `json:"password" validate:"required,min=10"`
}

// resetPassword godoc
// @Summary Set a new password with a one-time reset token (unauthenticated)
// @Tags auth
// @Accept json
// @Param body body resetInput true "token and password"
// @Success 204
// @Router /auth/reset [post]
func (h *Handler) resetPassword(w http.ResponseWriter, r *http.Request) {
	var in resetInput
	if !h.decode(w, r, &in) {
		return
	}
	uid, err := h.Store.ConsumePasswordReset(r.Context(), auth.HashToken(in.Token))
	if err != nil {
		fail(w, http.StatusBadRequest, "reset token invalid or expired")
		return
	}
	hash, err := auth.HashPassword(in.Password)
	if err != nil {
		failErr(w, err)
		return
	}
	if err := h.Store.SetPassword(r.Context(), uid, hash); err != nil {
		failErr(w, err)
		return
	}
	h.Store.Audit(r.Context(), &uid, "", "password.reset", "user", uid.String(), nil, nil, remoteIP(r))
	w.WriteHeader(http.StatusNoContent)
}

// version godoc
// @Summary Build version (unauthenticated)
// @Tags system
// @Produce json
// @Success 200 {object} map[string]string
// @Router /system/version [get]
func (h *Handler) version(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"version": h.Version, "node": h.Cfg.NodeName})
}

// systemStatus godoc
// @Summary Readiness of every dependency plus configuration summary
// @Tags system
// @Produce json
// @Success 200 {object} map[string]any
// @Router /system/status [get]
func (h *Handler) systemStatus(w http.ResponseWriter, r *http.Request) {
	var ready any
	if h.Ready != nil {
		ready = h.Ready(r.Context())
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ready":   ready,
		"version": h.Version,
		"node":    h.Cfg.NodeName,
		"config": map[string]any{
			"reserve_minutes": h.Cfg.Billing.ReserveMinutes, "max_call_duration": h.Cfg.Billing.MaxCallDuration.String(), "orphan_timeout": h.Cfg.Billing.OrphanTimeout.String(),
			"low_balance_threshold": h.Cfg.Billing.LowBalanceThreshold, "currency": h.Cfg.Billing.Currency,
			"originate_timeout": h.Cfg.Routing.OriginateTimeout, "progress_timeout": h.Cfg.Routing.ProgressTimeout, "block_negative_margin": h.Cfg.Routing.BlockNegativeMargin,
			"global_max_cps": h.Cfg.Routing.GlobalMaxCPS, "global_max_channels": h.Cfg.Routing.GlobalMaxChannels, "media_timeout_sec": h.Cfg.Routing.MediaTimeoutSec,
			"acl_mode": h.Cfg.ACLMode, "breaker_consecutive_faults": h.Cfg.Failover.BreakerConsecutiveFaults, "breaker_asr_threshold_percent": h.Cfg.Failover.BreakerASRThresholdPercent,
			"breaker_degraded_seconds": h.Cfg.Failover.BreakerDegradedSeconds, "gateway_ping_interval_seconds": h.Cfg.Failover.GatewayPingIntervalSeconds,
			"ban_threshold": h.Cfg.Ban.Threshold, "ban_window": h.Cfg.Ban.Window.String(), "ban_duration": h.Cfg.Ban.Duration.String(),
		},
	})
}

// failoverRules godoc
// @Summary The failure classification table (read only)
// @Tags system
// @Produce json
// @Success 200 {object} map[string]any
// @Router /system/failover-rules [get]
func (h *Handler) failoverRules(w http.ResponseWriter, _ *http.Request) {
	rules := h.Pipe.Rules()
	writeJSON(w, http.StatusOK, map[string]any{
		"number_fault":                   map[string]any{"sip_codes": rules.NumberFault.SIPCodes, "causes": rules.NumberFault.Causes},
		"carrier_fault":                  map[string]any{"sip_codes": rules.CarrierFault.SIPCodes, "causes": rules.CarrierFault.Causes},
		"min_ring_seconds_for_no_answer": rules.MinRingSecondsForNoAnswer,
		"default_5xx":                    rules.Default5xx,
		"default_other":                  rules.DefaultOther,
		"source":                         h.Cfg.Failover.RulesFile,
		"cause_to_sip":                   causeTable(),
	})
}

func causeTable() []map[string]any {
	causes := []string{"UNALLOCATED_NUMBER", "USER_BUSY", "NO_ANSWER", "NO_USER_RESPONSE", "CALL_REJECTED", "NORMAL_TEMPORARY_FAILURE", "NORMAL_CIRCUIT_CONGESTION",
		"NETWORK_OUT_OF_ORDER", "RECOVERY_ON_TIMER_EXPIRE", "PROGRESS_TIMEOUT", "GATEWAY_DOWN", "INCOMPATIBLE_DESTINATION", "BEARERCAPABILITY_NOTAUTH", "MANDATORY_IE_MISSING", "ORIGINATOR_CANCEL", "ALLOTTED_TIMEOUT"}
	out := make([]map[string]any, 0, len(causes))
	for _, c := range causes {
		code, phrase := failover.SIPCodeForCause(c)
		out = append(out, map[string]any{"cause": c, "sip_code": code, "reason": phrase})
	}
	return out
}

// getSettings godoc
// @Summary System settings rows (free-form key/value, e.g. UI preferences, sip trace state)
// @Tags system
// @Produce json
// @Success 200 {array} model.Setting
// @Router /system/settings [get]
func (h *Handler) getSettings(w http.ResponseWriter, r *http.Request) {
	out, err := h.Store.Settings(r.Context())
	if err != nil {
		failErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

type settingsInput struct {
	Values map[string]any `json:"values" validate:"required"`
}

// putSettings godoc
// @Summary Upsert settings rows
// @Tags system
// @Accept json
// @Produce json
// @Param body body settingsInput true "values"
// @Success 200 {array} model.Setting
// @Router /system/settings [put]
func (h *Handler) putSettings(w http.ResponseWriter, r *http.Request) {
	var in settingsInput
	if !h.decode(w, r, &in) {
		return
	}
	by := principal(r.Context()).Email
	for k, v := range in.Values {
		if strings.TrimSpace(k) == "" {
			continue
		}
		if err := h.Store.SetSetting(r.Context(), k, v, by); err != nil {
			failErr(w, err)
			return
		}
	}
	h.audit(r, "settings.update", "settings", "", nil, in.Values)
	h.getSettings(w, r)
}

// gatewayStates godoc
// @Summary Sofia gateway states from the OPTIONS ping poller
// @Tags system
// @Produce json
// @Success 200 {object} map[string]any
// @Router /system/gateways [get]
func (h *Handler) gatewayStates(w http.ResponseWriter, _ *http.Request) {
	if h.Gateways == nil {
		writeJSON(w, http.StatusOK, map[string]any{})
		return
	}
	writeJSON(w, http.StatusOK, h.Gateways.States())
}

// notifications godoc
// @Summary Recent notifications (low balance etc.)
// @Tags system
// @Produce json
// @Success 200 {array} model.Notification
// @Router /system/notifications [get]
func (h *Handler) notifications(w http.ResponseWriter, r *http.Request) {
	out, err := h.Store.Notifications(r.Context(), 200)
	if err != nil {
		failErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// auditLog godoc
// @Summary Audit log
// @Tags system
// @Produce json
// @Param entity_type query string false "entity type"
// @Param entity_id query string false "entity id"
// @Param actor query string false "actor email substring"
// @Success 200 {object} map[string]any
// @Router /audit-log [get]
func (h *Handler) auditLog(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	out, err := h.Store.AuditLog(r.Context(), q.Get("entity_type"), q.Get("entity_id"), q.Get("actor"), pageParams(r))
	if err != nil {
		failErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// ---- users ----

type userInput struct {
	Email    string `json:"email" validate:"required,email"`
	Password string `json:"password" validate:"omitempty,min=10"`
	Role     string `json:"role" validate:"required,oneof=admin operator viewer"`
	Status   string `json:"status" validate:"omitempty,oneof=active disabled"`
}

// listUsers godoc
// @Summary List users
// @Tags users
// @Produce json
// @Success 200 {array} model.User
// @Router /users [get]
func (h *Handler) listUsers(w http.ResponseWriter, r *http.Request) {
	out, err := h.Store.Users(r.Context())
	if err != nil {
		failErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// createUser godoc
// @Summary Create a user
// @Tags users
// @Accept json
// @Produce json
// @Param body body userInput true "user"
// @Success 201 {object} model.User
// @Router /users [post]
func (h *Handler) createUser(w http.ResponseWriter, r *http.Request) {
	var in userInput
	if !h.decode(w, r, &in) {
		return
	}
	if in.Password == "" {
		fail(w, http.StatusBadRequest, "password required")
		return
	}
	hash, err := auth.HashPassword(in.Password)
	if err != nil {
		failErr(w, err)
		return
	}
	out, err := h.Store.CreateUser(r.Context(), in.Email, hash, in.Role)
	if err != nil {
		failErr(w, err)
		return
	}
	h.audit(r, "user.create", "user", out.ID.String(), nil, map[string]string{"email": out.Email, "role": out.Role})
	writeJSON(w, http.StatusCreated, out)
}

// updateUser godoc
// @Summary Update role and status of a user
// @Tags users
// @Accept json
// @Produce json
// @Param id path string true "user id"
// @Param body body userInput true "user"
// @Success 200 {object} model.User
// @Router /users/{id} [put]
func (h *Handler) updateUser(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	var in userInput
	if !h.decode(w, r, &in) {
		return
	}
	if in.Status == "" {
		in.Status = "active"
	}
	p := principal(r.Context())
	if p.UserID == id && (in.Role != "admin" || in.Status != "active") {
		fail(w, http.StatusBadRequest, "cannot demote or disable yourself")
		return
	}
	before, err := h.Store.UserByID(r.Context(), id)
	if err != nil {
		failErr(w, err)
		return
	}
	out, err := h.Store.UpdateUser(r.Context(), id, in.Role, in.Status)
	if err != nil {
		failErr(w, err)
		return
	}
	h.audit(r, "user.update", "user", id.String(), map[string]string{"role": before.Role, "status": before.Status}, map[string]string{"role": out.Role, "status": out.Status})
	writeJSON(w, http.StatusOK, out)
}

// deleteUser godoc
// @Summary Delete a user
// @Tags users
// @Param id path string true "user id"
// @Success 204
// @Router /users/{id} [delete]
func (h *Handler) deleteUser(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	if principal(r.Context()).UserID == id {
		fail(w, http.StatusBadRequest, "cannot delete yourself")
		return
	}
	if err := h.Store.DeleteUser(r.Context(), id); err != nil {
		failErr(w, err)
		return
	}
	h.audit(r, "user.delete", "user", id.String(), nil, nil)
	w.WriteHeader(http.StatusNoContent)
}

type passwordInput struct {
	Password string `json:"password" validate:"required,min=10"`
}

// resetUserPassword godoc
// @Summary Set a user's password (admin), revoking their sessions
// @Tags users
// @Accept json
// @Param id path string true "user id"
// @Param body body passwordInput true "password"
// @Success 204
// @Router /users/{id}/password [post]
func (h *Handler) resetUserPassword(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	var in passwordInput
	if !h.decode(w, r, &in) {
		return
	}
	hash, err := auth.HashPassword(in.Password)
	if err != nil {
		failErr(w, err)
		return
	}
	if err := h.Store.SetPassword(r.Context(), id, hash); err != nil {
		failErr(w, err)
		return
	}
	h.audit(r, "user.password_reset", "user", id.String(), nil, nil)
	w.WriteHeader(http.StatusNoContent)
}

// ---- docs ----

// docsPage serves a Swagger UI page for the generated OpenAPI document.
func (h *Handler) docsPage(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(`<!doctype html><html><head><meta charset="utf-8"><title>OpenSBC API</title>
<link rel="stylesheet" href="https://cdnjs.cloudflare.com/ajax/libs/swagger-ui/5.17.14/swagger-ui.min.css"></head>
<body><div id="ui"></div>
<script src="https://cdnjs.cloudflare.com/ajax/libs/swagger-ui/5.17.14/swagger-ui-bundle.min.js"></script>
<script>window.onload=function(){SwaggerUIBundle({url:'/api/docs/openapi.json',dom_id:'#ui',withCredentials:true})}</script></body></html>`))
}

// openapiJSON serves the generated OpenAPI 3.1 document.
func (h *Handler) openapiJSON(w http.ResponseWriter, _ *http.Request) {
	b, err := os.ReadFile(h.Cfg.OpenAPIFile)
	if err != nil {
		b = openapiEmbedded
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(b)
}

// restartProfile godoc
// @Summary Restart a Sofia profile so it re-reads its configuration (drops the calls on that profile; use at a quiet time)
// @Tags system
// @Produce json
// @Param name path string true "external-ingress|external-egress"
// @Success 200 {object} map[string]string
// @Router /system/profiles/{name}/restart [post]
func (h *Handler) restartProfile(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	if name != "external-ingress" && name != "external-egress" {
		fail(w, http.StatusBadRequest, "unknown profile")
		return
	}
	if h.ESL == nil || !h.ESL.Connected() {
		fail(w, http.StatusServiceUnavailable, "FreeSWITCH not connected")
		return
	}
	out, err := h.ESL.API(r.Context(), "sofia profile "+name+" restart reloadxml")
	if err != nil {
		failErr(w, err)
		return
	}
	h.audit(r, "profile.restart", "profile", name, nil, map[string]string{"result": strings.TrimSpace(out)})
	writeJSON(w, http.StatusOK, map[string]string{"profile": name, "result": strings.TrimSpace(out)})
}
