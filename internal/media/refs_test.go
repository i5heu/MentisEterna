package media

import (
	"testing"
)

func TestExtractReferencedFileIDsFromMarkdown(t *testing.T) {
	tests := []struct {
		name string
		body string
		want []int64
	}{
		{
			name: "single link",
			body: `[my file](/file/1/42)`,
			want: []int64{42},
		},
		{
			name: "single image",
			body: `![alt text](/file/1/42)`,
			want: []int64{42},
		},
		{
			name: "multiple references",
			body: `[a](/file/1/10) and [b](/file/2/20) and ![c](/file/3/30)`,
			want: []int64{10, 20, 30},
		},
		{
			name: "deduplicates",
			body: `[a](/file/1/10) and [b](/file/2/10) same file`,
			want: []int64{10},
		},
		{
			name: "no references",
			body: `just plain text here`,
			want: nil,
		},
		{
			name: "empty body",
			body: ``,
			want: nil,
		},
		{
			name: "reference in parenthesized url",
			body: `see (http://example.com/file/5/99)`,
			want: []int64{99},
		},
		{
			name: "ignore malformed",
			body: `/file/notanumber/abc /file//123`,
			want: nil,
		},
		{
			name: "noteID is any number",
			body: `[link](/file/999/888)`,
			want: []int64{888},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractReferencedFileIDs(tt.body)
			if len(got) != len(tt.want) {
				t.Errorf("ExtractReferencedFileIDs(%q) = %v, want %v", tt.body, got, tt.want)
				return
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("ExtractReferencedFileIDs(%q) = %v, want %v", tt.body, got, tt.want)
					return
				}
			}
		})
	}
}
