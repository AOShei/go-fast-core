# go-fast-core

The shared document model and extractor interface for the go-fast-* extraction libraries. Standard library only.

Every extractor (`go-fast-pdf`, and later ooxml, html, email, ...) returns the same `*gofastcore.Document`, so chunkers, indexers and knowledge-graph builders are written once and work for every format.

```go
import "github.com/AOShei/go-fast-core" // package gofastcore
```

**Status: schema version 0.1, agreed with the PDF extractor over two review rounds on 2026-11-18.** Not yet tagged. It will be tagged v0.1.0 once `go-fast-pdf` has been ported to it.

## Series conventions

- **Naming.** Every package carries the brand prefix: `gofastcore`, `gofastpdf`, `gofasthtml`. No package in the series shares a name with a standard library package, so callers never need an import alias.
- **Layout.** A library's public API is in its module root. Implementation goes under `internal/`, the CLI under `cmd/`. No `pkg/` directory.
- **Format-specific settings.** `Options` holds only what every format understands. Settings for one format (for PDF: memory mapping, a cache budget) are fields on that library's extractor value: `gofastpdf.Extractor{...}.Extract(...)`.
- **Dependencies.** The standard library and other go-fast modules only. `go-fast-core` itself imports only the standard library. Any other dependency must be challenged.
- **Layering.** The shared model is the common denominator across formats. An extractor may offer a richer native API beside it (for PDF: word and line boxes, font names and sizes, glyph positions). Those details stay out of core.
- **No OCR.** The go-fast libraries extract what the file contains. They report what they found and leave the next step to the caller.

## The model

```
Document
├── Source      format, name, MIME, size
├── Metadata    title, authors, dates, language, extra
├── Outline[]   the source's own table of contents (PDF bookmarks, epub nav)
├── Parts[]     the format's natural unit: page, slide, sheet, chapter, message, body
│   ├── Blocks[]    content in reading order
│   └── Warnings[]  what went wrong in this part
├── Children[]  embedded documents (attachments, archive members), each a full Document
└── Warnings[]  what went wrong for the document as a whole
```

A block has a `Kind`, usually `Text`, and kind-specific fields:

| Kind | Text | Level | Other fields |
|---|---|---|---|
| `heading` | heading text | 1–6 | |
| `paragraph` | paragraph text | | |
| `list_item` | item text without the marker | nesting depth, from 1 | `Ordered`, `Marker` |
| `table` | empty | | `Table` (rows of cell strings, header row count, merges) |
| `image` | alt text or description | | `Image` (id, MIME, pixel size, optional bytes) |
| `code` | verbatim code | | `Attrs["language"]` |
| `quote` | quoted text | nesting depth, from 1 | |
| `caption` | caption text | | |
| `footnote` | note text | | `Marker` |
| `formula` | LaTeX or plain text | | |
| `note` | speaker note or comment | | |
| `header`, `footer` | running page header/footer | | |

Every block may also carry:

- `Origin`: `native` when the source declares the block's kind, level and boundaries (a docx style, an HTML tag, a tagged-PDF element, a PDF outline entry), `inferred` when the extractor deduced them from geometry or fonts. Absent in JSON means `native`. It does not cover the block's position in the reading order: a PDF image is `native` even though where it falls among the text is inferred.
- `Continued`: this block continues the last block of the same kind in the previous part, such as a paragraph split by a page break.
- `Loc`: where it came from. `BBox` for paged formats, `Range` (A1 notation) for sheets, `Offset`/`Length` for plain-text formats. This is what lets a retrieval hit be cited.
- `Spans`: optional inline bold/italic/link ranges over `Text`.
- `Attrs`: format-specific key/values with no dedicated field.

### Design decisions

- **Flat blocks, no section tree.** Hierarchy is implied by heading levels. Extractors emit in one pass and page workers stay independent.
- **Parts are self-contained.** A part owns its blocks and its warnings. A page worker fills one `Part` with no shared state and no locking, and a part handed on by itself is complete.
- **Structure first, Markdown derived.** `Markdown()` and `Text()` render the blocks. Extractors never emit Markdown strings.
- **Provenance, not confidence.** `Origin` says whether structure was declared or guessed. There is no numeric score: heuristics do not produce calibrated probabilities. A consumer that needs certainty treats `inferred` blocks as plain text and chunks by size.
- **Tables are `[][]string`.** Merged cells put their text top-left and are listed in `Merges`. Numbers and dates appear as the source displays them.
- **Furniture is kept but tagged.** Running headers and footers are `header`/`footer` blocks; the renderers drop them by default.
- **Partial results beat errors.** A document that is 95% readable returns with warnings. An error means nothing usable came out.

