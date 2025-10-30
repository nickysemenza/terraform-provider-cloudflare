package utils

import (
	"context"

	"github.com/cloudflare/cloudflare-go/v6"
	"github.com/cloudflare/cloudflare-go/v6/option"
	"github.com/cloudflare/cloudflare-go/v6/resource_tagging"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/cloudflare/terraform-provider-cloudflare/internal/logging"
)

// TagScope represents whether tags are zone-level or account-level.
type TagScope int

const (
	TagScopeZone TagScope = iota
	TagScopeAccount
)

// TagsHelper encapsulates resource tagging operations via the centralized
// resource_tagging API. Construct one per resource type using NewTagsHelper.
type TagsHelper struct {
	Scope        TagScope
	ResourceType string
}

// NewTagsHelper creates a TagsHelper for the given scope and resource type.
// The resourceType must match the API enum values (e.g. "custom_hostname", "zone", "account").
func NewTagsHelper(scope TagScope, resourceType string) TagsHelper {
	return TagsHelper{Scope: scope, ResourceType: resourceType}
}

// SetTagsAfterCreate sets tags on a newly created resource. Only calls the API if
// tags were provided in the plan. Returns the resulting tags for state, or nil on failure.
func (h TagsHelper) SetTagsAfterCreate(ctx context.Context, client *cloudflare.Client, diags *diag.Diagnostics, scopeID, resourceID string, plannedTags *map[string]types.String) *map[string]types.String {
	if plannedTags == nil {
		return nil
	}
	return h.updateTags(ctx, client, diags, scopeID, resourceID, plannedTags, "failed to set tags after creation")
}

// UpdateTagsIfChanged updates tags if they differ between plan and state.
// Returns the resulting tags for state.
func (h TagsHelper) UpdateTagsIfChanged(ctx context.Context, client *cloudflare.Client, diags *diag.Diagnostics, scopeID, resourceID string, planned, state *map[string]types.String) *map[string]types.String {
	if !TagsChanged(planned, state) {
		return state
	}
	return h.updateTags(ctx, client, diags, scopeID, resourceID, planned, "failed to update tags")
}

// ReadTags fetches tags for a resource. Returns the tags for state, or nil on failure.
func (h TagsHelper) ReadTags(ctx context.Context, client *cloudflare.Client, diags *diag.Diagnostics, scopeID, resourceID string) *map[string]types.String {
	if h.Scope == TagScopeZone {
		resp, err := client.ResourceTagging.ZoneTags.Get(
			ctx,
			resource_tagging.ZoneTagGetParams{
				ZoneID:       cloudflare.F(scopeID),
				ResourceID:   cloudflare.F(resourceID),
				ResourceType: cloudflare.F(resource_tagging.ZoneTagGetParamsResourceType(h.ResourceType)),
			},
			option.WithMiddleware(logging.Middleware(ctx)),
		)
		if err != nil {
			diags.AddWarning("failed to fetch tags", err.Error())
			return nil
		}
		return extractTagsFromResponse(resp.Tags)
	}

	resp, err := client.ResourceTagging.AccountTags.Get(
		ctx,
		resource_tagging.AccountTagGetParams{
			AccountID:    cloudflare.F(scopeID),
			ResourceID:   cloudflare.F(resourceID),
			ResourceType: cloudflare.F(resource_tagging.AccountTagGetParamsResourceType(h.ResourceType)),
		},
		option.WithMiddleware(logging.Middleware(ctx)),
	)
	if err != nil {
		diags.AddWarning("failed to fetch tags", err.Error())
		return nil
	}
	return extractTagsFromResponse(resp.Tags)
}

