package project

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
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

type Service struct {
	store *sqlite.Store
}

func NewService(store *sqlite.Store) *Service {
	return &Service{store: store}
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

func fromRecord(record sqlite.ProjectRecord) Project {
	return Project{
		ID:        record.ID,
		Name:      record.Name,
		Status:    record.Status,
		CreatedAt: record.CreatedAt,
		UpdatedAt: record.UpdatedAt,
	}
}

func hashKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

func randomID(prefix string) (string, error) {
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(buf), nil
}
