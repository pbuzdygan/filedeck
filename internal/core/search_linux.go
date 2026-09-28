package core

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/pbuzdygan/filedeck/internal/storage"
)

// SearchLimits bound every search: large shares return partial results
// (truncated) instead of tying up the server.
var SearchLimits = storage.SearchLimits{MaxScanned: 200000, MaxResults: 500, MaxDepth: 64}

// SearchTimeout bounds the time one search may walk.
const SearchTimeout = 10 * time.Second

// Search finds entries below dir whose name contains query. Requires List.
func (s *Service) Search(ctx context.Context, subject, space, dir, query string) ([]storage.Hit, bool, error) {
	query = strings.TrimSpace(query)
	if query == "" || utf8.RuneCountInString(query) > 100 || !utf8.ValidString(query) || strings.ContainsAny(query, "/\x00") {
		return nil, false, storage.ErrPath
	}
	if !s.allowed(subject, space, List) {
		return nil, false, ErrDenied
	}
	ctx, cancel := context.WithTimeout(ctx, SearchTimeout)
	defer cancel()
	return s.spaces[space].Search(ctx, dir, query, SearchLimits)
}
