package api

import (
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/pbuzdygan/filedeck/internal/identity"
	"rsc.io/qr"
)

// setupLifetime bounds how long a new secret waits for its first code.
const setupLifetime = 10 * time.Minute

type pendingTOTP struct {
	secret  []byte
	expires time.Time
}

// qrView is the QR code of the otpauth address as rows of "1" (dark) and "0"
// modules; the page draws it on a canvas, so nothing leaves the server.
type qrView struct {
	Size int      `json:"size"`
	Rows []string `json:"rows"`
}

func qrRows(text string) (qrView, error) {
	c, err := qr.Encode(text, qr.M)
	if err != nil {
		return qrView{}, err
	}
	v := qrView{Size: c.Size, Rows: make([]string, c.Size)}
	for y := range c.Size {
		var b strings.Builder
		for x := range c.Size {
			if c.Black(x, y) {
				b.WriteByte('1')
			} else {
				b.WriteByte('0')
			}
		}
		v.Rows[y] = b.String()
	}
	return v, nil
}

// totpRoutes let every account manage its own two-factor authentication, each
// change confirmed with the current password; administrators can only switch
// it off for another account (a lost phone).
func (a *API) totpRoutes() {
	a.mux.HandleFunc("GET /api/auth/totp", a.auth(func(w http.ResponseWriter, r *http.Request, l identity.Login) {
		on, left, err := a.store.TwoFactor(l.User.ID)
		if err != nil {
			problem(w, err)
			return
		}
		reply(w, 200, map[string]any{"enabled": on, "recovery_codes_left": left})
	}, false, false))
	// A new secret is kept in memory until the first code confirms it; the
	// current setup (if any) keeps working until then.
	a.mux.HandleFunc("POST /api/auth/totp/setup", a.auth(func(w http.ResponseWriter, r *http.Request, l identity.Login) {
		var in struct {
			Password string `json:"password"`
		}
		if !decode(w, r, &in) || !a.reauth(w, r, l, in.Password) {
			return
		}
		secret := identity.NewTOTPSecret()
		uri := identity.TOTPURI("Filedeck", l.User.Username+"@"+a.origin.Hostname(), secret)
		code, err := qrRows(uri)
		if err != nil {
			problem(w, err)
			return
		}
		a.pendingMu.Lock()
		now := time.Now()
		for id, p := range a.pending {
			if now.After(p.expires) {
				delete(a.pending, id)
			}
		}
		a.pending[l.User.ID] = pendingTOTP{secret, now.Add(setupLifetime)}
		a.pendingMu.Unlock()
		reply(w, 200, map[string]any{"key": identity.TOTPKey(secret), "uri": uri, "qr": code})
	}, false, false))
	a.mux.HandleFunc("POST /api/auth/totp/enable", a.auth(func(w http.ResponseWriter, r *http.Request, l identity.Login) {
		if !a.rate(w, r) {
			return
		}
		var in struct {
			Code string `json:"code"`
		}
		if !decode(w, r, &in) {
			return
		}
		a.pendingMu.Lock()
		p, ok := a.pending[l.User.ID]
		a.pendingMu.Unlock()
		if !ok || time.Now().After(p.expires) {
			fail(w, 409, "totp_setup_expired")
			return
		}
		codes, err := a.store.EnableTOTP(r.Context(), l.User.ID, l.Token, p.secret, in.Code)
		if err != nil {
			problem(w, err)
			return
		}
		a.pendingMu.Lock()
		delete(a.pending, l.User.ID)
		a.pendingMu.Unlock()
		a.event(r, slog.LevelInfo, "totp_enabled", "user", l.User.Username)
		reply(w, 200, map[string]any{"recovery_codes": codes})
	}, true, false))
	a.mux.HandleFunc("POST /api/auth/totp/recovery", a.auth(func(w http.ResponseWriter, r *http.Request, l identity.Login) {
		var in struct {
			Password string `json:"password"`
		}
		if !decode(w, r, &in) || !a.reauth(w, r, l, in.Password) {
			return
		}
		codes, err := a.store.NewRecoveryCodes(l.User.ID)
		if err != nil {
			problem(w, err)
			return
		}
		a.event(r, slog.LevelInfo, "totp_recovery_codes_renewed", "user", l.User.Username)
		reply(w, 200, map[string]any{"recovery_codes": codes})
	}, true, false))
	a.mux.HandleFunc("POST /api/auth/totp/disable", a.auth(func(w http.ResponseWriter, r *http.Request, l identity.Login) {
		var in struct {
			Password string `json:"password"`
		}
		if !decode(w, r, &in) || !a.reauth(w, r, l, in.Password) {
			return
		}
		if err := a.store.DisableTOTP(l.User.ID, l.Token); err != nil {
			problem(w, err)
			return
		}
		a.event(r, slog.LevelInfo, "totp_disabled", "user", l.User.Username)
		w.WriteHeader(204)
	}, true, false))
	// For someone who lost their authenticator and recovery codes: all their
	// sessions end, and they sign in with the password alone until they set
	// two-factor authentication up again.
	a.mux.HandleFunc("DELETE /api/users/{id}/totp", a.auth(func(w http.ResponseWriter, r *http.Request, l identity.Login) {
		var in struct {
			Reauth string `json:"reauth_password"`
		}
		if !decode(w, r, &in) || !a.reauth(w, r, l, in.Reauth) {
			return
		}
		token := ""
		if r.PathValue("id") == l.User.ID {
			token = l.Token
		}
		if err := a.store.DisableTOTP(r.PathValue("id"), token); err != nil {
			problem(w, err)
			return
		}
		a.event(r, slog.LevelWarn, "totp_reset", "user", l.User.Username, "target_id", r.PathValue("id"))
		w.WriteHeader(204)
	}, true, true))
}
