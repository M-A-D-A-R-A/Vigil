package project

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"vigil/internal/store/sqlite"
)

type Project struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Status    string `json:"status"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

type CreateResult struct {
	Project   Project `json:"project"`
	IngestKey string  `json:"ingest_key"`
}

type BrowserKey struct {
	ID             string   `json:"id"`
	ProjectID      string   `json:"project_id"`
	Name           string   `json:"name"`
	Status         string   `json:"status"`
	AllowedOrigins []string `json:"allowed_origins"`
	CreatedAt      string   `json:"created_at"`
	UpdatedAt      string   `json:"updated_at"`
}

type BrowserKeyCreateResult struct {
	Key       BrowserKey `json:"key"`
	Plaintext string     `json:"browser_ingest_key"`
}

type BrowserKeyAuthResult struct {
	Project Project
	Key     BrowserKey
}

type Service struct {
	store          *sqlite.Store
	browserCacheMu sync.RWMutex
	browserCache   map[string]BrowserKeyAuthResult
}

func NewService(store *sqlite.Store) *Service {
	return &Service{
		store:        store,
		browserCache: map[string]BrowserKeyAuthResult{},
	}
}

func (s *Service) CreateProject(name string) (*CreateResult, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("name is required")
	}

	projectID, err := randomID("proj_")
	if err != nil {
		return nil, err
	}
	key, err := randomID("vigil_")
	if err != nil {
		return nil, err
	}

	record, err := s.store.CreateProject(projectID, name, hashKey(key), time.Now().UTC())
	if err != nil {
		return nil, err
	}

	return &CreateResult{
		Project:   fromRecord(record),
		IngestKey: key,
	}, nil
}

func (s *Service) ListProjects() ([]Project, error) {
	records, err := s.store.ListProjects()
	if err != nil {
		return nil, err
	}

	projects := make([]Project, 0, len(records))
	for _, record := range records {
		projects = append(projects, fromRecord(record))
	}

	return projects, nil
}

func (s *Service) RegenerateKey(projectID string) (*CreateResult, error) {
	key, err := randomID("vigil_")
	if err != nil {
		return nil, err
	}

	record, err := s.store.UpdateProjectKey(strings.TrimSpace(projectID), hashKey(key), time.Now().UTC())
	if err != nil {
		return nil, err
	}

	return &CreateResult{
		Project:   fromRecord(record),
		IngestKey: key,
	}, nil
}

func (s *Service) CreateBrowserKey(projectID, name string, allowedOrigins []string) (*BrowserKeyCreateResult, error) {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return nil, fmt.Errorf("project_id is required")
	}
	if _, err := s.store.GetProjectByID(projectID); err != nil {
		return nil, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = "browser"
	}
	origins, err := normalizeAllowedOrigins(allowedOrigins)
	if err != nil {
		return nil, err
	}

	keyID, err := randomID("bkey_")
	if err != nil {
		return nil, err
	}
	key, err := randomID("vigil_browser_")
	if err != nil {
		return nil, err
	}
	keyHash, err := s.hashBrowserKey(key)
	if err != nil {
		return nil, err
	}

	record, err := s.store.CreateBrowserKey(keyID, projectID, name, keyHash, origins, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	s.clearBrowserCache()
	return &BrowserKeyCreateResult{
		Key:       browserKeyFromRecord(record),
		Plaintext: key,
	}, nil
}

func (s *Service) ListBrowserKeys(projectID string) ([]BrowserKey, error) {
	records, err := s.store.ListBrowserKeys(strings.TrimSpace(projectID))
	if err != nil {
		return nil, err
	}
	keys := make([]BrowserKey, 0, len(records))
	for _, record := range records {
		keys = append(keys, browserKeyFromRecord(record))
	}
	return keys, nil
}

func (s *Service) RotateBrowserKey(projectID, keyID string) (*BrowserKeyCreateResult, error) {
	key, err := randomID("vigil_browser_")
	if err != nil {
		return nil, err
	}
	keyHash, err := s.hashBrowserKey(key)
	if err != nil {
		return nil, err
	}

	existing, err := s.store.ListBrowserKeys(strings.TrimSpace(projectID))
	if err != nil {
		return nil, err
	}
	var origins []string
	for _, record := range existing {
		if record.ID == strings.TrimSpace(keyID) {
			origins = record.AllowedOrigins
			break
		}
	}
	if origins == nil {
		return nil, fmt.Errorf("browser key not found")
	}

	record, err := s.store.UpdateBrowserKey(strings.TrimSpace(projectID), strings.TrimSpace(keyID), keyHash, origins, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	s.clearBrowserCache()
	return &BrowserKeyCreateResult{
		Key:       browserKeyFromRecord(record),
		Plaintext: key,
	}, nil
}

func (s *Service) RevokeBrowserKey(projectID, keyID string) (*BrowserKey, error) {
	record, err := s.store.RevokeBrowserKey(strings.TrimSpace(projectID), strings.TrimSpace(keyID), time.Now().UTC())
	if err != nil {
		return nil, err
	}
	s.clearBrowserCache()
	key := browserKeyFromRecord(record)
	return &key, nil
}

func (s *Service) AuthenticateBearerToken(token string) (*Project, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, fmt.Errorf("bearer token is required")
	}

	record, err := s.store.GetProjectByKeyHash(hashKey(token))
	if err != nil {
		return nil, err
	}

	project := fromRecord(*record)
	return &project, nil
}

func (s *Service) AuthenticateBrowserToken(token, origin string) (*BrowserKeyAuthResult, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, fmt.Errorf("browser ingest key is required")
	}
	origin = strings.TrimSpace(origin)
	if origin == "" {
		return nil, fmt.Errorf("origin header is required")
	}
	normalizedOrigin, err := normalizeOrigin(origin)
	if err != nil {
		return nil, err
	}
	keyHash, err := s.hashBrowserKey(token)
	if err != nil {
		return nil, err
	}
	if cached, ok := s.getCachedBrowserAuth(keyHash); ok {
		if cached.Key.Status != "active" {
			return nil, fmt.Errorf("browser ingest key is not active")
		}
		if !originAllowed(normalizedOrigin, cached.Key.AllowedOrigins) {
			return nil, fmt.Errorf("origin is not allowed for browser ingest key")
		}
		result := cached
		return &result, nil
	}

	record, err := s.store.GetBrowserKeyByHash(keyHash)
	if err != nil {
		return nil, err
	}
	if record.Status != "active" {
		return nil, fmt.Errorf("browser ingest key is not active")
	}
	if !originAllowed(normalizedOrigin, record.AllowedOrigins) {
		return nil, fmt.Errorf("origin is not allowed for browser ingest key")
	}

	projectRecord, err := s.store.GetProjectByID(record.ProjectID)
	if err != nil {
		return nil, err
	}
	result := BrowserKeyAuthResult{
		Project: fromRecord(*projectRecord),
		Key:     browserKeyFromRecord(*record),
	}
	s.setCachedBrowserAuth(keyHash, result)
	return &result, nil
}

func (s *Service) HasAllowedBrowserOrigin(origin string) (bool, error) {
	normalizedOrigin, err := normalizeOrigin(origin)
	if err != nil {
		return false, err
	}
	return s.store.HasActiveBrowserKeyForOrigin(normalizedOrigin)
}

func fromRecord(record sqlite.ProjectRecord) Project {
	return Project{
		ID:        record.ID,
		Name:      record.Name,
		Status:    record.Status,
		CreatedAt: record.CreatedAt,
		UpdatedAt: record.UpdatedAt,
	}
}

func browserKeyFromRecord(record sqlite.BrowserKeyRecord) BrowserKey {
	return BrowserKey{
		ID:             record.ID,
		ProjectID:      record.ProjectID,
		Name:           record.Name,
		Status:         record.Status,
		AllowedOrigins: append([]string(nil), record.AllowedOrigins...),
		CreatedAt:      record.CreatedAt,
		UpdatedAt:      record.UpdatedAt,
	}
}

func hashKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

func (s *Service) hashBrowserKey(key string) (string, error) {
	seed, err := s.store.KeyHashSeed()
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, []byte(seed))
	_, _ = mac.Write([]byte(key))
	return hex.EncodeToString(mac.Sum(nil)), nil
}

func (s *Service) getCachedBrowserAuth(keyHash string) (BrowserKeyAuthResult, bool) {
	s.browserCacheMu.RLock()
	defer s.browserCacheMu.RUnlock()
	result, ok := s.browserCache[keyHash]
	return result, ok
}

func (s *Service) setCachedBrowserAuth(keyHash string, result BrowserKeyAuthResult) {
	s.browserCacheMu.Lock()
	defer s.browserCacheMu.Unlock()
	s.browserCache[keyHash] = result
}

func (s *Service) clearBrowserCache() {
	s.browserCacheMu.Lock()
	defer s.browserCacheMu.Unlock()
	s.browserCache = map[string]BrowserKeyAuthResult{}
}

func normalizeAllowedOrigins(raw []string) ([]string, error) {
	seen := map[string]struct{}{}
	origins := make([]string, 0, len(raw))
	for _, candidate := range raw {
		origin, err := normalizeOrigin(candidate)
		if err != nil {
			return nil, err
		}
		if _, ok := seen[origin]; ok {
			continue
		}
		seen[origin] = struct{}{}
		origins = append(origins, origin)
	}
	if len(origins) == 0 {
		return nil, fmt.Errorf("allowed_origins is required")
	}
	return origins, nil
}

func normalizeOrigin(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("origin is required")
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("origin must be an absolute origin")
	}
	if parsed.Path != "" && parsed.Path != "/" {
		return "", fmt.Errorf("origin must not include a path")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("origin must not include query or fragment")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("origin scheme must be http or https")
	}
	return parsed.Scheme + "://" + strings.ToLower(parsed.Host), nil
}

func originAllowed(origin string, allowedOrigins []string) bool {
	for _, allowed := range allowedOrigins {
		if origin == allowed {
			return true
		}
	}
	return false
}

func randomID(prefix string) (string, error) {
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(buf), nil
}
