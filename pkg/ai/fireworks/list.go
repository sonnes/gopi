package fireworks

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"

	"github.com/sonnes/gopi/pkg/ai"
)

// pageSize is the most models that one page of the list holds. The API
// caps a page at this size.
const pageSize = 200

// maxErrorBody is the most bytes of an error body that an error holds.
const maxErrorBody = 1024

// readyState is the state of a model that the API serves.
const readyState = "READY"

// listResponse is one page of GET /v1/accounts/{account}/models.
type listResponse struct {
	Models        []listedModel `json:"models"`
	NextPageToken string        `json:"nextPageToken"`
}

// listedModel holds the fields of a listed model that gopi reads. Name
// is the resource name, which is also the model ID of the Chat
// Completions API.
type listedModel struct {
	Name               string `json:"name"`
	DisplayName        string `json:"displayName"`
	State              string `json:"state"`
	ContextLength      int    `json:"contextLength"`
	SupportsImageInput bool   `json:"supportsImageInput"`
	SupportsTools      bool   `json:"supportsTools"`
	SupportsServerless bool   `json:"supportsServerless"`
}

// ListModels lists the models of the account, page by page, with their
// context windows and input modalities. It keeps only a model that a
// gopi agent can call with no deployment of its own: a ready serverless
// model with tools. So it skips the embedding and image models.
// A page token that repeats is an error, so a server that sends the
// same token on each page does not keep the list in a loop.
func (p *Provider) ListModels(ctx context.Context) ([]ai.Model, error) {
	var models []ai.Model
	token := ""
	seen := make(map[string]bool)
	for {
		page, err := p.listPage(ctx, token)
		if err != nil {
			return nil, fmt.Errorf("fireworks: list models: %w", err)
		}
		for _, m := range page.Models {
			if !m.servable() {
				continue
			}
			models = append(models, m.model())
		}
		if page.NextPageToken == "" {
			return models, nil
		}
		if seen[page.NextPageToken] {
			return nil, fmt.Errorf("fireworks: list models: the page token %q repeats", page.NextPageToken)
		}
		seen[page.NextPageToken] = true
		token = page.NextPageToken
	}
}

// listPage returns the page of the list after token. An empty token
// gives the first page.
func (p *Provider) listPage(ctx context.Context, token string) (listResponse, error) {
	query := url.Values{}
	query.Set("pageSize", strconv.Itoa(pageSize))
	if token != "" {
		query.Set("pageToken", token)
	}
	path := "/v1/accounts/" + url.PathEscape(p.cfg.account) + "/models"
	endpoint := p.cfg.baseURL + path + "?" + query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return listResponse{}, err
	}
	if p.cfg.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.cfg.apiKey)
	}
	client := cmp.Or(p.cfg.httpClient, http.DefaultClient)
	resp, err := client.Do(req)
	if err != nil {
		return listResponse{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
		return listResponse{}, fmt.Errorf("%s: %s", resp.Status, body)
	}
	var page listResponse
	if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
		return listResponse{}, err
	}
	return page, nil
}

// servable reports whether the serverless API serves m with tools.
func (m listedModel) servable() bool {
	return m.State == readyState && m.SupportsServerless && m.SupportsTools
}

// model returns m as an [ai.Model].
func (m listedModel) model() ai.Model {
	input := []ai.Modality{ai.ModalityText}
	if m.SupportsImageInput {
		input = append(input, ai.ModalityImage)
	}
	return ai.Model{
		ID:            m.Name,
		Name:          m.DisplayName,
		ContextWindow: m.ContextLength,
		Input:         input,
	}
}
