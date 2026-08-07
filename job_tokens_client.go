package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	openauth "github.com/TheGrimmChester/open-auth-go"
)

type mintedJobToken struct {
	Token string
	JTI   string
}

func mintJobToken(ctx context.Context, aud, scope, orgID, userID, agentKey string, ttl time.Duration) (mintedJobToken, error) {
	base := strings.TrimRight(strings.TrimSpace(os.Getenv("PEER_OAM_URL")), "/")
	if base == "" {
		return mintedJobToken{}, fmt.Errorf("PEER_OAM_URL not configured")
	}
	secret := []byte(strings.TrimSpace(os.Getenv("OPEN_SERVICE_JWT_SECRET")))
	if len(secret) == 0 {
		return mintedJobToken{}, fmt.Errorf("OPEN_SERVICE_JWT_SECRET not set")
	}
	svcTok, err := openauth.MintServiceJWT(secret, "opm-api", "oam-api", "job-tokens:mint")
	if err != nil {
		return mintedJobToken{}, err
	}
	if ttl <= 0 {
		ttl = 15 * time.Minute
	}
	body, _ := json.Marshal(map[string]interface{}{
		"product":         "opm-api",
		"audience":        aud,
		"scope":           scope,
		"organization_id": orgID,
		"user_id":         userID,
		"agent_key":       agentKey,
		"ttl_seconds":     int(ttl / time.Second),
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/api/internal/job-tokens/mint", bytes.NewReader(body))
	if err != nil {
		return mintedJobToken{}, err
	}
	req.Header.Set("Authorization", "Bearer "+svcTok)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return mintedJobToken{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return mintedJobToken{}, fmt.Errorf("job token mint %d", resp.StatusCode)
	}
	var out struct {
		Token string `json:"token"`
		JTI   string `json:"jti"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return mintedJobToken{}, err
	}
	allowLocalJobToken(ctx, "opm-api", out.JTI, ttl)
	return mintedJobToken{Token: out.Token, JTI: out.JTI}, nil
}

func revokeJobToken(ctx context.Context, jti string) {
	jti = strings.TrimSpace(jti)
	if jti == "" {
		return
	}
	revokeLocalJobToken(ctx, "opm-api", jti)
	base := strings.TrimRight(strings.TrimSpace(os.Getenv("PEER_OAM_URL")), "/")
	secret := []byte(strings.TrimSpace(os.Getenv("OPEN_SERVICE_JWT_SECRET")))
	if base == "" || len(secret) == 0 {
		return
	}
	svcTok, err := openauth.MintServiceJWT(secret, "opm-api", "oam-api", "job-tokens:revoke")
	if err != nil {
		return
	}
	body, _ := json.Marshal(map[string]string{"product": "opm-api", "jti": jti})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, base+"/api/internal/job-tokens/revoke", bytes.NewReader(body))
	if req == nil {
		return
	}
	req.Header.Set("Authorization", "Bearer "+svcTok)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err == nil && resp != nil {
		resp.Body.Close()
	}
}
