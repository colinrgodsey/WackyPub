package agent

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	ScratchpadFileName        = "scratchpad.json"
	MaxScratchpadEntries      = 50
	MaxExpandedArgBytes       = 500000
	ScratchpadOutputThreshold = 4000
)

type ScratchpadEntry struct {
	ID        string    `json:"id"`
	Seq       int64     `json:"seq"`
	Size      int       `json:"size"`
	CreatedBy string    `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
	Text      string    `json:"text"`
}

type ScratchpadItem struct {
	ID        string    `json:"id"`
	Seq       int64     `json:"seq"`
	Size      int       `json:"size"`
	CreatedBy string    `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
}

type ScratchpadStore struct {
	Entries map[string]*ScratchpadEntry `json:"entries"`
}

var (
	scratchpadMacroRegex = regexp.MustCompile(`<SCRATCHPAD_DATA\s+([^>]+)\s*/?>`)
	macroIDRegex         = regexp.MustCompile(`id="([^"]+)"`)
	macroSkipLinesRegex  = regexp.MustCompile(`skip_lines="(\d+)"`)
	macroNumLinesRegex   = regexp.MustCompile(`num_lines="(\d+)"`)

	scratchpadMutexes  sync.Map
	globalScratchpadMu sync.Mutex
)

func getScratchpadMutex(agentDir string) *sync.Mutex {
	absDir, err := filepath.Abs(agentDir)
	if err != nil {
		absDir = agentDir
	}
	if m, ok := scratchpadMutexes.Load(absDir); ok {
		return m.(*sync.Mutex)
	}
	globalScratchpadMu.Lock()
	defer globalScratchpadMu.Unlock()
	if m, ok := scratchpadMutexes.Load(absDir); ok {
		return m.(*sync.Mutex)
	}
	m := &sync.Mutex{}
	scratchpadMutexes.Store(absDir, m)
	return m
}

func generateScratchpadID(liveEntries map[string]*ScratchpadEntry) string {
	const charset = "0123456789abcdefghijklmnopqrstuvwxyz"
	for {
		var result strings.Builder
		for i := 0; i < 4; i++ {
			n, err := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
			if err != nil {
				result.WriteByte(charset[time.Now().UnixNano()%int64(len(charset))])
				continue
			}
			result.WriteByte(charset[n.Int64()])
		}
		id := result.String()
		if _, exists := liveEntries[id]; !exists {
			return id
		}
	}
}

// ReadScratchpadStore reads <agentDir>/scratchpad.json. If absent, returns an empty store.
func ReadScratchpadStore(agentDir string) (*ScratchpadStore, error) {
	spPath := filepath.Join(agentDir, ScratchpadFileName)
	data, err := os.ReadFile(spPath)
	if err != nil {
		if os.IsNotExist(err) {
			return &ScratchpadStore{Entries: make(map[string]*ScratchpadEntry)}, nil
		}
		return nil, fmt.Errorf("failed to read scratchpad at %s: %w", spPath, err)
	}

	var store ScratchpadStore
	if err := json.Unmarshal(data, &store); err != nil {
		return nil, fmt.Errorf("failed to parse scratchpad JSON: %w", err)
	}
	if store.Entries == nil {
		store.Entries = make(map[string]*ScratchpadEntry)
	}
	return &store, nil
}

// WriteScratchpadStore persists <agentDir>/scratchpad.json atomically via temporary file replacement.
func WriteScratchpadStore(agentDir string, store *ScratchpadStore) error {
	if err := os.MkdirAll(agentDir, 0755); err != nil {
		return fmt.Errorf("failed to create agent directory: %w", err)
	}

	data, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal scratchpad JSON: %w", err)
	}

	spPath := filepath.Join(agentDir, ScratchpadFileName)
	tmpPath := spPath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write scratchpad tmp file: %w", err)
	}

	return os.Rename(tmpPath, spPath)
}

