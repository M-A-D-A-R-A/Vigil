package raw

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"vigil/internal/event"
)

const defaultSegmentSize = 10 * 1024 * 1024

type Store struct {
	baseDir      string
	segmentBytes int64
}

type PruneSummary struct {
	DeletedDayDirs int    `json:"deleted_day_dirs"`
	DeletedFiles   int    `json:"deleted_files"`
	DeletedBytes   int64  `json:"deleted_bytes"`
	CutoffDay      string `json:"cutoff_day"`
}

func NewStore(baseDir string, segmentBytes int64) *Store {
	if segmentBytes <= 0 {
		segmentBytes = defaultSegmentSize
	}
	return &Store{baseDir: baseDir, segmentBytes: segmentBytes}
}

func (s *Store) Append(ev *event.StoredEvent) (string, error) {
	day := event.TimestampDay(ev.TS)
	dir := filepath.Join(s.baseDir, ev.ProjectID, day)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create raw storage dir: %w", err)
	}

	line, err := json.Marshal(ev)
	if err != nil {
		return "", fmt.Errorf("marshal raw event: %w", err)
	}
	line = append(line, '\n')

	path, err := s.nextSegmentPath(dir, int64(len(line)))
	if err != nil {
		return "", err
	}

	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return "", fmt.Errorf("open raw segment: %w", err)
	}
	defer file.Close()

	if _, err := file.Write(line); err != nil {
		return "", fmt.Errorf("append raw event: %w", err)
	}

	return path, nil
}

func (s *Store) Replay(ctx context.Context, fn func(*event.StoredEvent) error) error {
	files, err := s.segmentFiles()
	if err != nil {
		return err
	}

	for _, path := range files {
		if err := ctx.Err(); err != nil {
			return err
		}

		file, err := os.Open(path)
		if err != nil {
			return fmt.Errorf("open replay file %s: %w", path, err)
		}

		scanner := bufio.NewScanner(file)
		scanner.Buffer(make([]byte, 64*1024), int(s.segmentBytes))

		for scanner.Scan() {
			if err := ctx.Err(); err != nil {
				file.Close()
				return err
			}

			var ev event.StoredEvent
			if err := json.Unmarshal(scanner.Bytes(), &ev); err != nil {
				file.Close()
				return fmt.Errorf("decode replay event from %s: %w", path, err)
			}
			if err := fn(&ev); err != nil {
				file.Close()
				return err
			}
		}

		if err := scanner.Err(); err != nil {
			file.Close()
			return fmt.Errorf("scan replay file %s: %w", path, err)
		}

		file.Close()
	}

	return nil
}

func (s *Store) PruneBeforeDay(cutoffDay string, dryRun bool) (PruneSummary, error) {
	summary := PruneSummary{CutoffDay: cutoffDay}
	projects, err := os.ReadDir(s.baseDir)
	if err != nil {
		if os.IsNotExist(err) {
			return summary, nil
		}
		return summary, fmt.Errorf("read raw store base dir: %w", err)
	}

	for _, projectEntry := range projects {
		if !projectEntry.IsDir() {
			continue
		}

		projectDir := filepath.Join(s.baseDir, projectEntry.Name())
		dayEntries, err := os.ReadDir(projectDir)
		if err != nil {
			return summary, fmt.Errorf("read project raw dir %s: %w", projectDir, err)
		}

		for _, dayEntry := range dayEntries {
			if !dayEntry.IsDir() {
				continue
			}

			day := dayEntry.Name()
			if !isBeforeCutoffDay(day, cutoffDay) {
				continue
			}

			dayDir := filepath.Join(projectDir, day)
			files, bytes, err := dirStats(dayDir)
			if err != nil {
				return summary, fmt.Errorf("inspect retained day dir %s: %w", dayDir, err)
			}

			summary.DeletedDayDirs++
			summary.DeletedFiles += files
			summary.DeletedBytes += bytes

			if dryRun {
				continue
			}

			if err := os.RemoveAll(dayDir); err != nil {
				return summary, fmt.Errorf("remove retained day dir %s: %w", dayDir, err)
			}
		}
	}

	return summary, nil
}

func (s *Store) nextSegmentPath(dir string, incomingBytes int64) (string, error) {
	files, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("read segment dir: %w", err)
	}

	type candidate struct {
		path string
		size int64
		n    int
	}

	var latest candidate
	for _, entry := range files {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".ndjson") {
			continue
		}
		number, err := strconv.Atoi(strings.TrimSuffix(entry.Name(), ".ndjson"))
		if err != nil {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return "", err
		}
		if number > latest.n {
			latest = candidate{
				path: filepath.Join(dir, entry.Name()),
				size: info.Size(),
				n:    number,
			}
		}
	}

	if latest.n == 0 {
		return filepath.Join(dir, "0001.ndjson"), nil
	}
	if latest.size+incomingBytes <= s.segmentBytes {
		return latest.path, nil
	}
	return filepath.Join(dir, fmt.Sprintf("%04d.ndjson", latest.n+1)), nil
}

func (s *Store) segmentFiles() ([]string, error) {
	paths := []string{}
	err := filepath.WalkDir(s.baseDir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if strings.HasSuffix(entry.Name(), ".ndjson") {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("walk raw storage: %w", err)
	}

	sort.Strings(paths)
	return paths, nil
}

func isBeforeCutoffDay(day string, cutoffDay string) bool {
	parsedDay, err := time.Parse("2006-01-02", day)
	if err != nil {
		return false
	}
	parsedCutoff, err := time.Parse("2006-01-02", cutoffDay)
	if err != nil {
		return false
	}
	return parsedDay.Before(parsedCutoff)
}

func dirStats(dir string) (files int, bytes int64, err error) {
	err = filepath.WalkDir(dir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			return infoErr
		}
		files++
		bytes += info.Size()
		return nil
	})
	return files, bytes, err
}
