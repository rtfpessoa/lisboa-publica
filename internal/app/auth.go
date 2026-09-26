package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"google.golang.org/api/idtoken"
	"lisboapublica/internal/api"
)

type contextKey int

const requestKey contextKey = 0
const writerKey contextKey = 1
const identityKey contextKey = 2

type identity struct {
	Email, Name, Kind, KeyID string
	Scopes                   []string
	Expires                  time.Time
	Session                  bool
}

func request(ctx context.Context) *http.Request { return ctx.Value(requestKey).(*http.Request) }
func writer(ctx context.Context) http.ResponseWriter {
	return ctx.Value(writerKey).(http.ResponseWriter)
}
func principal(ctx context.Context) *identity { v, _ := ctx.Value(identityKey).(*identity); return v }
func randomSecret() (string, error) {
	var b [randomSecretBytes]byte
	if _, e := rand.Read(b[:]); e != nil {
		return "", e
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}
func tokenHash(s string) string { b := sha256.Sum256([]byte(s)); return hex.EncodeToString(b[:]) }
func (s *Server) authenticate(r *http.Request) (*identity, error) {
	if header := r.Header.Get("Authorization"); header != "" {
		return s.authenticateKey(r, header)
	}
	return s.authenticateSession(r)
}

func (s *Server) authenticateKey(r *http.Request, header string) (*identity, error) {
	if !strings.HasPrefix(header, "Bearer lp_") {
		return nil, fail(http.StatusUnauthorized, "invalid_key", "Chave inválida.")
	}
	token := strings.TrimPrefix(header, "Bearer ")
	actor := &identity{}
	var revoked bool
	err := s.Store.DB.QueryRow(r.Context(), "SELECT id,owner_email,name,scopes,expires_at,revoked FROM api_keys WHERE token_hash=$1", tokenHash(token)).Scan(&actor.KeyID, &actor.Email, &actor.Name, &actor.Scopes, &actor.Expires, &revoked)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && (revoked || !actor.Expires.After(time.Now())) {
		return nil, fail(http.StatusUnauthorized, "invalid_key", "Chave inválida, expirada ou revogada.")
	}
	if err != nil {
		return nil, err
	}
	actor.Kind = "api_key"
	return actor, nil
}

func (s *Server) authenticateSession(r *http.Request) (*identity, error) {
	cookie, err := r.Cookie("lp_session")
	if err == http.ErrNoCookie {
		return nil, nil
	}
	if err != nil {
		return nil, fail(http.StatusUnauthorized, "invalid_session", "Sessão inválida.")
	}
	actor := &identity{Session: true, Scopes: []string{"read:transit", "read:history"}}
	err = s.Store.DB.QueryRow(r.Context(), "SELECT email,name,auth_kind,expires_at FROM sessions WHERE token_hash=$1", tokenHash(cookie.Value)).Scan(&actor.Email, &actor.Name, &actor.Kind, &actor.Expires)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && !actor.Expires.After(time.Now()) {
		return nil, fail(http.StatusUnauthorized, "invalid_session", "Sessão expirada.")
	}
	if err != nil {
		return nil, err
	}
	return actor, nil
}
func (s *Server) cookie(w http.ResponseWriter, name, value string, expiry time.Time, same http.SameSite) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: "/", HttpOnly: true, Secure: strings.HasPrefix(s.Options.Origin, "https://"), SameSite: same, Expires: expiry, MaxAge: int(time.Until(expiry).Seconds())})
}
func (s *Server) clearCookie(w http.ResponseWriter, name string, same http.SameSite) {
	http.SetCookie(w, &http.Cookie{Name: name, Path: "/", HttpOnly: true, Secure: strings.HasPrefix(s.Options.Origin, "https://"), SameSite: same, MaxAge: -1, Expires: time.Unix(0, 0)})
}
func (s *Server) loginBinding(ctx context.Context, nonce string) error {
	r := request(ctx)
	if r.Header.Get("Origin") != s.Options.Origin {
		return fail(http.StatusForbidden, "origin", "Origem não autorizada.")
	}
	cookie, e := r.Cookie("lp_login")
	if e != nil || len(nonce) < minimumLoginNonceLength || cookie.Value != nonce {
		return fail(http.StatusForbidden, "login_binding", "Recarregue a página para iniciar sessão.")
	}
	return nil
}
func user(p *identity) api.User {
	return api.User{Email: p.Email, Name: p.Name, Scopes: p.Scopes, ExpiresAt: p.Expires, AuthKind: api.UserAuthKind(p.Kind)}
}
func (s *Server) newSession(ctx context.Context, email, name, kind string) (api.User, error) {
	secret, e := randomSecret()
	if e != nil {
		return api.User{}, e
	}
	expiry := time.Now().UTC().Add(sessionLifetime)
	_, e = s.Store.exec(ctx, "INSERT INTO sessions(token_hash,email,name,auth_kind,expires_at) VALUES($1,$2,$3,$4,$5)", tokenHash(secret), email, name, kind, expiry)
	if e != nil {
		return api.User{}, e
	}
	s.cookie(writer(ctx), "lp_session", secret, expiry, http.SameSiteLaxMode)
	s.clearCookie(writer(ctx), "lp_login", http.SameSiteStrictMode)
	return user(&identity{Email: email, Name: name, Kind: kind, Expires: expiry, Scopes: []string{"read:transit", "read:history"}}), nil
}

