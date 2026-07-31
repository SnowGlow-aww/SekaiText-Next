package service

import (
	"encoding/json"
	"errors"

	"sekaitext/backend/internal/model"
)

// DecodeGlossarySnapshot validates and decodes a full authoritative glossary
// snapshot. Entries and appellations must be present as JSON arrays, even when
// empty: a missing or null field must never be interpreted as an intentional
// authoritative clear. Grammar remains optional for compatibility with exports
// produced before that section was introduced.
func DecodeGlossarySnapshot(body []byte) (model.GlossaryData, error) {
	var snapshot model.GlossaryData
	if err := json.Unmarshal(body, &snapshot); err != nil {
		return model.GlossaryData{}, err
	}
	if snapshot.Entries == nil || snapshot.Appellations == nil {
		return model.GlossaryData{}, errors.New("glossary snapshot is missing full snapshot arrays")
	}
	return snapshot, nil
}
