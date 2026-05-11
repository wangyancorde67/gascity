package sessionlog

import (
	"bufio"
	"crypto/md5" //nolint:gosec // Kimi uses MD5 only as a workdir storage key.
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ReadKimiFile reads a Kimi Code context JSONL transcript and converts it to
// the standard Session format used by gc session logs.
func ReadKimiFile(path string, tailCompactions int) (*Session, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close() //nolint:errcheck // read-only file

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 256*1024), 50*1024*1024)

	var messages []*Entry
	var diagnostics SessionDiagnostics
	var lastNonEmptyLineMalformed bool
	var lastUUID string
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var raw kimiContextEntry
		if err := json.Unmarshal(line, &raw); err != nil {
			diagnostics.MalformedLineCount++
			lastNonEmptyLineMalformed = true
			continue
		}
		lastNonEmptyLineMalformed = false
		entry := convertKimiContextEntry(raw, line, len(messages), kimiSessionID(path))
		if entry == nil {
			continue
		}
		entry.ParentUUID = lastUUID
		lastUUID = entry.UUID
		messages = append(messages, entry)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scanning kimi session file: %w", err)
	}
	diagnostics.MalformedTail = lastNonEmptyLineMalformed

	sess := &Session{
		ID:          kimiSessionID(path),
		Messages:    messages,
		Diagnostics: diagnostics,
	}
	if tailCompactions > 0 {
		paginated, info := sliceAtCompactBoundaries(messages, tailCompactions, "", "")
		sess.Messages = paginated
		sess.Pagination = info
	}
	return sess, nil
}

// FindKimiSessionFile searches Kimi's session directory
// (~/.kimi/sessions/<work-dir-md5>/<session-id>/context.jsonl) for the most
// recently modified session matching workDir.
func FindKimiSessionFile(searchPaths []string, workDir string) string {
	workHash := kimiWorkDirHash(workDir)
	if workHash == "" {
		return ""
	}

	var (
		bestPath string
		bestTime time.Time
	)
	for _, root := range mergeKimiSearchPaths(searchPaths) {
		path := findKimiSessionFileIn(root, workHash)
		if path == "" {
			continue
		}
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		if bestPath == "" || info.ModTime().After(bestTime) {
			bestPath = path
			bestTime = info.ModTime()
		}
	}
	return bestPath
}

func findKimiSessionFileIn(root, workHash string) string {
	workRoot := filepath.Join(root, workHash)
	entries, err := os.ReadDir(workRoot)
	if err != nil {
		return ""
	}

	type candidate struct {
		path    string
		modTime time.Time
	}
	var files []candidate
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(workRoot, entry.Name(), "context.jsonl")
		info, err := os.Stat(path)
		if err != nil || info.IsDir() {
			continue
		}
		files = append(files, candidate{path: path, modTime: info.ModTime()})
	}

	sort.Slice(files, func(i, j int) bool {
		return files[i].modTime.After(files[j].modTime)
	})
	if len(files) == 0 {
		return ""
	}
	return files[0].path
}

func convertKimiContextEntry(raw kimiContextEntry, rawLine []byte, idx int, sessionID string) *Entry {
	role := strings.ToLower(strings.TrimSpace(raw.Role))
	switch role {
	case "user", "assistant", "system":
	default:
		return nil
	}

	content := kimiMessageContent(raw.Content)
	entryType := role
	return &Entry{
		UUID:      fmt.Sprintf("kimi-%d", idx),
		Type:      entryType,
		SessionID: sessionID,
		Message: mustMarshal(MessageContent{
			Role:    role,
			Content: content,
		}),
		Raw: append(json.RawMessage(nil), rawLine...),
	}
}

func kimiMessageContent(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return mustMarshal("")
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return mustMarshal(text)
	}
	var blocks []ContentBlock
	if err := json.Unmarshal(raw, &blocks); err == nil {
		return mustMarshal(blocks)
	}
	return mustMarshal(strings.TrimSpace(string(raw)))
}

func kimiSessionID(path string) string {
	dir := filepath.Base(filepath.Dir(path))
	if strings.TrimSpace(dir) != "" && dir != "." {
		return dir
	}
	base := filepath.Base(path)
	return strings.TrimSuffix(base, filepath.Ext(base))
}

func kimiWorkDirHash(workDir string) string {
	workDir = strings.TrimSpace(workDir)
	if workDir == "" {
		return ""
	}
	sum := md5.Sum([]byte(filepath.Clean(workDir)))
	return hex.EncodeToString(sum[:])
}

func mergeKimiSearchPaths(searchPaths []string) []string {
	var candidates []string
	for _, path := range searchPaths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		candidates = append(candidates, path)
		if filepath.Base(filepath.Clean(path)) != "sessions" {
			candidates = append(candidates, filepath.Join(path, "sessions"))
		}
	}
	return mergePaths(DefaultKimiSearchPaths(), candidates)
}

type kimiContextEntry struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}
