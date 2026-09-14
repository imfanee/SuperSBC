package admin

import (
	"net/http"
	"strings"
	"time"

	"github.com/pquerna/otp/totp"

	"github.com/opensbc/opensbc/internal/auth"
)

const (
	accessCookie  = "sbc_access"
	refreshCookie = "sbc_refresh"
	csrfCookie    = "sbc_csrf"
	maxFailures   = 10  // per email in the window
	maxFailuresIP = 100 // per source address in the window
	failureWindow = 15 * time.Minute
)

type loginRequest struct {
	Email    string `json:"email" validate:"required,email"`
	Password string `json:"password" validate:"required"`
	TOTPCode string `json:"totp_code"`
}

type meResponse struct {
	ID          string `json:"id"`
	Email       string `json:"email"`
	Role        string `json:"role"`
	TOTPEnabled bool   `json:"totp_enabled"`
	CSRFToken   string `json:"csrf_token"`
	ViaAPIKey   bool   `json:"via_api_key"`
}

func (h *Handler) setCookie(w http.ResponseWriter, name, value, path string, expires time.Time, httpOnly bool) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: path, Expires: expires, HttpOnly: httpOnly, Secure: h.Cfg.Auth.CookieSecure, SameSite: http.SameSiteStrictMode})
}

func (h *Handler) clearCookies(w http.ResponseWriter) {
	past := time.Unix(0, 0)
	h.setCookie(w, accessCookie, "", "/", past, true)
	h.setCookie(w, refreshCookie, "", "/api/v1/auth", past, true)
	h.setCookie(w, csrfCookie, "", "/", past, false)
}

// issueSession sets the three cookies for a user and returns the csrf token.
func (h *Handler) issueSession(w http.ResponseWriter, r *http.Request, userID, email, role string) (string, error) {
	uid, _ := parseUUID(userID)
	access, exp, err := h.signer.Issue(uid, email, role)
	if err != nil {
		return "", err
	}
	refresh, err := auth.RandomToken(32)
	if err != nil {
		return "", err
	}
	refreshExp := time.Now().Add(h.Cfg.Auth.RefreshTokenTTL)
	if _, err := h.Store.CreateSession(r.Context(), uid, auth.HashToken(refresh), refreshExp, remoteIP(r), r.UserAgent()); err != nil {
		return "", err
	}
	csrf, err := auth.RandomToken(24)
	if err != nil {
		return "", err
	}
	h.setCookie(w, accessCookie, access, "/", exp, true)
	h.setCookie(w, refreshCookie, refresh, "/api/v1/auth", refreshExp, true)
	h.setCookie(w, csrfCookie, csrf, "/", refreshExp, false)
	return csrf, nil
}

// login godoc
// @Summary Log in with email and password (and TOTP code when enabled)
// @Tags auth
// @Accept json
// @Produce json
// @Param body body loginRequest true "credentials"
// @Success 200 {object} meResponse
// @Failure 401 {object} errorBody
// @Failure 429 {object} errorBody
// @Router /auth/login [post]
func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if !h.decode(w, r, &req) {
		return
	}
	ip := remoteIP(r)
	if byEmail, byIP, _ := h.Store.RecentFailedLogins(r.Context(), req.Email, ip, failureWindow); byEmail >= maxFailures || byIP >= maxFailuresIP {
		fail(w, http.StatusTooManyRequests, "too many failed logins, try again later")
		return
	}
	u, err := h.Store.UserByEmail(r.Context(), req.Email)
	if err != nil || u.Status != "active" || !auth.VerifyPassword(u.PasswordHash, req.Password) {
		h.Store.RecordLoginAttempt(r.Context(), req.Email, ip, false)
		fail(w, http.StatusUnauthorized, "invalid credentials")
		return
	}
	if u.TOTPEnabled {
		if req.TOTPCode == "" {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "totp required", "totp_required": true})
			return
		}
		if u.TOTPSecret == nil || !totp.Validate(strings.TrimSpace(req.TOTPCode), *u.TOTPSecret) {
			h.Store.RecordLoginAttempt(r.Context(), req.Email, ip, false)
			fail(w, http.StatusUnauthorized, "invalid totp code")
			return
		}
	}
	csrf, err := h.issueSession(w, r, u.ID.String(), u.Email, u.Role)
	if err != nil {
		failErr(w, err)
		return
	}
	h.Store.RecordLoginAttempt(r.Context(), req.Email, ip, true)
	h.Store.TouchLogin(r.Context(), u.ID)
	h.Store.Audit(r.Context(), &u.ID, u.Email, "login", "user", u.ID.String(), nil, nil, ip)
	writeJSON(w, http.StatusOK, meResponse{ID: u.ID.String(), Email: u.Email, Role: u.Role, TOTPEnabled: u.TOTPEnabled, CSRFToken: csrf})
}