// GoogleLogin verifies a Google credential and its browser nonce before creating a session.
func (s *Server) GoogleLogin(ctx context.Context, r api.GoogleLoginRequestObject) (api.GoogleLoginResponseObject, error) {
	if s.Options.GoogleClientID == "" {
		return nil, fail(http.StatusServiceUnavailable, "google_unconfigured", "Google não está configurado.")
	}
	if r.Body == nil {
		return nil, fail(http.StatusBadRequest, "body", "Credencial em falta.")
	}
	if request(ctx).Header.Get("Origin") != s.Options.Origin {
		return nil, fail(http.StatusForbidden, "origin", "Origem não autorizada.")
	}
	verifyCtx, cancel := context.WithTimeout(ctx, googleVerificationTimeout)
	defer cancel()
	payload, err := s.VerifyGoogle(verifyCtx, r.Body.Credential, s.Options.GoogleClientID)
	if err != nil {
		return nil, fail(http.StatusUnauthorized, "google_token", "Credencial Google inválida.")
	}
	email, name, err := s.googleClaims(ctx, payload)
	if err != nil {
		return nil, err
	}
	result, err := s.newSession(ctx, email, name, "google")
	return api.GoogleLogin200JSONResponse(result), err
}

// DevelopmentLogin creates a test session only for explicit loopback development requests.
func (s *Server) DevelopmentLogin(ctx context.Context, r api.DevelopmentLoginRequestObject) (api.DevelopmentLoginResponseObject, error) {
	if !s.Options.DevAuth || s.Options.Environment != "development" {
		return nil, fail(http.StatusNotFound, "not_found", "Não encontrado.")
	}
	host, _, e := net.SplitHostPort(request(ctx).RemoteAddr)
	if e != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		return nil, fail(http.StatusForbidden, "local_only", "Sessão de desenvolvimento apenas em localhost.")
	}
	if r.Body == nil {
		return nil, fail(http.StatusBadRequest, "body", "Nonce em falta.")
	}
	if e = s.loginBinding(ctx, r.Body.LoginNonce); e != nil {
		return nil, e
	}
	u, e := s.newSession(ctx, "developer@localhost", "Desenvolvimento local", "development")
	return api.DevelopmentLogin200JSONResponse(u), e
}

// GetMe returns the authenticated browser user.
func (s *Server) GetMe(ctx context.Context, _ api.GetMeRequestObject) (api.GetMeResponseObject, error) {
	return api.GetMe200JSONResponse(user(principal(ctx))), nil
}

// Logout revokes the browser session and clears its cookie.
func (s *Server) Logout(ctx context.Context, _ api.LogoutRequestObject) (api.LogoutResponseObject, error) {
	s.clearCookie(writer(ctx), "lp_session", http.SameSiteLaxMode)
	cookie, _ := request(ctx).Cookie("lp_session")
	if cookie != nil {
		if _, e := s.Store.execCleanup(ctx, "DELETE FROM sessions WHERE token_hash=$1", tokenHash(cookie.Value)); e != nil {
			return nil, e
		}
	}
	return api.Logout204Response{}, nil
}

