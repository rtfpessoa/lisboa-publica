package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type metroOAuthToken struct {
	Token   string `json:"access_token"`
	Expires int64  `json:"expires_in"`
	Type    string `json:"token_type"`
}

func (m *MetroClient) token(ctx context.Context) (string, error) {
	if m.tokenValue != "" && time.Now().Before(m.expires) {
		return m.tokenValue, nil
	}
	token, err := m.acquireToken(ctx)
	if err != nil {
		return "", err
	}
	ttl := time.Duration(token.Expires) * time.Second
	margin := min(maximumTokenMargin, ttl/tokenMarginFraction)
	m.expires = time.Now().Add(ttl - margin)
	m.tokenValue = token.Token
	return m.tokenValue, nil
}
func (m *MetroClient) acquireToken(ctx context.Context) (metroOAuthToken, error) {
	body := url.Values{"grant_type": []string{"client_credentials"}}.Encode()
	route, err := http.NewRequestWithContext(ctx, "POST", m.TokenURL, strings.NewReader(body))
	if err != nil {
		return metroOAuthToken{}, err
	}
	route.SetBasicAuth(m.ClientID, m.Secret)
	route.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	release := ProtectUpstreamWork(m.Client, route.URL.Hostname(), 3)
	defer release()
	res, err := m.Client.Do(route)
	if err != nil {
		return metroOAuthToken{}, fmt.Errorf("Metro OAuth connection failed")
	}
	defer res.Body.Close()
	return decodeMetroToken(res)
}
func decodeMetroToken(res *http.Response) (metroOAuthToken, error) {
	if res.StatusCode != http.StatusOK {
		return metroOAuthToken{}, fmt.Errorf("Metro OAuth HTTP%d", res.StatusCode)
	}
	var token metroOAuthToken
	if err := json.NewDecoder(io.LimitReader(res.Body, metroOAuthResponseBytes)).Decode(&token); err != nil || !validMetroToken(token) {
		return metroOAuthToken{}, fmt.Errorf("invalid Metro OAuth response")
	}
	return token, nil
}
func validMetroToken(token metroOAuthToken) bool {
	return token.Token != "" && token.Expires > 0 && strings.EqualFold(token.Type, "Bearer")
}
func (m *MetroClient) get(ctx context.Context, path string, dst any) error {
	token, err := m.token(ctx)
	if err != nil {
		return err
	}
	route, err := http.NewRequestWithContext(ctx, "GET", m.Base+path, nil)
	if err != nil {
		return err
	}
	route.Header.Set("Authorization", "Bearer "+token)
	return m.readAPI(route, dst)
}
func (m *MetroClient) readAPI(route *http.Request, dst any) error {
	release := ProtectUpstreamWork(m.Client, route.URL.Hostname(), 3)
	defer release()
	res, err := m.Client.Do(route)
	if err != nil {
		return fmt.Errorf("Metro API connection failed")
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusUnauthorized {
		m.tokenValue = ""
		m.expires = time.Time{}
	}
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("Metro API HTTP%d", res.StatusCode)
	}
	return decodeMetroAPI(res.Body, dst)
}
func decodeMetroAPI(body io.Reader, dst any) error {
	var envelope struct {
		Code json.RawMessage `json:"codigo"`
		Data json.RawMessage `json:"resposta"`
	}
	if err := json.NewDecoder(io.LimitReader(body, 2<<20)).Decode(&envelope); err != nil || !validMetroEnvelope(envelope.Code, envelope.Data) {
		return fmt.Errorf("invalid Metro API envelope")
	}
	if json.Unmarshal(envelope.Data, dst) != nil {
		return fmt.Errorf("invalid Metro API data")
	}
	return nil
}
func validMetroEnvelope(code, data json.RawMessage) bool {
	return (string(code) == "200" || string(code) == `"200"`) && len(data) > 0
}