## Extractor contract

1. All strings are valid UTF-8 and follow the text conventions below.
2. A block is at least a paragraph-sized unit, never a fragment of a line or word.
3. Blocks are in reading order. Where the format does not declare order (PDF), order is the extractor's best inference.
4. A top-level document has at least one part.
5. `Part.Number` is the part's 1-based ordinal in the source. Numbers strictly increase but need not be contiguous: extracting pages 40–60 yields parts numbered 40 to 60.
6. A block that runs across a part boundary is either assigned to the part where it starts or split, with `Continued` set on the later piece.
7. `Table` is set exactly on `table` blocks, `Image` exactly on `image` blocks.
8. `Extract` is safe to call from many goroutines and honors context cancellation.
9. Output is deterministic: the same input and options give the same document whatever `Options.Workers` is, including where a limit truncates it.

`doc.Validate()` checks everything mechanical in rules 1, 4, 5 and 7, plus spans, tables, merges, warnings and enum values. Run it on every test fixture and fuzz output:

```go
doc, err := gofastpdf.Extract(ctx, f, size, nil)
if err != nil { t.Fatal(err) }
if err := doc.Validate(); err != nil { t.Fatal(err) }
```

### Text conventions

- Text is in logical order, the order in which it is read and typed, also for right-to-left scripts. An extractor that cannot guarantee this for some input says so with a warning.
- Soft line breaks inside a paragraph are joined with a single space. A `\n` in `Text` means a hard break.
- End-of-line hyphenation is resolved only when the extractor is certain. Otherwise the hyphen stays and the lines are joined with no space: `inter-` + `national` gives `inter-national`, so a real compound such as `well-` + `known` comes out right.
- Presentation-form ligatures (U+FB00 to U+FB06) are expanded to their letters.
- No string contains a control character (NUL, form feed, carriage return, ...) other than `\n` and `\t`.
- Block text is not empty and has no leading or trailing whitespace.
- Code blocks are verbatim: the ligature and whitespace rules do not apply to them.
- Unicode normalization form is not guaranteed (normalization is not in the standard library).

`Validate` enforces the rules it can check mechanically: no control characters in any string, no unexpanded ligatures in block text or table cells, and trimmed non-empty block text.

### JSON

`parts`, `blocks` and `rows` are always arrays, `[]` when empty and never `null`, so a consumer can iterate them without a nil check on a failed page or an unextracted child.

### Coordinates

`Part.Width`/`Height` and `Loc.BBox` describe the part **as displayed**: origin top-left, y increasing downward. For PDF that is the CropBox after `/Rotate` and `UserUnit` are applied, in points. A box drawn at `BBox` on a rendered image of the page encloses the block.

### Span offsets are bytes

`Span.Start` and `Span.End` are **byte offsets into the UTF-8 text**, not character offsets. They always fall on character boundaries. Languages that index strings by code point must convert. In Python:

```python
text.encode("utf-8")[span["start"]:span["end"]].decode("utf-8")
```

### Images

`Image.ID` identifies the image resource, not the block. Equal IDs mean equal bytes, and several blocks may share one ID (a logo on every page). With `ImagesData`, the bytes are guaranteed only on the first block with a given ID in document order; repeats may omit them.

## Warnings

A warning is a fact about what happened. It never recommends an action.

```json
{"code": "part.image_only", "severity": "warning", "message": "1 image, no text operators"}
```

Warnings local to a part are in `Part.Warnings`; those about the whole file are in `Document.Warnings`. `doc.AllWarnings()` iterates both, and `part.Has(code)` tests one part.

| Severity | Meaning |
|---|---|
| `info` | Something notable happened; the output is complete |
| `warning` | The output is present but degraded, or may not represent what a reader of the source would see |
| `error` | Content is missing or untrustworthy |

Shared codes (constants `Warn*`):

