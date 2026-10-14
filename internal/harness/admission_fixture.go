//go:build buddy_admission_fixture

package harness

// The adapter's end-to-end tests (cmd/buddy-adapter) build the real binary
// and launch it against a fake native harness whose bytes no committed
// record admits. Built with -tags buddy_admission_fixture, and only then,
// the adapter reads its records from the file BUDDY_ADMISSION_FIXTURE names
// INSTEAD of the embedded set (so a fixture record for a committed version
// is the only one, as a committed record is), through the same strict parser
// and the same launch checks: those tests exercise real admission rather
// than a bypass. install.sh and every
// qualification runner build without the tag; an installed adapter has no
// way to read a record from a file (TestAdmissionFixtureNotInDefaultBuild).

import (
	"encoding/json"
	"os"
)

func init() {
	name := os.Getenv("BUDDY_ADMISSION_FIXTURE")
	if name == "" {
		return
	}
	admissionRecords = func(harness, executable string) ([]AdmissionRecord, error) {
		var records []AdmissionRecord
		data, err := os.ReadFile(name)
		if err != nil {
			return nil, refuse("admission_unreadable", "fixture records: %v", err)
		}
		var raw []json.RawMessage
		if err := json.Unmarshal(data, &raw); err != nil {
			return nil, refuse("admission_unreadable", "fixture records: %v", err)
		}
		for _, item := range raw {
			r, err := ParseAdmission(item)
			if err != nil {
				return nil, err
			}
			records = append(records, r)
		}
		return records, nil
	}
}
