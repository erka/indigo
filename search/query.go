package search

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	es "github.com/opensearch-project/opensearch-go/v2"
)

type EsSearchHit struct {
	Index  string          `json:"_index"`
	ID     string          `json:"_id"`
	Score  float64         `json:"_score"`
	Source json.RawMessage `json:"_source"`
}

type EsSearchHits struct {
	Total struct { // not used
		Value    int
		Relation string
	} `json:"total"`
	MaxScore float64       `json:"max_score"`
	Hits     []EsSearchHit `json:"hits"`
}

type EsSearchResponse struct {
	Took     int  `json:"took"`
	TimedOut bool `json:"timed_out"`
	// Shards ???
	Hits EsSearchHits `json:"hits"`
}

type UserResult struct {
	Did    string `json:"did"`
	Handle string `json:"handle"`
}

type PostSearchResult struct {
	Tid  string     `json:"tid"`
	Cid  string     `json:"cid"`
	User UserResult `json:"user"`
	Post any        `json:"post"`
}

func checkParams(offset, size int) error {
	if offset+size > 5000 || size > 1000 || offset > 1000 || offset < 0 || size < 0 {
		return fmt.Errorf("disallowed size/offset parameters")
	}
	return nil
}

func DoSearchPosts(ctx context.Context, escli *es.Client, index, q string, offset, size int) (*EsSearchResponse, error) {
	if err := checkParams(offset, size); err != nil {
		return nil, err
	}
	query := map[string]interface{}{
		// TODO: filter to not show any created_at in the future
		"sort": map[string]any{
			"created_at": map[string]any{
				"order": "desc",
			},
		},
		"query": map[string]interface{}{
			"match": map[string]interface{}{
				"everything": map[string]interface{}{
					"query": q,
				},
			},
		},
		"size": size,
		"from": offset,
	}

	return doSearch(ctx, escli, index, query)
}

func DoSearchProfiles(ctx context.Context, escli *es.Client, index, q string, offset, size int) (*EsSearchResponse, error) {
	if err := checkParams(offset, size); err != nil {
		return nil, err
	}
	basic := map[string]interface{}{
		"match": map[string]interface{}{
			"everything": map[string]interface{}{
				"query": q,
			},
		},
	}
	query := map[string]interface{}{
		"query": map[string]interface{}{
			"bool": map[string]interface{}{
				"must": basic,
				"should": []interface{}{
					map[string]interface{}{"term": map[string]interface{}{"has_avatar": true}},
					map[string]interface{}{"term": map[string]interface{}{"has_banner": true}},
				},
				"boost": 1.0,
			},
		},
		"size": size,
		"from": offset,
	}

	return doSearch(ctx, escli, index, query)
}

func DoSearchProfilesTypeahead(ctx context.Context, escli *es.Client, index, q string) (*EsSearchResponse, error) {
	query := map[string]interface{}{
		"query": map[string]interface{}{
			"multi_match": map[string]interface{}{
				"query": q,
				"type":  "bool_prefix",
				"fields": []string{
					"typeahead",
					"typeahead._2gram",
					"typeahead._3gram",
				},
			},
		},
		"size": 30,
	}

	return doSearch(ctx, escli, index, query)
}

// helper to do a full-featured Lucene query parser (query_string) search, with all possible facets. Not safe to expose publicly.
func DoSearchGeneric(ctx context.Context, escli *es.Client, index, q string) (*EsSearchResponse, error) {
	query := map[string]interface{}{
		"query": map[string]interface{}{
			"query_string": map[string]interface{}{
				"query":                  q,
				"default_operator":       "and",
				"analyze_wildcard":       true,
				"allow_leading_wildcard": false,
				"lenient":                true,
				"default_field":          "everything",
			},
		},
	}

	return doSearch(ctx, escli, index, query)
}

func doSearch(ctx context.Context, escli *es.Client, index string, query interface{}) (*EsSearchResponse, error) {
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(query); err != nil {
		log.Fatalf("Error encoding query: %s", err)
	}
	slog.Warn("sending query", "index", index, "query", query)

	// Perform the search request.
	res, err := escli.Search(
		escli.Search.WithContext(ctx),
		escli.Search.WithIndex(index),
		escli.Search.WithBody(&buf),
		escli.Search.WithTrackTotalHits(false), // expensive to track total hits
	)
	if err != nil {
		log.Fatalf("Error getting response: %s", err)
	}
	defer res.Body.Close()

	var out EsSearchResponse
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decoding search response: %w", err)
	}

	return &out, nil
}
