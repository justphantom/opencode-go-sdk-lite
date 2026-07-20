package opencode

import (
	"context"
	"encoding/json"
	"net/url"
)

// locationQuery 把 LocationRef 编码成 deepObject query（spec: location[directory]=...&location[workspace]=...）。
func locationQuery(loc *LocationRef) url.Values {
	q := url.Values{}
	if loc == nil {
		return q
	}
	if loc.Directory != "" {
		q.Set("location[directory]", loc.Directory)
	}
	if loc.WorkspaceID != "" {
		q.Set("location[workspace]", loc.WorkspaceID)
	}
	return q
}

// ListModels 列出可用模型，按发布时间倒序。
func (c *Client) ListModels(ctx context.Context, loc *LocationRef) ([]ModelV2Info, error) {
	var wrapped struct {
		Location json.RawMessage `json:"location"`
		Data     []ModelV2Info   `json:"data"`
	}
	if err := c.doJSON(ctx, http_GET, "/api/model", locationQuery(loc), nil, &wrapped, 0); err != nil {
		return nil, err
	}
	return wrapped.Data, nil
}

// ListProviders 列出可用 provider。
func (c *Client) ListProviders(ctx context.Context, loc *LocationRef) ([]ProviderV2Info, error) {
	var wrapped struct {
		Location json.RawMessage  `json:"location"`
		Data     []ProviderV2Info `json:"data"`
	}
	if err := c.doJSON(ctx, http_GET, "/api/provider", locationQuery(loc), nil, &wrapped, 0); err != nil {
		return nil, err
	}
	return wrapped.Data, nil
}

// GetProvider 返回单个 provider 详情。
func (c *Client) GetProvider(ctx context.Context, providerID string, loc *LocationRef) (*ProviderV2Info, error) {
	var wrapped struct {
		Location json.RawMessage `json:"location"`
		Data     ProviderV2Info  `json:"data"`
	}
	if err := c.doJSON(ctx, http_GET, "/api/provider/"+providerID, locationQuery(loc), nil, &wrapped, 0); err != nil {
		return nil, err
	}
	return &wrapped.Data, nil
}
