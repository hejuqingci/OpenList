package szczk

import "time"

// AuthResponse represents the response from the authentication API
type AuthResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
}

// RefreshTokenResponse represents the response from the token refresh API
type RefreshTokenResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int    `json:"expires_in"`
}

// FileListResponse represents the response from the list files API
type FileListResponse struct {
	Files []FileItem `json:"files"`
}

// FileItem represents a single file or folder item in the list
type FileItem struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Size       int64     `json:"size"`
	IsFolder   bool      `json:"is_folder"`
	ModifiedAt time.Time `json:"modified_at"` // Assuming time.Time for direct unmarshaling
}

// DownloadLinkResponse represents the response from the get download link API
type DownloadLinkResponse struct {
	URL string `json:"url"`
}

// FirstUploadResponse represents the response from the first upload API
type FirstUploadResponse struct {
	UploadURL   string `json:"upload_url"`
	UploadToken string `json:"upload_token"`
}

