package szczk

import (
	"context"
	"encoding/json"
	"strings"
	"fmt"
	"net/http"
	"time"

	"github.com/hejuqingci/OpenList/driver"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/errs"
	log "github.com/sirupsen/logrus"
	"resty.dev/v3"
)

type Szczk struct {
	model.Storage
	Addition
	client *resty.Client
	accessToken string
	refreshToken string
	tokenExpiresAt time.Time
}

type Addition struct {
	APIKey    string `json:"api_key"`
	APISecret string `json:"api_secret"`
	RootFolderID string `json:"root_folder_id"`
	AuthURL string `json:"auth_url"`
	BaseURL string `json:"base_url"`
}

func (d *Szczk) Config() driver.Config {
	return config
}

func (d *Szczk) GetAddition() driver.Additional {
	return &d.Addition
}

func (d *Szczk) Init(ctx context.Context) error {
	log.Debug("Initializing Szczk driver")
	d.client = resty.New()
	d.client.SetBaseURL(d.Addition.BaseURL)

	// Authenticate and get initial tokens
	err := d.authenticate(ctx)
	if err != nil {
		return fmt.Errorf("failed to authenticate: %w", err)
	}

	// Start background token refresh
	go d.startTokenRefresh(ctx)

	return nil
}

func (d *Szczk) Drop(ctx context.Context) error {
	log.Debug("Dropping Szczk driver")
	// No specific cleanup needed for this driver
	return nil
}