// ListKeys lists only the current browser user’s API keys.
func (s *Server) ListKeys(ctx context.Context, _ api.ListKeysRequestObject) (api.ListKeysResponseObject, error) {
	filter, err := s.filter(ctx, false)
	if err != nil {
		return nil, err
	}
	rows, err := s.Store.DB.Query(ctx, "SELECT id,name,scopes,created_at,expires_at,revoked FROM api_keys WHERE owner_email=$1 ORDER BY created_at DESC,id", principal(ctx).Email)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []api.ApiKey{}
	for rows.Next() {
		var v api.ApiKey
		var scopes []string
		if err = rows.Scan(&v.Id, &v.Name, &scopes, &v.CreatedAt, &v.ExpiresAt, &v.Revoked); err != nil {
			return nil, err
		}
		v.Scopes = []api.ApiKeyScopes{}
		for _, scope := range scopes {
			v.Scopes = append(v.Scopes, api.ApiKeyScopes(scope))
		}
		out = append(out, v)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	page, data := paginate(out, filter, "")
	return api.ListKeys200JSONResponse{Data: data, Page: page}, nil
}

// CreateKey creates a bounded, expiring key and shows its secret once.
func (s *Server) CreateKey(ctx context.Context, r api.CreateKeyRequestObject) (api.CreateKeyResponseObject, error) {
	scopes, err := validateKey(r.Body)
	if err != nil {
		return nil, err
	}
	key, secret, err := newPersonalKey(r.Body.Name, scopes)
	if err != nil {
		return nil, err
	}
	if err = s.Store.insertPersonalKey(ctx, principal(ctx).Email, key, secret); err != nil {
		return nil, err
	}
	writer(ctx).Header().Set("Cache-Control", "no-store")
	return api.CreateKey201JSONResponse{Key: key, Secret: secret}, nil
}

func newPersonalKey(name string, scopes []string) (api.ApiKey, string, error) {
	secret, err := randomSecret()
	if err != nil {
		return api.ApiKey{}, "", err
	}
	id, err := randomSecret()
	if err != nil {
		return api.ApiKey{}, "", err
	}
	now := time.Now().UTC()
	key := api.ApiKey{Id: id, Name: name, CreatedAt: now, ExpiresAt: now.AddDate(0, 0, 30), Scopes: []api.ApiKeyScopes{}}
	for _, scope := range scopes {
		key.Scopes = append(key.Scopes, api.ApiKeyScopes(scope))
	}
	return key, "lp_" + secret, nil
}

// insertPersonalKey keeps quota verification and the reserved write under one process lock.
func (s *Store) insertPersonalKey(ctx context.Context, owner string, key api.ApiKey, secret string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.checkPersonalKeyQuota(ctx, owner); err != nil {
		return err
	}
	scopes := make([]string, 0, len(key.Scopes))
	for _, scope := range key.Scopes {
		scopes = append(scopes, string(scope))
	}
	_, err := s.execLocked(ctx, operationalDatabaseBytes, "INSERT INTO api_keys(id,owner_email,name,token_hash,scopes,created_at,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7)", key.Id, owner, key.Name, tokenHash(secret), scopes, key.CreatedAt, key.ExpiresAt)
	return err
}

func (s *Store) checkPersonalKeyQuota(ctx context.Context, owner string) error {
	var count int
	if err := s.DB.QueryRow(ctx, "SELECT count(*) FROM api_keys WHERE owner_email=$1 AND NOT revoked AND expires_at>$2", owner, time.Now()).Scan(&count); err != nil {
		return err
	}
	if count >= maxPersonalKeys {
		return fail(http.StatusBadRequest, "key_limit", "Limite de20 chaves ativas.")
	}
	return nil
}

// RevokeKey revokes a key belonging to the current browser user.
func (s *Server) RevokeKey(ctx context.Context, r api.RevokeKeyRequestObject) (api.RevokeKeyResponseObject, error) {
	result, e := s.Store.execCleanup(ctx, "UPDATE api_keys SET revoked=TRUE WHERE id=$1 AND owner_email=$2", r.KeyId, principal(ctx).Email)
	if e != nil {
		return nil, e
	}
	if result.RowsAffected() == 0 {
		return nil, fail(http.StatusNotFound, "not_found", "Chave não encontrada.")
	}
	return api.RevokeKey204Response{}, nil
}

type rateEntry struct {
	Start time.Time
	Count int
}
type limiter struct {
	mu      sync.Mutex
	entries map[string]rateEntry
	limit   int
}

func (l *limiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	if len(l.entries) >= maxRateEntries {
		for k, v := range l.entries {
			if now.Sub(v.Start) > time.Minute {
				delete(l.entries, k)
			}
		}
		if len(l.entries) >= maxRateEntries {
			if _, ok := l.entries[key]; !ok {
				return false
			}
		}
	}
	err := l.entries[key]
	if now.Sub(err.Start) >= time.Minute {
		err = rateEntry{Start: now}
	}
	if err.Count >= l.limit {
		return false
	}
	err.Count++
	l.entries[key] = err
	return true
}

var _ = idtoken.Validate

func (s *Server) googleClaims(ctx context.Context, payload *idtoken.Payload) (string, string, error) {
	var err error
	if payload.Issuer != "accounts.google.com" && payload.Issuer != "https://accounts.google.com" {
		return "", "", fail(http.StatusUnauthorized, "google_issuer", "Emissor Google inválido.")
	}
	if payload.Audience != s.Options.GoogleClientID || payload.Expires <= time.Now().Unix() {
		return "", "", fail(http.StatusUnauthorized, "google_token", "Credencial Google expirada.")
	}
	nonce, _ := payload.Claims["nonce"].(string)
	if err = s.loginBinding(ctx, nonce); err != nil {
		return "", "", err
	}
	verified, _ := payload.Claims["email_verified"].(bool)
	email, _ := payload.Claims["email"].(string)
	name, _ := payload.Claims["name"].(string)
	if !verified || email == "" {
		return "", "", fail(http.StatusUnauthorized, "google_email", "Email Google não verificado.")
	}
	return email, name, nil
}

func validateKey(body *api.CreateKeyJSONRequestBody) ([]string, error) {
	if body == nil {
		return nil, fail(http.StatusBadRequest, "body", "Dados em falta.")
	}
	if strings.TrimSpace(body.Name) == "" {
		return nil, fail(http.StatusBadRequest, "name", "Nome em falta.")
	}
	scopes := []string{}
	for _, scope := range body.Scopes {
		v := string(scope)
		if v != "read:transit" && v != "read:history" {
			return nil, fail(http.StatusForbidden, "scope", "Âmbito não permitido.")
		}
		scopes = append(scopes, v)
	}
	if len(scopes) < 1 {
		return nil, fail(http.StatusBadRequest, "scope", "Selecione um âmbito.")
	}
	return scopes, nil
}
