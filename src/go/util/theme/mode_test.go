package theme

import "testing"

func TestParse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		value   string
		want    Mode
		wantErr bool
	}{
		{value: "system", want: System},
		{value: " light ", want: Light},
		{value: "DARK", want: Dark},
		{value: "auto", wantErr: true},
		{value: "", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.value, func(t *testing.T) {
			t.Parallel()

			got, err := Parse(tt.value)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected an error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("Parse(%q) = %q, want %q", tt.value, got, tt.want)
			}
		})
	}
}