func (d *Szczk) authenticate(ctx context.Context) error {
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

	var authResp struct {
		AccessToken string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn int `json:"expires_in"`
	}

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

func (d *Szczk) refreshAccessToken(ctx context.Context) error {
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

	var refreshResp struct {
		AccessToken string `json:"access_token"`
		ExpiresIn int `json:"expires_in"`
	}

	err = json.Unmarshal(resp.Body(), &refreshResp)
	if err != nil {
		return fmt.Errorf("failed to parse token refresh response: %w", err)
	}

	d.accessToken = refreshResp.AccessToken
	d.tokenExpiresAt = time.Now().Add(time.Duration(refreshResp.ExpiresIn) * time.Second)
	log.Debug("Access token refreshed successfully")
	return nil
}

func (d *Szczk) startTokenRefresh(ctx context.Context) {
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

// List implements the driver.Reader interface
func (d *Szczk) List(ctx context.Context, dir model.Obj, args model.ListArgs) ([]model.Obj, error) {
	folderID := d.RootFolderID
	if dir != nil {
		folderID = dir.GetID()
	}

	resp, err := d.client.R().
		SetContext(ctx).
		SetHeader("Authorization", "Bearer "+d.accessToken).
		SetQueryParams(map[string]string{
			"folder_id": folderID,
		}).
		Get("/list_files")

	if err != nil {
		return nil, fmt.Errorf("failed to list files: %w", err)
	}

	if resp.IsError() {
		return nil, fmt.Errorf("failed to list files with status %d: %s", resp.StatusCode(), resp.String())
	}

	var listResp struct {
		Files []struct {
			ID string `json:"id"`
			Name string `json:"name"`
			Size int64 `json:"size"`
			IsFolder bool `json:"is_folder"`
			ModifiedAt string `json:"modified_at"`
		} `json:"files"`
	}

	err = json.Unmarshal(resp.Body(), &listResp)
	if err != nil {
		return nil, fmt.Errorf("failed to parse list files response: %w", err)
	}

	var objs []model.Obj
	for _, file := range listResp.Files {
		modifiedTime, _ := time.Parse(time.RFC3339, file.ModifiedAt) // Assuming RFC3339 format
		obj := &model.Object{
			ID: file.ID,
			Name: file.Name,
			Size: file.Size,
			IsFolder: file.IsFolder,
			Modified: modifiedTime,
			// Path needs to be constructed based on parent folder ID and file name
			Path: dir.GetPath() + "/" + file.Name, // This needs refinement for actual path construction
		}
		objs = append(objs, obj)
	}
	return objs, nil
}

// Link implements the driver.Reader interface
func (d *Szczk) Link(ctx context.Context, file model.Obj, args model.LinkArgs) (*model.Link, error) {
	if file.IsDir() {
		return nil, errs.NotFile
	}

	resp, err := d.client.R().
		SetContext(ctx).
		SetHeader("Authorization", "Bearer "+d.accessToken).
		SetQueryParams(map[string]string{
			"file_id": file.GetID(),
		}).
		Get("/get_download_url")

	if err != nil {
		return nil, fmt.Errorf("failed to get download link: %w", err)
	}

	if resp.IsError() {
		return nil, fmt.Errorf("failed to get download link with status %d: %s", resp.StatusCode(), resp.String())
	}

	var linkResp struct {
		URL string `json:"url"`
	}

	err = json.Unmarshal(resp.Body(), &linkResp)
	if err != nil {
		return nil, fmt.Errorf("failed to parse download link response: %w", err)
	}

	return &model.Link{
		URL: linkResp.URL,
		// ContentLength: file.GetSize(), // Assuming the API doesn't return this, or we can get it from file.GetSize()
	}, nil
}

// MakeDir implements the driver.Mkdir interface
func (d *Szczk) MakeDir(ctx context.Context, parentDir model.Obj, dirName string) error {
	// The API documentation does not explicitly provide a "create folder" endpoint.
	// Assuming it's not directly supported or requires a different approach for now.
	return errs.NotImplement
}

// Rename implements the driver.Rename interface
func (d *Szczk) Rename(ctx context.Context, srcObj model.Obj, newName string) error {
	resp, err := d.client.R().
		SetContext(ctx).
		SetHeader("Authorization", "Bearer "+d.accessToken).
		SetBody(map[string]string{
			"item_id": srcObj.GetID(),
			"new_name": newName,
		}).
		Post("/rename_item")

	if err != nil {
		return fmt.Errorf("failed to rename item: %w", err)
	}

	if resp.IsError() {
		return fmt.Errorf("failed to rename item with status %d: %s", resp.StatusCode(), resp.String())
	}

	return nil
}

// Move implements the driver.Move interface
func (d *Szczk) Move(ctx context.Context, srcObj, dstDir model.Obj) error {
	resp, err := d.client.R().
		SetContext(ctx).
		SetHeader("Authorization", "Bearer "+d.accessToken).
		SetBody(map[string]string{
			"item_id": srcObj.GetID(),
			"destination_folder_id": dstDir.GetID(),
		}).
		Post("/move_item")

	if err != nil {
		return fmt.Errorf("failed to move item: %w", err)
	}

	if resp.IsError() {
		return fmt.Errorf("failed to move item with status %d: %s", resp.StatusCode(), resp.String())
	}

	return nil
}

// Remove implements the driver.Remove interface
func (d *Szczk) Remove(ctx context.Context, obj model.Obj) error {
	resp, err := d.client.R().
		SetContext(ctx).
		SetHeader("Authorization", "Bearer "+d.accessToken).
		SetBody(map[string]string{
			"item_id": obj.GetID(),
		}).
		Post("/delete_item")

	if err != nil {
		return fmt.Errorf("failed to delete item: %w", err)
	}

	if resp.IsError() {
		return nil, fmt.Errorf("failed to delete item with status %d: %s", resp.StatusCode(), resp.String())
	}

	return nil
}

// Put implements the driver.Put interface
func (d *Szczk) Put(ctx context.Context, dstDir model.Obj, file model.FileStreamer, up driver.UpdateProgress) error {
	// Step 1: Call /first_upload
	firstUploadResp, err := d.client.R().
		SetContext(ctx).
		SetHeader("Authorization", "Bearer "+d.accessToken).
		SetBody(map[string]interface{}{
			"parent_folder_id": dstDir.GetID(),
			"file_name": file.GetName(),
			"file_size": file.GetSize(),
			// "file_hash": "", // Assuming hash is not strictly required or can be calculated later
		}).
		Post("/first_upload")

	if err != nil {
		return fmt.Errorf("failed to initiate upload (first_upload): %w", err)
	}

	if firstUploadResp.IsError() {
		return fmt.Errorf("failed to initiate upload with status %d: %s", firstUploadResp.StatusCode(), firstUploadResp.String())
	}

	var firstUploadData struct {
		UploadURL string `json:"upload_url"`
		UploadToken string `json:"upload_token"`
	}

	err = json.Unmarshal(firstUploadResp.Body(), &firstUploadData)
	if err != nil {
		return fmt.Errorf("failed to parse first_upload response: %w", err)
	}

	// Step 2: Upload the file content to UploadURL
	uploadClient := resty.New()
	uploadResp, err := uploadClient.R().
		SetContext(ctx).
		SetFileReader("file", file.GetName(), file).
		Post(firstUploadData.UploadURL)

	if err != nil {
		return fmt.Errorf("failed to upload file content: %w", err)
	}

	if uploadResp.IsError() {
		return fmt.Errorf("failed to upload file content with status %d: %s", uploadResp.StatusCode(), uploadResp.String())
	}

	// Step 3: Call /ok_upload to finalize
	okUploadResp, err := d.client.R().
		SetContext(ctx).
		SetHeader("Authorization", "Bearer "+d.accessToken).
		SetBody(map[string]string{
			"upload_token": firstUploadData.UploadToken,
		}).
		Post("/ok_upload")

	if err != nil {
		return fmt.Errorf("failed to finalize upload (ok_upload): %w", err)
	}

	if okUploadResp.IsError() {
		return fmt.Errorf("failed to finalize upload with status %d: %s", okUploadResp.StatusCode(), okUploadResp.String())
	}

	// Update progress (simplified, actual implementation would involve more granular updates during file upload)
	if up != nil {
		up.UpdateProgress(file.GetSize(), file.GetSize())
	}

	return nil
}

// Register the driver
func init() {
	driver.RegisterDriver(&Szczk{})
}

// Get implements the driver.Getter interface
func (d *Szczk) Get(ctx context.Context, path string) (model.Obj, error) {
	// The API does not have a direct "get file by path" endpoint.
	// We need to list the parent directory and find the file by name.

	// First, determine the parent folder ID and file name from the path.
	// Assuming path is like "/folder1/folder2/file.txt"
	parentPath := "/"
	fileName := path

	lastSlash := strings.LastIndex(path, "/")
	if lastSlash != -1 {
		parentPath = path[:lastSlash]
		fileName = path[lastSlash+1:]
	}

	// If the path is the root, then the parent is the root folder ID
	parentFolderID := d.RootFolderID
	if parentPath != "/" {
		// This part is tricky. We need to resolve the parentPath to a folder ID.
		// For simplicity, let's assume for now that `path` directly corresponds to an item ID
		// or that `List` can be called recursively to find the parent folder ID.
		// For a more robust solution, a path-to-ID resolution mechanism would be needed.
		// For now, we'll assume the 'path' provided to Get is actually the 'ID' of the object.
		// This is a simplification and might need adjustment based on actual API behavior.
		parentFolderID = path // This is a placeholder, needs actual path resolution
	}

	// Call List to get the files in the parent directory
	// This is inefficient for a single Get operation, but necessary if no direct Get by path/ID exists.
	listArgs := model.ListArgs{}
	parentObj := &model.Object{ID: parentFolderID, Path: parentPath, IsFolder: true}
	files, err := d.List(ctx, parentObj, listArgs)
	if err != nil {
		return nil, fmt.Errorf("failed to list parent directory for Get operation: %w", err)
	}

	for _, file := range files {
		if file.GetName() == fileName {
			return file, nil
		}
	}

	return nil, errs.ObjectNotFound
}

// Ensure Szczk implements all necessary interfaces
var (
	_ driver.Driver = (*Szczk)(nil)
	_ driver.Reader = (*Szczk)(nil)
		_ driver.Mkdir = (*Szczk)(nil)
	_ driver.Getter = (*Szczk)(nil)

	_ driver.Rename = (*Szczk)(nil)
	_ driver.Move = (*Szczk)(nil)
	_ driver.Remove = (*Szczk)(nil)
	_ driver.Put = (*Szczk)(nil)
	// _ driver.Copy = (*Szczk)(nil) // Copy is not directly supported by the API, would need to implement client-side copy
	// _ driver.Getter = (*Szczk)(nil) // Get is not directly supported, List provides file info
)

