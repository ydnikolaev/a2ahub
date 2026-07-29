package notes

import (
	"github.com/ydnikolaev/a2ahub/internal/release"
	"github.com/ydnikolaev/a2ahub/internal/schema"
)

type releaseDetailDecoder struct {
	corpus *schema.Corpus
}

// NewDetailDecoder returns the one production adapter from the
// authored release-notes parser and schema corpus into P49's verified future
// detail cache.
func NewDetailDecoder() (release.DetailDecoder, error) {
	corpus, err := schema.Load()
	if err != nil {
		return nil, err
	}
	return releaseDetailDecoder{corpus: corpus}, nil
}

func (d releaseDetailDecoder) DecodeAndValidate(raw []byte) (release.Detail, error) {
	parsed, err := ParseReleaseNotes(raw)
	if err != nil {
		return release.Detail{}, release.ErrDetailMalformed
	}
	if parsed.Schema != release.ReleaseNotesSchemaV1 {
		return release.Detail{}, release.ErrDetailUnknownSchema
	}
	instance, err := schema.DecodeYAMLInstance(raw)
	if err != nil {
		return release.Detail{}, release.ErrDetailMalformed
	}
	violations, err := d.corpus.ValidateReleaseNotes(instance)
	if err != nil || len(violations) != 0 {
		return release.Detail{}, release.ErrDetailMalformed
	}
	detail := release.Detail{
		Schema: parsed.Schema, Version: parsed.Version, Released: parsed.Released,
		Headline: parsed.Headline, Changes: make([]release.DetailChange, 0, len(parsed.Changes)),
	}
	for _, change := range parsed.Changes {
		detail.Changes = append(detail.Changes, release.DetailChange{
			ID: change.ID, Kind: change.Kind, Impact: change.Impact,
			Subject: change.Subject, Detail: change.Detail,
			Affects: append([]string(nil), change.Affects...),
			Action: release.DetailAction{
				Scope: change.Action.Scope, Why: change.Action.Why,
				Detect: append([]string(nil), change.Action.Detect...),
				Run:    append([]string(nil), change.Action.Run...),
			},
		})
	}
	return detail, nil
}
