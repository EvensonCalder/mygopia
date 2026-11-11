package kopia

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ClientOptionsHTTP configures a Client talking to a local or remote kopia server.
type ClientConfig struct {
	BaseURL         string
	Username        string
	Password        string
	ControlPassword string
	ServerCertPEM   []byte
	Timeout         time.Duration
}

// Client is a concurrency-safe HTTP client for Kopia's UI and Control REST APIs.
type Client struct {
	baseURL     string
	username    string
	password    string
	controlPass string
	httpClient  *http.Client
	streamHTTP  *http.Client
}

const maxAPIResponseBytes = 64 << 20 // 64 MiB safety cap per JSON response

func compactNonNil[T any](items []*T) []*T {
	if len(items) == 0 {
		return items
	}
	out := items[:0]
	for _, it := range items {
		if it != nil {
			out = append(out, it)
		}
	}
	return out
}

func safePathSegment(seg, label string) (string, error) {
	if seg == "" || seg == "." || seg == ".." || strings.ContainsAny(seg, "/\\\x00") {
		return "", fmt.Errorf("invalid %s %q", label, seg)
	}
	return url.PathEscape(seg), nil
}

// NewClient constructs a Client pinned to the server's ephemeral TLS certificate.
func NewClient(cfg ClientConfig) (*Client, error) {
	base := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if base == "" {
		return nil, fmt.Errorf("empty kopia server base URL")
	}
	if strings.HasPrefix(strings.ToLower(base), "http://") {
		return nil, fmt.Errorf("refusing plaintext HTTP kopia server URL")
	}
	if !strings.HasPrefix(strings.ToLower(base), "https://") {
		base = "https://" + base
	}
	if len(cfg.ServerCertPEM) == 0 {
		return nil, fmt.Errorf("missing kopia server TLS certificate PEM")
	}
	user := cfg.Username
	if user == "" {
		user = "kopia"
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(cfg.ServerCertPEM) {
		return nil, fmt.Errorf("failed to parse kopia server TLS certificate PEM")
	}
	tlsCfg := &tls.Config{
		MinVersion: tls.VersionTLS12,
		RootCAs:    pool,
	}

	transport := &http.Transport{
		TLSClientConfig:     tlsCfg,
		MaxIdleConns:        32,
		MaxIdleConnsPerHost: 32,
		IdleConnTimeout:     60 * time.Second,
	}
	jar, _ := cookiejar.New(nil)
	noRedirect := func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}

	return &Client{
		baseURL:     base,
		username:    user,
		password:    cfg.Password,
		controlPass: cfg.ControlPassword,
		httpClient: &http.Client{
			Transport:     transport,
			Jar:           jar,
			Timeout:       timeout,
			CheckRedirect: noRedirect,
		},
		streamHTTP: &http.Client{
			Transport:     transport,
			Jar:           jar,
			CheckRedirect: noRedirect,
		},
	}, nil
}

func (c *Client) doJSON(ctx context.Context, method, path string, query url.Values, reqBody, respOut any, useControlAuth bool) error {
	fullURL := c.baseURL + path
	if len(query) > 0 {
		fullURL += "?" + query.Encode()
	}

	var bodyReader io.Reader
	if reqBody != nil {
		data, err := json.Marshal(reqBody)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, fullURL, bodyReader)
	if err != nil {
		return err
	}
	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")

	if useControlAuth && c.controlPass != "" {
		req.SetBasicAuth("server-control", c.controlPass)
	} else if c.password != "" {
		req.SetBasicAuth(c.username, c.password)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxAPIResponseBytes+1))
	if err != nil {
		return err
	}
	if int64(len(raw)) > maxAPIResponseBytes {
		return fmt.Errorf("kopia response for %s exceeded %d MiB safety limit", path, maxAPIResponseBytes>>20)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		apiErr := &APIError{StatusCode: resp.StatusCode}
		if err := json.Unmarshal(raw, apiErr); err != nil || (apiErr.Code == "" && apiErr.Message == "") {
			apiErr.Message = strings.TrimSpace(string(raw))
		}
		return apiErr
	}

	if respOut != nil && len(bytes.TrimSpace(raw)) > 0 {
		if err := json.Unmarshal(raw, respOut); err != nil {
			return fmt.Errorf("decode %s response: %w", path, err)
		}
	}
	return nil
}

func sourceQuery(src SourceInfo) url.Values {
	q := url.Values{}
	if src.UserName != "" {
		q.Set("userName", src.UserName)
	}
	if src.Host != "" {
		q.Set("host", src.Host)
	}
	if src.Path != "" {
		q.Set("path", src.Path)
	}
	return q
}

