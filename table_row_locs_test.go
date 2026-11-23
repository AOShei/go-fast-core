package gofastcore

import (
	"encoding/json"
	"strings"
	"testing"
)

func rowLocsDoc(locs []Location) *Document {
	return &Document{SchemaVersion: SchemaVersion, Source: Source{Format: "xml"}, Parts: []Part{{Kind: PartBody, Number: 1, Blocks: []Block{{
		Kind:  KindTable,
		Table: &Table{Rows: [][]string{{"a", "b"}, {"c", "d"}}, RowLocs: locs},
	}}}}}
}

func TestTableRowLocs(t *testing.T) {
	doc := rowLocsDoc([]Location{{Offset: 10, Length: 20}, {Offset: 30, Length: 20, Anchor: "r2"}})
	if err := doc.Validate(); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"row_locs":[{"offset":10,"length":20},{"offset":30,"length":20,"anchor":"r2"}]`) {
		t.Fatalf("row_locs not written as expected: %s", data)
	}
	var back Document
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	if got := back.Parts[0].Blocks[0].Table.RowLocs; len(got) != 2 || got[1].Anchor != "r2" || got[0].Offset != 10 {
		t.Fatalf("row_locs after a round trip: %+v", got)
	}
	// Absent when the source says nothing about rows.
	data, _ = json.Marshal(rowLocsDoc(nil))
	if strings.Contains(string(data), "row_locs") {
		t.Fatalf("row_locs written for a table without them: %s", data)
	}
}

func TestTableRowLocsValidate(t *testing.T) {
	for name, locs := range map[string][]Location{
		"one entry for two rows": {{Offset: 1, Length: 1}},
		"negative offset":        {{Offset: -1}, {}},
		"negative length":        {{}, {Length: -1}},
	} {
		if err := rowLocsDoc(locs).Validate(); err == nil {
			t.Errorf("%s: Validate accepted it", name)
		}
	}
}