| Code | What happened |
|---|---|
| `part.failed` | The part could not be parsed; its blocks are missing or incomplete |
| `part.no_text` | The part was parsed and contains no text and no images |
| `part.image_only` | The part was parsed and contains no text, but does contain image content. Reported whatever `Options.Images` is set to |
| `text.unmapped_glyphs` | Some text has no Unicode mapping in the source and appears as U+FFFD or is absent |
| `image.unsupported_encoding` | Image data was requested and an image uses an encoding the extractor cannot deliver; its block has no data |
| `input.repaired` | The input was structurally damaged and was read by a recovery path |
| `limit.exceeded` | A limit was reached during extraction; the output is truncated there |
| `child.not_extracted` | An embedded document is listed but was not extracted |
| `child.unsupported` | No extractor handles the embedded document's format, or it uses an unimplemented feature |
| `child.encrypted` | The embedded document is encrypted and was not opened (no password, or the wrong one) |
| `child.failed` | Extraction of an embedded document failed for any other reason |

An extractor may add codes of its own under a format prefix, such as `pdf.xref_rebuilt`.

This is how a caller handles scanned pages without go-fast knowing anything about OCR:

```go
for i := range doc.Parts {
    p := &doc.Parts[i]
    if p.Has(gofastcore.WarnPartImageOnly) {
        sendToMyOCR(path, p.Number) // the caller's decision, not ours
    }
}
```

## Extractor interface

```go
type Extractor interface {
    Formats() []string
    Extract(ctx context.Context, r io.ReaderAt, size int64, opts *Options) (*Document, error)
}
```

`Options` (zero value: text and structure only, whole input, all CPUs):

| Field | Purpose |
|---|---|
| `Workers` | Goroutines inside one document; 0 means all CPUs |
| `Images` | `ImagesNone`, `ImagesMeta` (position and size), `ImagesData` (bytes too) |
| `Spans` | Ask for inline formatting and links |
| `Password` | Tried on encrypted input, and passed down to embedded documents |
| `Parts` | `PartRange{First, Last}`, inclusive part numbers; zero value means all; a range that selects nothing is `ErrNoPartsSelected` |
| `Limits` | `MaxInputBytes`, `MaxDecodedBytes`, `MaxParts`, `MaxDepth`. Zero means the extractor's default; negative means no limit |
| `Child` | Function that extracts embedded documents; see below |
| `Name` | Recorded as `Source.Name` |

### Errors

Extractors wrap these, so test with `errors.Is`: `ErrPasswordRequired`, `ErrWrongPassword`, `ErrUnsupported`, `ErrMalformed`, `ErrLimitExceeded`, `ErrNoPartsSelected`.

Only `MaxInputBytes` produces `ErrLimitExceeded`. Every other limit truncates the document and adds a `limit.exceeded` warning with severity `error`. `MaxParts` returns the first `MaxParts` parts of the requested range, also where the part count is known up front.

### Embedded documents

An email attachment may be a PDF; a PDF may embed a spreadsheet. Extractors do not import each other. The caller injects a dispatcher as `Options.Child`, and an extractor hands every embedded file to `opts.ExtractChild(...)`, appending the result to `Children`:

```go
doc.Children = append(doc.Children, opts.ExtractChild(ctx, name, mime, reader, size))
```

`ExtractChild` always returns a document, so every embedded file is at least listed with its name and size. When the child was not extracted, the result has no parts and one warning whose code says why: `child.not_extracted` (no `Child` supplied, or depth limit reached), `child.unsupported`, `child.encrypted` or `child.failed`. The last three are chosen from the error the child extractor returned, so a dispatcher should return `ErrUnsupported` for formats it has no extractor for.

## Schema evolution

`schema_version` changes when a field is removed or its meaning changes. Additions do not change it, including new block kinds: a decoder that meets a kind, part kind, origin or severity it does not know maps it to that enum's `Unknown` value, and unknown style names are ignored. An unknown block renders as a paragraph. Re-encoding such a document writes `"unknown"`, so the original name is lost. `Validate` rejects `Unknown`, since extractors must never emit it.

## How each format maps