// --- Repository Management ---

func (c *Client) Status(ctx context.Context) (*StatusResponse, error) {
	var out StatusResponse
	if err := c.doJSON(ctx, http.MethodGet, "/api/v1/repo/status", nil, nil, &out, false); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) SupportedAlgorithms(ctx context.Context) (*SupportedAlgorithmsResponse, error) {
	var out SupportedAlgorithmsResponse
	if err := c.doJSON(ctx, http.MethodGet, "/api/v1/repo/algorithms", nil, nil, &out, false); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) CheckRepoExists(ctx context.Context, storage StorageConnection) (bool, error) {
	err := c.doJSON(ctx, http.MethodPost, "/api/v1/repo/exists", nil, CheckRepositoryExistsRequest{Storage: storage}, nil, false)
	if err == nil {
		return true, nil
	}
	if apiErr, ok := err.(*APIError); ok && apiErr.Code == "NOT_INITIALIZED" {
		return false, nil
	}
	return false, err
}

func (c *Client) ConnectRepo(ctx context.Context, req ConnectRepositoryRequest) error {
	return c.doJSON(ctx, http.MethodPost, "/api/v1/repo/connect", nil, req, nil, false)
}

func (c *Client) CreateRepo(ctx context.Context, req CreateRepositoryRequest) error {
	return c.doJSON(ctx, http.MethodPost, "/api/v1/repo/create", nil, req, nil, false)
}

func (c *Client) DisconnectRepo(ctx context.Context) error {
	return c.doJSON(ctx, http.MethodPost, "/api/v1/repo/disconnect", nil, struct{}{}, nil, false)
}

func (c *Client) SetDescription(ctx context.Context, description string) (*StatusResponse, error) {
	var out StatusResponse
	body := ClientOptions{Description: description}
	if err := c.doJSON(ctx, http.MethodPost, "/api/v1/repo/description", nil, body, &out, false); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) SyncRepo(ctx context.Context) error {
	return c.doJSON(ctx, http.MethodPost, "/api/v1/repo/sync", nil, struct{}{}, nil, false)
}

func (c *Client) GetThrottle(ctx context.Context) (*ThrottleLimits, error) {
	var out ThrottleLimits
	if err := c.doJSON(ctx, http.MethodGet, "/api/v1/repo/throttle", nil, nil, &out, false); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) SetThrottle(ctx context.Context, limits ThrottleLimits) error {
	return c.doJSON(ctx, http.MethodPut, "/api/v1/repo/throttle", nil, limits, nil, false)
}

// --- Sources & Snapshot Actions ---

func (c *Client) ListSources(ctx context.Context) (*SourcesResponse, error) {
	var out SourcesResponse
	if err := c.doJSON(ctx, http.MethodGet, "/api/v1/sources", nil, nil, &out, false); err != nil {
		return nil, err
	}
	out.Sources = compactNonNil(out.Sources)
	return &out, nil
}

func (c *Client) CreateSource(ctx context.Context, folderPath string, createSnapshotNow bool, pol *Policy) (*CreateSnapshotSourceResponse, error) {
	if pol == nil {
		pol = &Policy{}
	}
	var out CreateSnapshotSourceResponse
	req := CreateSnapshotSourceRequest{
		Path:           folderPath,
		CreateSnapshot: createSnapshotNow,
		Policy:         pol,
	}
	if err := c.doJSON(ctx, http.MethodPost, "/api/v1/sources", nil, req, &out, false); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) TriggerSnapshot(ctx context.Context, src *SourceInfo) error {
	var q url.Values
	if src != nil {
		q = sourceQuery(*src)
	}
	return c.doJSON(ctx, http.MethodPost, "/api/v1/sources/upload", q, struct{}{}, nil, false)
}

func (c *Client) CancelSnapshot(ctx context.Context, src *SourceInfo) error {
	var q url.Values
	if src != nil {
		q = sourceQuery(*src)
	}
	return c.doJSON(ctx, http.MethodPost, "/api/v1/sources/cancel", q, struct{}{}, nil, false)
}

func (c *Client) PauseSource(ctx context.Context, src *SourceInfo) error {
	var q url.Values
	if src != nil {
		q = sourceQuery(*src)
	}
	return c.doJSON(ctx, http.MethodPost, "/api/v1/control/pause-source", q, struct{}{}, nil, true)
}

