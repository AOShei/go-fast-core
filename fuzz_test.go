package gofastcore

import (
	"encoding/json"
	"io"
	"testing"
)

// FuzzDocumentJSON: any JSON that decodes into a Document, valid or not,
// must validate and render without panicking. A caller may read a
// document back from JSON written by another program.
func FuzzDocumentJSON(f *testing.F) {
	f.Add([]byte(`{"schema_version":"0.2","parts":[{"kind":"page","blocks":[{"kind":"table","table":{"header_rows":4,"rows":[["a","b"],["c"]],"merges":[{"row":5,"col":9,"row_span":3,"col_span":-2}],"cells":[{"row":7,"col":1,"href":"x"}],"display":[{"row":3,"col":3}]}}]}]}`))
	f.Add([]byte(`{"parts":[{"kind":"body","blocks":[{"kind":"list_item","level":-3,"text":"a","spans":[{"start":5,"end":2}]},{"kind":"heading","level":99,"text":"h"}]}]}`))
	f.Add([]byte(`{"outline":[{"title":"t","level":-1,"part":99}],"children":[null,{"parts":[{"kind":"sheet","blocks":[{"kind":"paragraph","text":"c","spans":[{"start":-1,"end":99,"href":"h"}]}]}]}]}`))
	f.Fuzz(func(t *testing.T, b []byte) {
		var d Document
		if json.Unmarshal(b, &d) != nil {
			return
		}
		_ = d.Validate()
		ro := &RenderOptions{Children: true, Stored: true, Furniture: true, PartMarkers: true}
		d.WriteMarkdown(io.Discard, ro)
		d.WriteText(io.Discard, ro)
		d.WriteMarkdown(io.Discard, nil)
	})
}