// CreateScratchpad creates a new scratchpad entry with a random 4-character ID according to D18.
// Thread-safe for concurrent goroutines within the same process.
// Automatically evicts the entry with the lowest seq when live entries exceed cap (50).
func CreateScratchpad(agentDir string, text string, createdBy string) (*ScratchpadEntry, error) {
	mu := getScratchpadMutex(agentDir)
	mu.Lock()
	defer mu.Unlock()

	store, err := ReadScratchpadStore(agentDir)
	if err != nil {
		return nil, err
	}

	var maxSeq int64 = 0
	for _, entry := range store.Entries {
		if entry.Seq > maxSeq {
			maxSeq = entry.Seq
		}
	}

	id := generateScratchpadID(store.Entries)
	if createdBy == "" {
		createdBy = "create_scratchpad"
	}

	entry := &ScratchpadEntry{
		ID:        id,
		Seq:       maxSeq + 1,
		Size:      len(text),
		CreatedBy: createdBy,
		CreatedAt: time.Now().UTC(),
		Text:      text,
	}
	store.Entries[id] = entry

	// Evict lowest seq entry if cap (50) exceeded
	if len(store.Entries) > MaxScratchpadEntries {
		var lowestSeq int64 = -1
		var lowestID string
		for eID, e := range store.Entries {
			if lowestSeq == -1 || e.Seq < lowestSeq {
				lowestSeq = e.Seq
				lowestID = eID
			}
		}
		if lowestID != "" {
			delete(store.Entries, lowestID)
		}
	}

	if err := WriteScratchpadStore(agentDir, store); err != nil {
		return nil, err
	}
	return entry, nil
}

// GetScratchpad retrieves stored text by entry ID, optionally paginated by line range.
// Thread-safe for concurrent goroutines within the same process.
func GetScratchpad(agentDir string, id string, skipLines *int, numLines *int) (string, error) {
	mu := getScratchpadMutex(agentDir)
	mu.Lock()
	defer mu.Unlock()

	store, err := ReadScratchpadStore(agentDir)
	if err != nil {
		return "", err
	}

	entry, exists := store.Entries[id]
	if !exists {
		return "", fmt.Errorf("scratchpad entry %q not found", id)
	}

	lines := strings.Split(entry.Text, "\n")
	start := 0
	if skipLines != nil && *skipLines > 0 {
		start = *skipLines
		if start > len(lines) {
			start = len(lines)
		}
	}

	end := len(lines)
	if numLines != nil && *numLines >= 0 {
		end = start + *numLines
		if end > len(lines) {
			end = len(lines)
		}
	}

	if start >= len(lines) {
		return "", nil
	}

	return strings.Join(lines[start:end], "\n"), nil
}

// ListScratchpads returns metadata items for all live entries in store ordered by seq ascending.
// Thread-safe for concurrent goroutines within the same process.
func ListScratchpads(agentDir string) ([]ScratchpadItem, int, int, error) {
	mu := getScratchpadMutex(agentDir)
	mu.Lock()
	defer mu.Unlock()

	store, err := ReadScratchpadStore(agentDir)
	if err != nil {
		return nil, 0, MaxScratchpadEntries, err
	}

	var items []ScratchpadItem
	for _, entry := range store.Entries {
		items = append(items, ScratchpadItem{
			ID:        entry.ID,
			Seq:       entry.Seq,
			Size:      entry.Size,
			CreatedBy: entry.CreatedBy,
			CreatedAt: entry.CreatedAt,
		})
	}

	sort.Slice(items, func(i, j int) bool {
		return items[i].Seq < items[j].Seq
	})

	return items, len(items), MaxScratchpadEntries, nil
}

