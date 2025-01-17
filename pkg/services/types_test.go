package services

import (
	"testing"

	. "github.com/onsi/gomega"
)

func makeParams(orderByAry []string) map[string][]string {
	params := make(map[string][]string)
	params["orderBy"] = orderByAry
	return params
}

func getValidTestParams() []string {
	return []string{"bootstrap_server_host", "cloud_provider", "cluster_id", "created_at", "href", "id", "instance_type", "multi_az", "name", "organisation_id", "owner", "reauthentication_enabled", "region", "status", "updated_at", "version"}
}

func Test_ValidateOrderBy(t *testing.T) {
	tests := []struct {
		name        string
		params      map[string][]string
		wantErr     bool
		validParams []string
	}{
		{
			name:        "One Column Asc",
			params:      makeParams([]string{"name asc"}),
			wantErr:     false,
			validParams: getValidTestParams(),
		},
		{
			name:        "One Column Desc",
			params:      makeParams([]string{"region desc"}),
			wantErr:     false,
			validParams: getValidTestParams(),
		},
		{
			name:        "Multiple Columns Mixed Sorting",
			params:      makeParams([]string{"region desc, name asc, cloud_provider desc"}),
			wantErr:     false,
			validParams: getValidTestParams(),
		},
		{
			name:        "Multiple Columns Mixed Sorting with invalid column",
			params:      makeParams([]string{"region desc, name asc, invalid desc"}),
			wantErr:     true,
			validParams: getValidTestParams(),
		},
		{
			name:        "Multiple Columns Mixed Sorting with invalid sort",
			params:      makeParams([]string{"region desc, name asc, cloud_provider random"}),
			wantErr:     true,
			validParams: getValidTestParams(),
		},
		{
			name:        "Multiple Columns Multiple spaces",
			params:      makeParams([]string{"region    desc  ,    name   asc ,    cloud_provider asc"}),
			wantErr:     false,
			validParams: getValidTestParams(),
		},
		{
			name:        "Invalid Column Desc",
			params:      makeParams([]string{"invalid desc"}),
			wantErr:     true,
			validParams: getValidTestParams(),
		},
		{
			name:        "No ordering - first",
			params:      makeParams([]string{"region"}),
			wantErr:     false,
			validParams: getValidTestParams(),
		},
		{
			name:        "No ordering - mixed",
			params:      makeParams([]string{"region, cloud_provider desc, name"}),
			wantErr:     false,
			validParams: getValidTestParams(),
		},
		{
			name:        "No ordering - all valid params",
			params:      makeParams(getValidTestParams()),
			wantErr:     false,
			validParams: getValidTestParams(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			RegisterTestingT(t)
			la := NewListArguments(tt.params)
			err := la.Validate(tt.validParams)
			if tt.wantErr {
				Expect(err).To(HaveOccurred())
			} else {
				Expect(err).NotTo(HaveOccurred())
			}
		})
	}
}
