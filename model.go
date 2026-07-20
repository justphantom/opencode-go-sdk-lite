package opencode

import (
	"context"
	"fmt"
	"net/url"
)

// locationQuery 把 LocationRef 编码成 V1 平铺 query（directory=...&workspace=...）。
func locationQuery(loc *LocationRef) url.Values {
	q := url.Values{}
	if loc == nil {
		return q
	}
	if loc.Directory != "" {
		q.Set("directory", loc.Directory)
	}
	if loc.WorkspaceID != "" {
		q.Set("workspace", loc.WorkspaceID)
	}
	return q
}

// listProviders 拉取 GET /provider 的原始响应，供 List*/Get* 复用。
func (c *Client) listProviders(ctx context.Context, loc *LocationRef) (*ProvidersResponse, error) {
	var out ProvidersResponse
	if err := c.doJSON(ctx, http_GET, "/provider", locationQuery(loc), nil, &out, 0); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListModels 列出所有 provider 下的模型。V1 无独立模型目录，
// 模型清单内嵌在 GET /provider 的 all[].models 中，此处拍平；
// Enabled 由 status=="active" 推导。
func (c *Client) ListModels(ctx context.Context, loc *LocationRef) ([]ModelInfo, error) {
	resp, err := c.listProviders(ctx, loc)
	if err != nil {
		return nil, err
	}
	var out []ModelInfo
	for _, p := range resp.All {
		for _, m := range p.Models {
			m.Enabled = m.Status == "active"
			out = append(out, m)
		}
	}
	return out, nil
}

// ListProviders 列出可用 provider。
func (c *Client) ListProviders(ctx context.Context, loc *LocationRef) ([]ProviderInfo, error) {
	resp, err := c.listProviders(ctx, loc)
	if err != nil {
		return nil, err
	}
	return resp.All, nil
}

// GetProvider 返回单个 provider 详情。V1 无 /provider/{id}，从 all 中按 id 筛选。
func (c *Client) GetProvider(ctx context.Context, providerID string, loc *LocationRef) (*ProviderInfo, error) {
	resp, err := c.listProviders(ctx, loc)
	if err != nil {
		return nil, err
	}
	for i := range resp.All {
		if resp.All[i].ID == providerID {
			return &resp.All[i], nil
		}
	}
	return nil, fmt.Errorf("opencode: provider %q not found", providerID)
}