// ExpandScratchpadMacros replaces any inline <SCRATCHPAD_DATA id="X" skip_lines="N" num_lines="M" /> macros
// in text with the corresponding scratchpad text content according to D18.
func ExpandScratchpadMacros(agentDir string, text string) (string, error) {
	if !strings.Contains(text, "<SCRATCHPAD_DATA") {
		return text, nil
	}

	var firstErr error
	expanded := scratchpadMacroRegex.ReplaceAllStringFunc(text, func(match string) string {
		if firstErr != nil {
			return match
		}

		idMatch := macroIDRegex.FindStringSubmatch(match)
		if len(idMatch) < 2 {
			firstErr = fmt.Errorf("invalid <SCRATCHPAD_DATA /> macro syntax: missing id attribute")
			return match
		}
		id := idMatch[1]

		var skipLines *int
		if skipMatch := macroSkipLinesRegex.FindStringSubmatch(match); len(skipMatch) >= 2 {
			val, _ := strconv.Atoi(skipMatch[1])
			skipLines = &val
		}

		var numLines *int
		if numMatch := macroNumLinesRegex.FindStringSubmatch(match); len(numMatch) >= 2 {
			val, _ := strconv.Atoi(numMatch[1])
			numLines = &val
		}

		content, err := GetScratchpad(agentDir, id, skipLines, numLines)
		if err != nil {
			firstErr = fmt.Errorf("scratchpad entry %q not found for macro expansion", id)
			return match
		}
		return content
	})

	if firstErr != nil {
		return "", firstErr
	}
	return expanded, nil
}

type ScratchpadMatch struct {
	Line      int    `json:"line"`
	SkipLines int    `json:"skip_lines"`
	Text      string `json:"text"`
}

type SearchScratchpadResult struct {
	ID           string            `json:"id"`
	Query        string            `json:"query"`
	TotalMatches int               `json:"total_matches"`
	MaxResults   int               `json:"max_results"`
	Matches      []ScratchpadMatch `json:"matches"`
}

// SearchScratchpad searches a specific scratchpad entry (by ID) for query according to D25.
// Returns matching lines with precomputed skip_lines for get_scratchpad pagination.
func SearchScratchpad(agentDir string, id string, query string, caseSensitive *bool, useRegex bool, maxResults int) (*SearchScratchpadResult, error) {
	if id == "" {
		return nil, fmt.Errorf("id is required")
	}
	if query == "" {
		return nil, fmt.Errorf("query must not be empty")
	}
	if maxResults <= 0 {
		maxResults = 50
	}
	isCaseSensitive := true
	if caseSensitive != nil {
		isCaseSensitive = *caseSensitive
	}

	mu := getScratchpadMutex(agentDir)
	mu.Lock()
	defer mu.Unlock()

	store, err := ReadScratchpadStore(agentDir)
	if err != nil {
		return nil, err
	}

	entry, exists := store.Entries[id]
	if !exists {
		return nil, fmt.Errorf("scratchpad entry %q not found", id)
	}

	lines := strings.Split(entry.Text, "\n")

	var reg *regexp.Regexp
	if useRegex {
		pattern := query
		if !isCaseSensitive {
			pattern = "(?i)" + pattern
		}
		r, err := regexp.Compile(pattern)
		if err != nil {
			return nil, fmt.Errorf("invalid regex query %q: %w", query, err)
		}
		reg = r
	}

	var matches []ScratchpadMatch
	totalMatches := 0

	queryLower := ""
	if !useRegex && !isCaseSensitive {
		queryLower = strings.ToLower(query)
	}

	for i, lineText := range lines {
		matched := false
		if useRegex {
			matched = reg.MatchString(lineText)
		} else {
			if isCaseSensitive {
				matched = strings.Contains(lineText, query)
			} else {
				matched = strings.Contains(strings.ToLower(lineText), queryLower)
			}
		}

		if matched {
			totalMatches++
			if len(matches) < maxResults {
				truncatedText := lineText
				if len(truncatedText) > 200 {
					truncatedText = truncatedText[:200]
				}
				matches = append(matches, ScratchpadMatch{
					Line:      i + 1,
					SkipLines: i,
					Text:      truncatedText,
				})
			}
		}
	}

	if matches == nil {
		matches = []ScratchpadMatch{}
	}

	return &SearchScratchpadResult{
		ID:           id,
		Query:        query,
		TotalMatches: totalMatches,
		MaxResults:   maxResults,
		Matches:      matches,
	}, nil
}