| Format | Part kind | Notes |
|---|---|---|
| pdf | `page` | `BBox` on blocks; structure is `inferred` unless the PDF is tagged or the outline confirms a heading |
| docx, odt, rtf | one `body` | Heading styles give levels; comments become `note` |
| pptx, odp | `slide` | `Name` is the slide title; speaker notes become `note` |
| xlsx, ods, csv | `sheet` | `Name` is the sheet name; each used region is a `table` with `Loc.Range` |
| html, md, txt | one `body` | `Loc.Offset`/`Length` into the source |
| epub | `chapter` | One per spine item; nav becomes `Outline` |
| eml, msg, mbox | `message` | Headers in `Metadata.Extra`; attachments in `Children` |

## Porting go-fast-pdf

This is a clean break: no compatibility shim for the old `pkg/loader` API or JSON.

| Today (`go-fast-pdf/pkg/model`) | Shared model |
|---|---|
| `Document.Pages[]` | `Document.Parts[]` with `Kind: PartPage` |
| `Page.PageNumber` | `Part.Number` (the page's ordinal, also when a range is extracted); a page label such as `iv` goes in `Part.Name` |
| `Page.Width`, `Height` | `Part.Width`, `Height`, as displayed (CropBox after rotation) |
| `Page.Content` (one string) | `Part.Blocks[]`; `part.Markdown()` or `part.Text()` gives a string |
| `Page.CharCount` | Dropped; derivable from the blocks |
| `Page.Images[]` | `image` blocks placed in reading order |
| `Image.ID` (`Im1`, page-local) | `Image.ID` from the object number (`obj-12`); synthetic for inline images |
| `Image.Width`, `Height` | Same, now `int` |
| `Image.Rect` `[x, y, w, h]`, bottom-left origin | `Block.Loc.BBox`, displayed top-left space |
| `Image.Type`, `ColorSpace` | `Block.Attrs["inline"]`, `Attrs["color_space"]` |
| `Metadata.Title`, `Encrypted` | Same names |
| `Metadata.Author` | `Metadata.Authors`, one entry, not split |
| PDF `/Keywords` | `Metadata.Keywords`, one entry, not split |
| `Metadata.Creator` | `Metadata.Application` |
| `Metadata.Producer` | `Metadata.Extra["producer"]` |
| PDF bookmarks (not extracted today) | `Document.Outline` |
| `LoadPDF` / `LoadPDFConcurrent` | `gofastpdf.Extract(ctx, r, size, opts)` |

The PDF library needs a baseline layout stage (lines, paragraphs, column order) before its first release under this model, because of contract rules 2 and 3. Headings, lists, tables and header/footer tagging can follow incrementally without a schema change; until then every text block is a `paragraph` with `OriginInferred`.

## Example output

```json
{
  "schema_version": "0.1",
  "source": {"format": "pdf", "name": "report.pdf"},
  "metadata": {"title": "Q3 Report", "authors": ["A. Writer"]},
  "outline": [{"title": "Q3 Report", "level": 1, "part": 1, "y": 72}],
  "parts": [
    {
      "kind": "page", "number": 1, "width": 612, "height": 792,
      "blocks": [
        {"kind": "heading", "level": 1, "text": "Q3 Report",
         "loc": {"bbox": {"x0": 72, "y0": 72, "x1": 300, "y1": 96}}},
        {"kind": "paragraph", "origin": "inferred", "text": "Revenue grew in every region."},
        {"kind": "table", "origin": "inferred", "table": {"header_rows": 1,
         "rows": [["Region", "Rev"], ["EMEA", "1.2M"]]}}
      ]
    },
    {
      "kind": "page", "number": 2, "width": 612, "height": 792,
      "blocks": [],
      "warnings": [{"code": "part.image_only", "severity": "warning"}]
    }
  ],
  "warnings": [{"code": "input.repaired", "severity": "info", "message": "xref rebuilt"}]
}
```

## Deferred

- **Streaming.** No shared `Sink` in 0.1, but nothing prevents one: parts are self-contained, carry their own warnings, and need not be numbered contiguously. One conflict must be settled before a sink is designed. Some inferred structure needs the whole document (PDF heading levels need the document-wide body font size; header/footer tagging compares pages), so a streaming extractor either offers only page-local inference or runs a statistics pass first. That decides whether a sink receives final parts or provisional ones. When it is added, it should come with a serialized form (one header line, one line per part, one trailer line) for the CLI and non-Go consumers.
- **Numeric confidence.** Only if measurements from the PDF layout work show that a score would be meaningful.
- **Typed table cells.** Cells are strings. Typed values can be added alongside if a consumer needs them.