func (c *Client) ResumeSource(ctx context.Context, src *SourceInfo) error {
	var q url.Values
	if src != nil {
		q = sourceQuery(*src)
	}
	return c.doJSON(ctx, http.MethodPost, "/api/v1/control/resume-source", q, struct{}{}, nil, true)
}

func (c *Client) Estimate(ctx context.Context, rootPath string, polOverride *Policy) (*TaskInfo, error) {
	var out TaskInfo
	body := map[string]any{
		"root":                 rootPath,
		"maxExamplesPerBucket": 10,
		"policyOverride":       polOverride,
	}
	if err := c.doJSON(ctx, http.MethodPost, "/api/v1/estimate", nil, body, &out, false); err != nil {
		return nil, err
	}
	return &out, nil
}

// --- Snapshots, Objects, Restore & Mounts ---

func (c *Client) ListSnapshots(ctx context.Context, src SourceInfo, includeIdentical bool) (*SnapshotsResponse, error) {
	q := sourceQuery(src)
	if includeIdentical {
		q.Set("all", "1")
	}
	var out SnapshotsResponse
	if err := c.doJSON(ctx, http.MethodGet, "/api/v1/snapshots", q, nil, &out, false); err != nil {
		return nil, err
	}
	out.Snapshots = compactNonNil(out.Snapshots)
	return &out, nil
}

func (c *Client) EditSnapshots(ctx context.Context, req EditSnapshotsRequest) error {
	return c.doJSON(ctx, http.MethodPost, "/api/v1/snapshots/edit", nil, req, nil, false)
}

func (c *Client) DeleteSnapshots(ctx context.Context, req DeleteSnapshotsRequest) error {
	return c.doJSON(ctx, http.MethodPost, "/api/v1/snapshots/delete", nil, req, nil, false)
}

func (c *Client) GetDirectory(ctx context.Context, objectID string) (*DirManifest, error) {
	escID, err := safePathSegment(objectID, "directory object ID")
	if err != nil {
		return nil, err
	}
	var out DirManifest
	if err := c.doJSON(ctx, http.MethodGet, "/api/v1/objects/"+escID, nil, nil, &out, false); err != nil {
		return nil, err
	}
	out.Entries = compactNonNil(out.Entries)
	return &out, nil
}

