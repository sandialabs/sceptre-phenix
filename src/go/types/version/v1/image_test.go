package v1

import "testing"

func TestImagePostBuild(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		image Image
		want  string
	}{
		{name: "no scripts", image: Image{}, want: ""},
		{
			name: "scripts in order, blank lines dropped",
			image: Image{
				Scripts:     map[string]string{"a": "echo a\n\necho b\n", "b": "echo c", "unused": "echo x"},
				ScriptOrder: []string{"b", "a", "missing"},
			},
			want: "      echo c\n      echo a\n      echo b",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.image.PostBuild(); got != tt.want {
				t.Errorf("PostBuild() = %q, want %q", got, tt.want)
			}
		})
	}
}
