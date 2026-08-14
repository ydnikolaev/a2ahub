package html

import (
	"encoding/json"
	"strings"
	"testing"
)

// dashboardTemplateCorpus returns the exact shipped template bytes followed by
// the decoded self-contained component resources embedded in those bytes.
//
// P4 split the dashboard root into Design Components without changing the
// offline artifact contract. The build stores transitive component sources as
// JSON strings inside Blob resources, so a test looking only for an
// unescaped source token in template.html would report a false drop. Keeping
// the original bytes in the corpus still lets encoding/bundle assertions see
// their exact representation; appending decoded resources lets semantic
// reachability assertions inspect every source the shipped runtime can load.
func dashboardTemplateCorpus(t *testing.T) string {
	t.Helper()

	raw := string(DefaultTemplate())
	const marker = `new Blob([`
	var decoded strings.Builder
	for cursor := 0; ; {
		relative := strings.Index(raw[cursor:], marker)
		if relative < 0 {
			break
		}
		start := cursor + relative + len(marker)
		decoder := json.NewDecoder(strings.NewReader(raw[start:]))
		var resource string
		if err := decoder.Decode(&resource); err != nil {
			t.Fatalf("decode embedded dashboard Blob resource at byte %d: %v", start, err)
		}
		decoded.WriteByte('\n')
		decoded.WriteString(resource)
		cursor = start + int(decoder.InputOffset())
	}
	if decoded.Len() == 0 {
		t.Fatal("shipped dashboard contains no decodable component Blob resources")
	}
	return raw + decoded.String()
}