// DownloadObjectToFile streams any Kopia object directly to targetPath on disk with owner-only permissions (0600).
func (c *Client) DownloadObjectToFile(ctx context.Context, objectID, filename, targetPath string) error {
	escID, err := safePathSegment(objectID, "object ID")
	if err != nil {
		return err
	}
	q := url.Values{}
	if filename != "" {
		q.Set("fname", filename)
	}
	fullURL := c.baseURL + "/api/v1/objects/" + escID
	if len(q) > 0 {
		fullURL += "?" + q.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fullURL, nil)
	if err != nil {
		return err
	}
	if c.password != "" {
		req.SetBasicAuth(c.username, c.password)
	}

	resp, err := c.streamHTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("download failed (HTTP %d): %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	dir := filepath.Dir(targetPath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".mygopia-dl-*.part")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, copyErr := io.Copy(f, resp.Body)
	closeErr := f.Close()
	if copyErr != nil {
		_ = os.Remove(tmp)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(tmp)
		return closeErr
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, targetPath); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func (c *Client) StartRestore(ctx context.Context, req RestoreRequest) (*TaskInfo, error) {
	var out TaskInfo
	if err := c.doJSON(ctx, http.MethodPost, "/api/v1/restore", nil, req, &out, false); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) ListMounts(ctx context.Context) ([]*MountedSnapshot, error) {
	var out MountedSnapshots
	if err := c.doJSON(ctx, http.MethodGet, "/api/v1/mounts", nil, nil, &out, false); err != nil {
		return nil, err
	}
	return compactNonNil(out.Items), nil
}

func (c *Client) MountSnapshot(ctx context.Context, rootOID string) (*MountedSnapshot, error) {
	var out MountedSnapshot
	body := map[string]string{"root": rootOID}
	if err := c.doJSON(ctx, http.MethodPost, "/api/v1/mounts", nil, body, &out, false); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) UnmountSnapshot(ctx context.Context, rootOID string) error {
	escID, err := safePathSegment(rootOID, "mount object ID")
	if err != nil {
		return err
	}
	return c.doJSON(ctx, http.MethodDelete, "/api/v1/mounts/"+escID, nil, nil, nil, false)
}

// --- Policies ---

func (c *Client) ListPolicies(ctx context.Context) ([]*PolicyListEntry, error) {
	var out PoliciesResponse
	if err := c.doJSON(ctx, http.MethodGet, "/api/v1/policies", nil, nil, &out, false); err != nil {
		return nil, err
	}
	return compactNonNil(out.Policies), nil
}

func (c *Client) GetPolicy(ctx context.Context, target SourceInfo) (*Policy, error) {
	var out Policy
	err := c.doJSON(ctx, http.MethodGet, "/api/v1/policy", sourceQuery(target), nil, &out, false)
	if err != nil {
		if apiErr, ok := err.(*APIError); ok && apiErr.Code == "NOT_FOUND" {
			return &Policy{}, nil
		}
		return nil, err
	}
	return &out, nil
}

func (c *Client) SetPolicy(ctx context.Context, target SourceInfo, pol *Policy) error {
	if pol == nil {
		pol = &Policy{}
	}
	return c.doJSON(ctx, http.MethodPut, "/api/v1/policy", sourceQuery(target), pol, nil, false)
}

func (c *Client) DeletePolicy(ctx context.Context, target SourceInfo) error {
	return c.doJSON(ctx, http.MethodDelete, "/api/v1/policy", sourceQuery(target), nil, nil, false)
}

func (c *Client) ResolvePolicy(ctx context.Context, target SourceInfo, updates *Policy, upcomingCount int) (*ResolvePolicyResponse, error) {
	if upcomingCount <= 0 {
		upcomingCount = 5
	}
	var out ResolvePolicyResponse
	req := ResolvePolicyRequest{
		Updates:                  updates,
		NumUpcomingSnapshotTimes: upcomingCount,
	}
	if err := c.doJSON(ctx, http.MethodPost, "/api/v1/policy/resolve", sourceQuery(target), req, &out, false); err != nil {
		return nil, err
	}
	return &out, nil
}

// --- Tasks & Notifications ---

func (c *Client) ListTasks(ctx context.Context) ([]TaskInfo, error) {
	var out TaskListResponse
	if err := c.doJSON(ctx, http.MethodGet, "/api/v1/tasks", nil, nil, &out, false); err != nil {
		return nil, err
	}
	return out.Tasks, nil
}

func (c *Client) GetTask(ctx context.Context, taskID string) (*TaskInfo, error) {
	escID, err := safePathSegment(taskID, "task ID")
	if err != nil {
		return nil, err
	}
	var out TaskInfo
	if err := c.doJSON(ctx, http.MethodGet, "/api/v1/tasks/"+escID, nil, nil, &out, false); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) GetTaskLogs(ctx context.Context, taskID string) ([]TaskLogEntry, error) {
	escID, err := safePathSegment(taskID, "task ID")
	if err != nil {
		return nil, err
	}
	var out TaskLogResponse
	if err := c.doJSON(ctx, http.MethodGet, "/api/v1/tasks/"+escID+"/logs", nil, nil, &out, false); err != nil {
		return nil, err
	}
	return out.Logs, nil
}

func (c *Client) CancelTask(ctx context.Context, taskID string) error {
	escID, err := safePathSegment(taskID, "task ID")
	if err != nil {
		return err
	}
	return c.doJSON(ctx, http.MethodPost, "/api/v1/tasks/"+escID+"/cancel", nil, struct{}{}, nil, false)
}

func (c *Client) TaskSummary(ctx context.Context) (TaskSummary, error) {
	var out TaskSummary
	if err := c.doJSON(ctx, http.MethodGet, "/api/v1/tasks-summary", nil, nil, &out, false); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *Client) ListNotificationProfiles(ctx context.Context) ([]NotificationProfile, error) {
	var out []NotificationProfile
	if err := c.doJSON(ctx, http.MethodGet, "/api/v1/notificationProfiles", nil, nil, &out, false); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *Client) SaveNotificationProfile(ctx context.Context, profile NotificationProfile) error {
	return c.doJSON(ctx, http.MethodPost, "/api/v1/notificationProfiles", nil, profile, nil, false)
}

func (c *Client) DeleteNotificationProfile(ctx context.Context, profileName string) error {
	escName, err := safePathSegment(profileName, "notification profile name")
	if err != nil {
		return err
	}
	return c.doJSON(ctx, http.MethodDelete, "/api/v1/notificationProfiles/"+escName, nil, nil, nil, false)
}

func (c *Client) TestNotificationProfile(ctx context.Context, profile NotificationProfile) error {
	return c.doJSON(ctx, http.MethodPost, "/api/v1/testNotificationProfile", nil, profile, nil, false)
}