// refresh godoc
// @Summary Rotate the refresh token and issue a new access token
// @Tags auth
// @Produce json
// @Success 200 {object} meResponse
// @Failure 401 {object} errorBody
// @Router /auth/refresh [post]
func (h *Handler) refresh(w http.ResponseWriter, r *http.Request) {
	c, err := r.Cookie(refreshCookie)
	if err != nil {
		fail(w, http.StatusUnauthorized, "no refresh token")
		return
	}
	sess, err := h.Store.SessionByToken(r.Context(), auth.HashToken(c.Value))
	if err != nil {
		h.clearCookies(w)
		fail(w, http.StatusUnauthorized, "refresh token invalid")
		return
	}
	u, err := h.Store.UserByID(r.Context(), sess.UserID)
	if err != nil || u.Status != "active" {
		h.clearCookies(w)
		fail(w, http.StatusUnauthorized, "user disabled")
		return
	}
	h.Store.RevokeSession(r.Context(), auth.HashToken(c.Value))
	csrf, err := h.issueSession(w, r, u.ID.String(), u.Email, u.Role)
	if err != nil {
		failErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, meResponse{ID: u.ID.String(), Email: u.Email, Role: u.Role, TOTPEnabled: u.TOTPEnabled, CSRFToken: csrf})
}

// logout godoc
// @Summary Log out (revokes the refresh session and clears cookies)
// @Tags auth
// @Success 204
// @Router /auth/logout [post]
func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(refreshCookie); err == nil {
		h.Store.RevokeSession(r.Context(), auth.HashToken(c.Value))
	}
	h.clearCookies(w)
	w.WriteHeader(http.StatusNoContent)
}

// me godoc
// @Summary Current principal
// @Tags auth
// @Produce json
// @Success 200 {object} meResponse
// @Router /auth/me [get]
func (h *Handler) me(w http.ResponseWriter, r *http.Request) {
	p := principal(r.Context())
	u, err := h.Store.UserByID(r.Context(), p.UserID)
	if err != nil {
		fail(w, http.StatusUnauthorized, "user not found")
		return
	}
	csrf := ""
	if c, err := r.Cookie(csrfCookie); err == nil {
		csrf = c.Value
	}
	writeJSON(w, http.StatusOK, meResponse{ID: u.ID.String(), Email: u.Email, Role: u.Role, TOTPEnabled: u.TOTPEnabled, CSRFToken: csrf, ViaAPIKey: p.ViaAPIKey})
}

type changePasswordRequest struct {
	CurrentPassword string `json:"current_password" validate:"required"`
	NewPassword     string `json:"new_password" validate:"required,min=10"`
}

// changePassword godoc
// @Summary Change own password (revokes other sessions)
// @Tags auth
// @Accept json
// @Param body body changePasswordRequest true "passwords"
// @Success 204
// @Router /auth/password [post]
func (h *Handler) changePassword(w http.ResponseWriter, r *http.Request) {
	var req changePasswordRequest
	if !h.decode(w, r, &req) {
		return
	}
	u, ok := h.ensureUser(w, r)
	if !ok {
		return
	}
	if !auth.VerifyPassword(u.PasswordHash, req.CurrentPassword) {
		fail(w, http.StatusForbidden, "current password wrong")
		return
	}
	hash, err := auth.HashPassword(req.NewPassword)
	if err != nil {
		failErr(w, err)
		return
	}
	if err := h.Store.SetPassword(r.Context(), u.ID, hash); err != nil {
		failErr(w, err)
		return
	}
	h.audit(r, "password.change", "user", u.ID.String(), nil, nil)
	h.clearCookies(w)
	w.WriteHeader(http.StatusNoContent)
}

// totpSetup godoc
// @Summary Generate a TOTP secret (not enabled until confirmed)
// @Tags auth
// @Produce json
// @Success 200 {object} map[string]string
// @Router /auth/totp/setup [post]
func (h *Handler) totpSetup(w http.ResponseWriter, r *http.Request) {
	u, ok := h.ensureUser(w, r)
	if !ok {
		return
	}
	key, err := totp.Generate(totp.GenerateOpts{Issuer: "OpenSBC", AccountName: u.Email})
	if err != nil {
		failErr(w, err)
		return
	}
	secret := key.Secret()
	if err := h.Store.SetTOTP(r.Context(), u.ID, &secret, false); err != nil {
		failErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"secret": secret, "otpauth_url": key.URL()})
}