// updateTags calls the resource_tagging Update API and returns the resulting tags.
func (h TagsHelper) updateTags(ctx context.Context, client *cloudflare.Client, diags *diag.Diagnostics, scopeID, resourceID string, tags *map[string]types.String, warnMsg string) *map[string]types.String {
	apiTags := ConvertTerraformTagsToAPI(tags)

	if h.Scope == TagScopeZone {
		resp, err := client.ResourceTagging.ZoneTags.Update(
			ctx,
			resource_tagging.ZoneTagUpdateParams{
				ZoneID: cloudflare.F(scopeID),
				Body: resource_tagging.ZoneTagUpdateParamsBodyResourceTaggingSetTagsRequestZoneLevelBase{
					ResourceID:   cloudflare.F(resourceID),
					ResourceType: cloudflare.F(resource_tagging.ZoneTagUpdateParamsBodyResourceTaggingSetTagsRequestZoneLevelBaseResourceType(h.ResourceType)),
					Tags:         cloudflare.F(apiTags),
				},
			},
			option.WithMiddleware(logging.Middleware(ctx)),
		)
		if err != nil {
			diags.AddWarning(warnMsg, err.Error())
			return nil
		}
		return extractTagsFromResponse(resp.Tags)
	}

	resp, err := client.ResourceTagging.AccountTags.Update(
		ctx,
		resource_tagging.AccountTagUpdateParams{
			AccountID: cloudflare.F(scopeID),
			Body: resource_tagging.AccountTagUpdateParamsBodyResourceTaggingSetTagsRequestAccountLevelBase{
				ResourceID:   cloudflare.F(resourceID),
				ResourceType: cloudflare.F(resource_tagging.AccountTagUpdateParamsBodyResourceTaggingSetTagsRequestAccountLevelBaseResourceType(h.ResourceType)),
				Tags:         cloudflare.F(apiTags),
			},
		},
		option.WithMiddleware(logging.Middleware(ctx)),
	)
	if err != nil {
		diags.AddWarning(warnMsg, err.Error())
		return nil
	}
	return extractTagsFromResponse(resp.Tags)
}

// TagsSchemaAttribute returns the standard schema attribute for resource tags.
func TagsSchemaAttribute() schema.MapAttribute {
	return schema.MapAttribute{
		Description: "Tags associated with this resource. Tags are key-value pairs that can be used to organize and filter resources.",
		Optional:    true,
		Computed:    true,
		ElementType: types.StringType,
	}
}

// ConvertTerraformTagsToAPI converts Terraform tags (map[string]types.String)
// to API tags (map[string]string) for use in API requests.
// Returns an empty map if input is nil.
func ConvertTerraformTagsToAPI(tags *map[string]types.String) map[string]string {
	result := make(map[string]string)
	if tags == nil {
		return result
	}

	for k, v := range *tags {
		result[k] = v.ValueString()
	}
	return result
}

// ConvertAPITagsToTerraform converts API tags (map[string]string)
// to Terraform tags (map[string]types.String) for storing in state.
// Returns nil if input map is empty.
func ConvertAPITagsToTerraform(tags map[string]string) *map[string]types.String {
	if len(tags) == 0 {
		return nil
	}

	result := make(map[string]types.String, len(tags))
	for k, v := range tags {
		result[k] = types.StringValue(v)
	}
	return &result
}

// extractTagsFromResponse extracts tags from a resource_tagging API response.
// The SDK response types use Tags interface{} which at runtime may be
// map[string]string (from typed union variants) or map[string]interface{}
// (from generic JSON unmarshaling).
func extractTagsFromResponse(tags interface{}) *map[string]types.String {
	switch t := tags.(type) {
	case map[string]string:
		return ConvertAPITagsToTerraform(t)
	case map[string]interface{}:
		result := make(map[string]string, len(t))
		for k, v := range t {
			if s, ok := v.(string); ok {
				result[k] = s
			}
		}
		return ConvertAPITagsToTerraform(result)
	default:
		return nil
	}
}

// TagsChanged checks if Terraform tags have changed between planned and state.
// Returns true if tags are different, false if they're the same.
func TagsChanged(planned, state *map[string]types.String) bool {
	// Check for nil/existence changes
	if (planned == nil && state != nil) ||
		(planned != nil && state == nil) {
		return true
	}

	// Both nil = no change
	if planned == nil && state == nil {
		return false
	}

	// Check length difference
	if len(*planned) != len(*state) {
		return true
	}

	// Check each key-value pair
	for k, v := range *planned {
		stateV, exists := (*state)[k]
		if !exists || stateV.ValueString() != v.ValueString() {
			return true
		}
	}

	return false
}
