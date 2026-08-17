package names

import "testing"

func TestDeriveToolName(t *testing.T) {
	cases := []struct {
		name     string
		upstream string
		tool     string
		want     string
		wantErr  bool
	}{
		{name: "simple", upstream: "billing", tool: "getInvoice", want: "billing_getInvoice"},
		{name: "sanitizes special chars", upstream: "my-api", tool: "do thing!", want: "my-api_do_thing_"},
		{name: "collapses runs of bad chars", upstream: "a", tool: "b   c", want: "a_b_c"},
		{
			name:     "too long",
			upstream: "upstream",
			tool:     "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			wantErr:  true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := DeriveToolName(tc.upstream, tc.tool)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got name %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestDeriveToolNameEmptyInputsStillProduceUnderscore(t *testing.T) {
	// The literal "_" separator means the derived name can never sanitize to
	// an empty string, even with empty upstream/tool names.
	got, err := DeriveToolName("", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "_" {
		t.Fatalf("got %q, want \"_\"", got)
	}
}