type totpCodeRequest struct {
	Code string `json:"code" validate:"required"`
}

// totpConfirm godoc
// @Summary Confirm a TOTP code and enable 2FA
// @Tags auth
// @Accept json
// @Param body body totpCodeRequest true "code"
// @Success 204
// @Router /auth/totp/confirm [post]
func (h *Handler) totpConfirm(w http.ResponseWriter, r *http.Request) {
	var req totpCodeRequest
	if !h.decode(w, r, &req) {
		return
	}
	u, ok := h.ensureUser(w, r)
	if !ok {
		return
	}
	if u.TOTPSecret == nil || !totp.Validate(strings.TrimSpace(req.Code), *u.TOTPSecret) {
		fail(w, http.StatusBadRequest, "invalid code")
		return
	}
	if err := h.Store.SetTOTP(r.Context(), u.ID, u.TOTPSecret, true); err != nil {
		failErr(w, err)
		return
	}
	h.audit(r, "totp.enable", "user", u.ID.String(), nil, nil)
	w.WriteHeader(http.StatusNoContent)
}

// totpDisable godoc
// @Summary Disable 2FA (requires a valid code)
// @Tags auth
// @Accept json
// @Param body body totpCodeRequest true "code"
// @Success 204
// @Router /auth/totp/disable [post]
func (h *Handler) totpDisable(w http.ResponseWriter, r *http.Request) {
	var req totpCodeRequest
	if !h.decode(w, r, &req) {
		return
	}
	u, ok := h.ensureUser(w, r)
	if !ok {
		return
	}
	if !u.TOTPEnabled || u.TOTPSecret == nil || !totp.Validate(strings.TrimSpace(req.Code), *u.TOTPSecret) {
		fail(w, http.StatusBadRequest, "invalid code")
		return
	}
	if err := h.Store.SetTOTP(r.Context(), u.ID, nil, false); err != nil {
		failErr(w, err)
		return
	}
	h.audit(r, "totp.disable", "user", u.ID.String(), nil, nil)
	w.WriteHeader(http.StatusNoContent)
}

type createAPIKeyRequest struct {
	Name string `json:"name" validate:"required,max=100"`
}

// listAPIKeys godoc
// @Summary List API keys (own keys; admins see all)
// @Tags auth
// @Produce json
// @Success 200 {array} model.APIKey
// @Router /auth/api-keys [get]
func (h *Handler) listAPIKeys(w http.ResponseWriter, r *http.Request) {
	p := principal(r.Context())
	var uid *uuidT
	if p.Role != "admin" {
		u := p.UserID
		uid = &u
	}
	keys, err := h.Store.APIKeys(r.Context(), uid)
	if err != nil {
		failErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, keys)
}

// createAPIKey godoc
// @Summary Create an API key (the plaintext is returned once)
// @Tags auth
// @Accept json
// @Produce json
// @Param body body createAPIKeyRequest true "name"
// @Success 201 {object} map[string]any
// @Router /auth/api-keys [post]
func (h *Handler) createAPIKey(w http.ResponseWriter, r *http.Request) {
	var req createAPIKeyRequest
	if !h.decode(w, r, &req) {
		return
	}
	p := principal(r.Context())
	key, prefix, hash, err := auth.NewAPIKey()
	if err != nil {
		failErr(w, err)
		return
	}
	k, err := h.Store.CreateAPIKey(r.Context(), p.UserID, req.Name, prefix, hash)
	if err != nil {
		failErr(w, err)
		return
	}
	h.audit(r, "apikey.create", "api_key", k.ID.String(), nil, map[string]any{"name": req.Name, "prefix": prefix})
	writeJSON(w, http.StatusCreated, map[string]any{"api_key": k, "key": key})
}

// revokeAPIKey godoc
// @Summary Revoke an API key
// @Tags auth
// @Param id path string true "key id"
// @Success 204
// @Router /auth/api-keys/{id} [delete]
func (h *Handler) revokeAPIKey(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	p := principal(r.Context())
	var uid *uuidT
	if p.Role != "admin" {
		u := p.UserID
		uid = &u
	}
	if err := h.Store.RevokeAPIKey(r.Context(), id, uid); err != nil {
		failErr(w, err)
		return
	}
	h.audit(r, "apikey.revoke", "api_key", id.String(), nil, nil)
	w.WriteHeader(http.StatusNoContent)
}
