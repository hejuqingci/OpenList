package szczk

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/errs"
	log "github.com/sirupsen/logrus"
	"resty.dev/v3"
)

// authenticate handles the initial authentication to get access and refresh tokens.
func (d *Szczk) authenticate(ctx context.Context) error {
	log.Debug("Attempting initial authentication with Szczk Cloud")
	resp, err := d.client.R().
		SetContext(ctx).
		SetQueryParams(map[string]string{
			"api_key": d.Addition.APIKey,
			"api_secret": d.Addition.APISecret,
		}).
		Get(d.Addition.AuthURL + "/authenticate") // Assuming AuthURL is the base for authentication

	if err != nil {
		return fmt.Errorf("authentication request failed: %w", err)
	}

	if resp.IsError() {
		return fmt.Errorf("authentication failed with status %d: %s", resp.StatusCode(), resp.String())
	}

	var authResp AuthResponse
	err = json.Unmarshal(resp.Body(), &authResp)
	if err != nil {
		return fmt.Errorf("failed to parse authentication response: %w", err)
	}

	d.accessToken = authResp.AccessToken
	d.refreshToken = authResp.RefreshToken
	d.tokenExpiresAt = time.Now().Add(time.Duration(authResp.ExpiresIn) * time.Second)
	log.Debug("Successfully authenticated with Szczk Cloud")
	return nil
}

// refreshAccessToken handles refreshing the access token using the refresh token.
func (d *Szczk) refreshAccessToken(ctx context.Context) error {
	log.Debug("Attempting to refresh access token")
	resp, err := d.client.R().
		SetContext(ctx).
		SetHeader("Authorization", "Bearer "+d.accessToken).
		SetBody(map[string]string{
			"refresh_token": d.refreshToken,
		}).
		Post(d.Addition.AuthURL + "/refresh_token") // Assuming AuthURL is the base for authentication

	if err != nil {
		return fmt.Errorf("token refresh request failed: %w", err)
	}

	if resp.IsError() {
		return fmt.Errorf("token refresh failed with status %d: %s", resp.StatusCode(), resp.String())
	}

	var refreshResp RefreshTokenResponse
	err = json.Unmarshal(resp.Body(), &refreshResp)
	if err != nil {
		return fmt.Errorf("failed to parse token refresh response: %w", err)
	}

	d.accessToken = refreshResp.AccessToken
	d.tokenExpiresAt = time.Now().Add(time.Duration(refreshResp.ExpiresIn) * time.Second)
	log.Debug("Access token refreshed successfully")
	return nil
}

// startTokenRefresh starts a background goroutine to refresh the access token periodically.
func (d *Szczk) startTokenRefresh(ctx context.Context) {
	log.Debug("Starting token refresh goroutine")
	// Refresh token 5 minutes before it expires
	refreshInterval := d.tokenExpiresAt.Sub(time.Now()) - 5*time.Minute
	if refreshInterval < 0 {
		refreshInterval = 1 * time.Minute // If already expired or too close, try refreshing in 1 minute
	}

	ticker := time.NewTicker(refreshInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Debug("Token refresh goroutine stopped.")
			return
		case <-ticker.C:
			err := d.refreshAccessToken(ctx)
			if err != nil {
				log.Errorf("Error refreshing access token: %v", err)
				// Potentially re-authenticate if refresh token also fails
				err = d.authenticate(ctx)
				if err != nil {
					log.Errorf("Error re-authenticating: %v", err)
				}
			}
			// Reset ticker for next refresh based on new token expiry
			newRefreshInterval := d.tokenExpiresAt.Sub(time.Now()) - 5*time.Minute
			if newRefreshInterval < 0 {
				newRefreshInterval = 1 * time.Minute
			}
			ticker.Reset(newRefreshInterval)
		}
	}
}

// makeRequest is a helper to make authenticated API requests.
func (d *Szczk) makeRequest(ctx context.Context, method, path string, body interface{}) (*resty.Response, error) {
	req := d.client.R().
		SetContext(ctx).
		SetHeader("Authorization", "Bearer "+d.accessToken)

	if body != nil {
		req.SetBody(body)
	}

	resp, err := req.Execute(method, path)
	if err != nil {
		return nil, fmt.Errorf("API request failed: %w", err)
	}

	if resp.IsError() {
		// Check for token expiration and try to refresh
		if resp.StatusCode() == http.StatusUnauthorized || resp.StatusCode() == http.StatusForbidden {
			log.Warn("Access token expired or invalid, attempting to refresh...")
			err := d.refreshAccessToken(ctx)
			if err != nil {
				return nil, fmt.Errorf("failed to refresh token after unauthorized response: %w", err)
			}
			// Retry the request with the new token
			log.Info("Retrying request with new access token...")
			return d.makeRequest(ctx, method, path, body)
		}
		return nil, fmt.Errorf("API request failed with status %d: %s", resp.StatusCode(), resp.String())
	}

	return resp, nil
}

