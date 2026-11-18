package gofastcore

import "encoding/json"

// The types below marshal their required lists as [] when empty, never as
// null. A consumer in another language can then iterate "parts", "blocks"
// and "rows" without a nil check, which matters most for exactly the parts
// that failed and the children that were not extracted.

func (d Document) MarshalJSON() ([]byte, error) {
	type plain Document
	if d.Parts == nil {
		d.Parts = []Part{}
	}
	return json.Marshal(plain(d))
}

func (p Part) MarshalJSON() ([]byte, error) {
	type plain Part
	if p.Blocks == nil {
		p.Blocks = []Block{}
	}
	return json.Marshal(plain(p))
}

func (t Table) MarshalJSON() ([]byte, error) {
	type plain Table
	if t.Rows == nil {
		t.Rows = [][]string{}
	}
	return json.Marshal(plain(t))
}
