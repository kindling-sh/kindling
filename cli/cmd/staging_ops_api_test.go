package cmd

import "testing"

// ════════════════════════════════════════════════════════════════
// stagingDeployNameDefaults
// ════════════════════════════════════════════════════════════════

func TestStagingDeployNameDefaults(t *testing.T) {
	tests := []struct {
		name          string
		slug          string
		namespace     string
		wantChartName string
		wantNamespace string
	}{
		{
			name:          "no branch resolved, no namespace -- today's exact pre-existing behavior",
			slug:          "",
			namespace:     "",
			wantChartName: "kindling-snapshot",
			wantNamespace: "default",
		},
		{
			name:          "branch resolved, no namespace -- both default to the slug",
			slug:          "feature-add-checkout-retry",
			namespace:     "",
			wantChartName: "feature-add-checkout-retry",
			wantNamespace: "feature-add-checkout-retry",
		},
		{
			name:          "branch resolved, explicit namespace -- namespace is never overridden",
			slug:          "feature-add-checkout-retry",
			namespace:     "my-explicit-ns",
			wantChartName: "feature-add-checkout-retry",
			wantNamespace: "my-explicit-ns",
		},
		{
			name:          "no branch resolved, explicit namespace -- chart name still falls back",
			slug:          "",
			namespace:     "my-explicit-ns",
			wantChartName: "kindling-snapshot",
			wantNamespace: "my-explicit-ns",
		},
		{
			name:          "explicit namespace literally 'default' is preserved, not re-derived",
			slug:          "feature-x",
			namespace:     "default",
			wantChartName: "feature-x",
			wantNamespace: "default",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			chartName, ns := stagingDeployNameDefaults(tt.slug, tt.namespace)
			if chartName != tt.wantChartName {
				t.Errorf("chartName = %q, want %q", chartName, tt.wantChartName)
			}
			if ns != tt.wantNamespace {
				t.Errorf("namespace = %q, want %q", ns, tt.wantNamespace)
			}
		})
	}
}
